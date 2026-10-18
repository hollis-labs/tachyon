// Package server assembles Tachyon's same-origin API on top of app-host.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/libs/app/app-host/plugins"
	pluginhost "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/transcripts"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Version is stamped at build time.
var Version = "0.0.0-dev"

// Config is the host's resolved configuration.
type Config struct {
	// PluginBinDir holds built plugin executables named by plugin ID.
	PluginBinDir          string
	DataDir               string
	CacheDir              string
	CachePolicyFile       string
	ChatSyncInterval      time.Duration
	ApprovalQueueURL      string
	FragmentsURL          string
	FragmentsTokenFile    string
	TranscriptMaxSessions int
	Upstreams             catalog.Upstreams
}

// pluginState is one catalog entry's admission result.
type pluginState struct {
	entry   catalog.Entry
	running bool
	reason  string
}

// Server owns the plugin inventory and the app routes.
type Server struct {
	cfg       Config
	inventory *plugins.Inventory
	states    []pluginState
	instance  string
	store     *localstore.Store
	objects   *objects.Service
	cache     *apicache.Cache
	chat      *chatbridge.Owned
}

// New resolves the catalog against built binaries. Missing binaries are
// reported, not fatal: a host without a plugin still serves its UI.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, instance: fmt.Sprintf("tachyon-%d", time.Now().UnixNano())}
	var specs []pluginhost.Spec
	for _, entry := range catalog.Entries() {
		bin, err := filepath.Abs(filepath.Join(cfg.PluginBinDir, entry.ID))
		if err != nil {
			s.states = append(s.states, pluginState{entry: entry, reason: "bad plugin dir: " + err.Error()})
			continue
		}
		if info, err := os.Stat(bin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			s.states = append(s.states, pluginState{entry: entry, reason: "not built: " + bin})
			log.Printf("plugin %s: not built (%s); run `make plugins`", entry.ID, bin)
			continue
		}
		specs = append(specs, s.spec(entry, bin))
		s.states = append(s.states, pluginState{entry: entry, running: true})
	}
	s.inventory = plugins.NewInventory(specs)
	return s
}

func (s *Server) spec(entry catalog.Entry, bin string) pluginhost.Spec {
	dataDir := filepath.Join(s.cfg.DataDir, entry.ID)
	cacheDir := filepath.Join(s.cfg.CacheDir, entry.ID)
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.MkdirAll(cacheDir, 0o700)
	var secrets []string
	if s.cfg.Upstreams.TorqueToken != "" {
		secrets = append(secrets, s.cfg.Upstreams.TorqueToken)
	}
	if s.cfg.Upstreams.GitHubToken != "" {
		secrets = append(secrets, s.cfg.Upstreams.GitHubToken)
	}
	return pluginhost.Spec{
		ID:      entry.ID,
		Command: bin,
		Dir:     filepath.Dir(bin),
		Env:     entry.Env(s.cfg.Upstreams),
		Secrets: secrets,
		Init: subprocess.InitParams{
			HostInfo:           subprocess.HostInfo{Version: Version, Protocol: subprocess.ProtocolVersion},
			PluginDir:          filepath.Dir(bin),
			DataDir:            dataDir,
			CacheDir:           cacheDir,
			CapabilityContract: capability.ContractVersion,
			Incarnation:        capability.RuntimeIdentity{HostInstance: s.instance, OwnerID: entry.ID, OwnerGeneration: 1},
			Grants:             capability.GrantSet{},
		},
	}
}

// Start admits every built plugin. Admission is all-or-nothing.
func (s *Server) Start(ctx context.Context) error {
	store, err := localstore.Open(ctx, filepath.Join(s.cfg.DataDir, "tachyon.db"))
	if err != nil {
		return fmt.Errorf("local database: %w", err)
	}
	settings, err := apicache.LoadSettings(ctx, store, s.cfg.CachePolicyFile)
	if err != nil {
		store.Close()
		return fmt.Errorf("cache policy: %w", err)
	}
	s.store = store
	s.objects = objects.New(store)
	s.cache = apicache.New(store, settings, s.cacheIdentity)

	if err := s.inventory.Start(ctx); err != nil {
		store.Close()
		return fmt.Errorf("plugin admission: %w", err)
	}
	source, sourceErr := chatbridge.NewSource(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile)
	s.chat = &chatbridge.Owned{Store: store, Source: source, SourceID: s.cfg.Upstreams.TetherAddr, ConfigError: sourceErr, Interval: s.cfg.ChatSyncInterval}
	s.chat.FreshSeconds = settings.Policies["chat-turns"].FreshSeconds
	s.chat.RevalidateSeconds = settings.Policies["chat-turns"].RevalidateSeconds
	s.chat.CacheEnabled = settings.Policies["chat-turns"].Enabled
	// Move existing outgoing messages from the earlier credential-keyed cache
	// identity into stable local ownership. Keep old rows as migration evidence.
	if previous, e := s.cacheIdentity("chat"); e == nil {
		next := apicache.Digest("chat-owner\n" + s.cfg.Upstreams.TetherAddr)
		if e = store.Writer.Submit(ctx, "chat_outbox_ownership", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO chat_outbox(identity,id,launch_id,body,created_at,status,error,session_id)
    SELECT ?,id,launch_id,body,created_at,status,error,session_id FROM chat_outbox WHERE identity=?
    ON CONFLICT(identity,id) DO NOTHING`, next, previous)
			return err
		}); e != nil {
			return fmt.Errorf("chat outbox migration: %w", e)
		}
	}
	s.chat.Start()
	return nil
}

// Stop retires plugin processes after HTTP has drained.
func (s *Server) Stop(ctx context.Context) error {
	if s.chat != nil {
		s.chat.Stop()
	}
	err := s.inventory.Stop(ctx)
	if s.store != nil {
		err = errors.Join(err, s.store.Close())
	}
	return err
}

func (s *Server) cacheIdentity(scope string) (string, error) {
	u := s.cfg.Upstreams
	addr, token := u.TorqueURL, u.TorqueToken
	switch scope {
	case "transcripts":
		return transcripts.New(s.cfg.FragmentsURL, s.cfg.FragmentsTokenFile, s.cfg.TranscriptMaxSessions).Identity()
	case "approvals":
		return apicache.Digest("approval-queue\n" + s.cfg.ApprovalQueueURL), nil
	case "catalog", "chat":
		addr, token = u.TetherAddr, ""
		if addr == "" {
			return "", errors.New("Tether address missing")
		}
		if err := upstreamauth.Guard(addr, u.TetherTokenFile); err != nil {
			return "", err
		}
		if u.TetherTokenFile != "" {
			var err error
			token, err = tokenfile.NewWatcher(u.TetherTokenFile, tokenfile.Options{}, nil).Token()
			if err != nil {
				return "", err
			}
		}
	case "chat-turns":
		return apicache.Digest("chat-owner\n" + u.TetherAddr), nil
	case "forge":
		return apicache.Digest("github\n" + u.GitHubOrg + "\n" + u.GitHubToken), nil
	default:
		parsed, err := url.Parse(addr)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return "", errors.New("Torque address invalid")
		}
		if err := upstreamauth.Guard(addr, token); err != nil {
			return "", err
		}
	}
	return apicache.Digest(addr + "\n" + token), nil
}

// Routes returns the handler mounted by app-host at /api/.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	transcriptHandler := http.Handler(transcripts.New(s.cfg.FragmentsURL, s.cfg.FragmentsTokenFile, s.cfg.TranscriptMaxSessions))
	if s.cache != nil {
		transcriptHandler = s.cache.Wrap(transcriptHandler)
	}
	mux.Handle(transcripts.Prefix+"/", transcriptHandler)
	operator := approvals.New(s.cfg.ApprovalQueueURL)
	// Object management uses live reads and server-verified operator mutations.
	// Keep the whole prefix outside the generic plugin response cache.
	mux.Handle("/api/plugins/console/v1/tether/", operator.RequireOperator(s.inventory.APIRoutes()))
	approvalHandler := http.Handler(operator)
	if s.cache != nil {
		approvalHandler = s.cache.Wrap(approvalHandler)
	}
	mux.Handle(approvals.Prefix+"/", approvalHandler)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})
	if s.store != nil {
		outbox := &chatbridge.Outbox{Store: s.store, Identity: func() (string, error) { return apicache.Digest("chat-owner\n" + s.cfg.Upstreams.TetherAddr), nil }, Next: s.inventory.APIRoutes()}
		handler := http.Handler(outbox)
		if s.cache != nil {
			handler = s.cache.Wrap(handler)
		}
		mux.Handle("GET "+chatbridge.ChatPrefix+"/outbox", handler)
		mux.Handle("POST "+chatbridge.ChatPrefix+"/agents/{launch}/send", operator.RequireOperator(handler))
	}
	if s.chat != nil {
		mux.Handle(chatbridge.Prefix+"/", operator.RequireOperator(s.chat))
	}
	mux.HandleFunc("GET /api/plugins", s.listPlugins)
	mux.HandleFunc("GET /api/plugins/{id}/stream/{name}", s.stream)
	if s.cache != nil {
		mux.HandleFunc(apicache.ImportedTeamsPath, s.cache.ImportedTeamsHandler)
		mux.HandleFunc("GET /api/cache/policy", s.cache.PolicyHandler)
		mux.Handle("/api/plugins/", s.cache.Wrap(s.inventory.APIRoutes()))
	} else {
		mux.Handle("/api/plugins/", s.inventory.APIRoutes())
	}
	return mux
}

type pluginView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Reason      string   `json:"reason,omitempty"`
	Streams     []string `json:"streams"`
}

func (s *Server) listPlugins(w http.ResponseWriter, _ *http.Request) {
	out := make([]pluginView, 0, len(s.states))
	for _, st := range s.states {
		v := pluginView{ID: st.entry.ID, Name: st.entry.Name, Version: st.entry.Version, Description: st.entry.Description, Streams: []string{}}
		for name := range st.entry.Streams {
			v.Streams = append(v.Streams, name)
		}
		switch _, ok := s.inventory.Client(st.entry.ID); {
		case ok:
			v.Status = "running"
		case st.running:
			v.Status = "stopped"
		default:
			v.Status, v.Reason = "unavailable", st.reason
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// stream relays a declared SSE upstream. The plugin must be admitted; the
// upstream is fixed by the catalog, never derived from the request path.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	var decl *catalog.Stream
	for _, st := range s.states {
		if st.entry.ID == id && st.running {
			if d, ok := st.entry.Streams[name]; ok {
				decl = &d
			}
		}
	}
	if decl == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown stream", "code": "not_found"})
		return
	}
	target, err := url.Parse(decl.Upstream(s.cfg.Upstreams))
	if err != nil || target.Host == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream upstream misconfigured", "code": "unavailable"})
		return
	}
	var bearer string
	if decl.Bearer != nil {
		bearer = decl.Bearer(s.cfg.Upstreams)
	}
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := *target
			out.RawQuery = pr.In.URL.RawQuery
			pr.Out.URL = &out
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			if bearer != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+bearer)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream upstream unavailable", "code": "unavailable"})
		},
	}
	proxy.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

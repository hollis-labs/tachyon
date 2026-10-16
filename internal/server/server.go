// Package server assembles Tachyon's same-origin API on top of app-host.
package server

import (
	"context"
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

	"github.com/hollis-labs/tachyon/internal/catalog"
)

// Version is stamped at build time.
var Version = "0.0.0-dev"

// Config is the host's resolved configuration.
type Config struct {
	// PluginBinDir holds built plugin executables named by plugin ID.
	PluginBinDir string
	DataDir      string
	CacheDir     string
	Upstreams    catalog.Upstreams
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
	if err := s.inventory.Start(ctx); err != nil {
		return fmt.Errorf("plugin admission: %w", err)
	}
	return nil
}

// Stop retires plugin processes after HTTP has drained.
func (s *Server) Stop(ctx context.Context) error { return s.inventory.Stop(ctx) }

// Routes returns the handler mounted by app-host at /api/.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})
	mux.HandleFunc("GET /api/plugins", s.listPlugins)
	mux.HandleFunc("GET /api/plugins/{id}/stream/{name}", s.stream)
	mux.Handle("/api/plugins/", s.inventory.APIRoutes())
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

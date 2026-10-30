// Package server assembles Tachyon's same-origin API on top of app-host.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/libs/app/app-host/plugins"
	pluginhost "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/agentsapi"
	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/agentsderive"
	"github.com/hollis-labs/tachyon/internal/agentsserve"
	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/appagent"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/carddelivery"
	"github.com/hollis-labs/tachyon/internal/cardevents"
	"github.com/hollis-labs/tachyon/internal/cardfx"
	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/chatactivity"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/chatthreads"
	"github.com/hollis-labs/tachyon/internal/chattopics"
	"github.com/hollis-labs/tachyon/internal/chatworkspace"
	"github.com/hollis-labs/tachyon/internal/dataverbs"
	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/itemstate"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/objectsync"
	"github.com/hollis-labs/tachyon/internal/planner"
	"github.com/hollis-labs/tachyon/internal/provideractions"
	"github.com/hollis-labs/tachyon/internal/tesseractread"
	"github.com/hollis-labs/tachyon/internal/torqueread"
	"github.com/hollis-labs/tachyon/internal/transcripts"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Version is stamped at build time.
var Version = "0.0.0-dev"

// Config is the host's resolved configuration.
type Config struct {
	// PluginBinDir holds built plugin executables named by plugin ID.
	PluginBinDir     string
	DataDir          string
	CacheDir         string
	CachePolicyFile  string
	ChatSyncInterval time.Duration
	// AgentsStubs mounts the fixture-backed Agents monitor contract (TACHYON_AGENTS_STUBS=1) for GUI development.
	AgentsStubs bool
	// ForgeStubs mounts the fixture-backed Pull requests contract (TACHYON_FORGE_STUBS=1) for GUI development.
	ForgeStubs bool
	// AgentsCollector runs the always-on Agents collector (TACHYON_AGENTS_COLLECTOR=0 turns it off for debugging).
	// It needs a Tether address; the read-scope token is the usual TETHER_TOKEN_FILE.
	AgentsCollector bool
	// ChatStubs mounts the fixture-backed chat contract (TACHYON_CHAT_STUBS=1) for GUI development.
	ChatStubs bool
	// ChatClientContext sends shared workspace items to the app agent as client_context on each
	// turn (TACHYON_CHAT_CLIENT_CONTEXT=1). Off until Nanite confirms the field.
	ChatClientContext bool
	// AppAgentConfigFile enables the in-app agent chat (see appagent.NaniteConfig). Empty disables it.
	AppAgentConfigFile          string
	ApprovalQueueURL            string
	ApprovalProducerConfigFile  string
	ApprovalAssuranceConfigFile string
	// Optional host adapter; nil uses the configured current-workload HTTP client.
	ApprovalCurrentWorkloadTrust approvals.CurrentWorkloadTrust
	FragmentsURL                 string
	FragmentsTokenFile           string
	TranscriptMaxSessions        int
	// ActionHub configures the ssh transport to the hub for registered actions.
	// With no address the hub machine is unavailable; agent-os runs locally.
	ActionHub actionrun.SSH
	// ActionDetachGrace keeps an interactive run alive this long after its last
	// terminal closes (default 0: end it at once).
	ActionDetachGrace time.Duration
	// CardEffectsTorque lets an approved card change Torque task statuses.
	CardEffectsTorque bool
	// CardDeliveryFile is the routing profile and sink file for pushing card
	// outcomes to askers. Empty leaves card delivery pull-only.
	CardDeliveryFile string
	// CardEvents configures card.* event publishing to the hub; off by default.
	CardEvents struct {
		Enabled   bool
		URL       string // HOLLIS_EVENTS_URL
		TokenFile string // HOLLIS_AUTH_TOKEN_FILE_TACHYON
	}
	Upstreams catalog.Upstreams
}

// pluginState is one catalog entry's admission result.
type pluginState struct {
	entry   catalog.Entry
	running bool
	reason  string
}

// Server owns the plugin inventory and the app routes.
type Server struct {
	cfg              Config
	inventory        *plugins.Inventory
	states           []pluginState
	instance         string
	store            *localstore.Store
	objects          *objects.Service
	objectJobs       *objectsync.Jobs
	cache            *apicache.Cache
	chat             *chatbridge.Owned
	appAgent         *appagent.Service
	chatThreads      *chatthreads.Handler
	chatWorkspace    *chatworkspace.Handler
	chatTopics       *chattopics.Handler
	chatPlan         *chatplan.Service
	chatActivity     *chatactivity.Handler
	approvalProducer *approvals.Producer
	approvalInbox    *approvals.Inbox
	approvalTrusted  *approvals.TrustedAPI
	approvalCancel   context.CancelFunc
	agentsCollector  *agentscollector.Collector
	itemState        *itemstate.Store
	actions          *actionrun.Service
	cardFx           *cardfx.Store
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
	started := false
	defer func() {
		if !started {
			if s.approvalCancel != nil {
				s.approvalCancel()
			}
			if s.objectJobs != nil {
				_ = s.objectJobs.Close()
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.inventory.Stop(cleanup)
			_ = store.Close()
		}
	}()

	producerConfig, err := approvals.LoadProducerConfig(s.cfg.ApprovalProducerConfigFile)
	if err != nil {
		return fmt.Errorf("approval producer config: %w", err)
	}
	approvalContext, approvalCancel := context.WithCancel(ctx)
	s.approvalCancel = approvalCancel
	s.approvalInbox = approvals.NewInbox(&approvals.Store{DB: store}, approvals.New(s.cfg.ApprovalQueueURL))
	s.approvalProducer, err = approvals.NewProducer(approvalContext, s.approvalInbox.Service, producerConfig)
	if err != nil {
		return fmt.Errorf("approval producer trust: %w", err)
	}
	if s.cfg.ApprovalProducerConfigFile != "" && producerConfig.Issuer != "" {
		// A changed producer config (also on SIGHUP) takes effect without a restart;
		// a bad edit is logged and the last good configuration keeps serving.
		go func() {
			_ = s.approvalProducer.Watch(approvalContext, s.cfg.ApprovalProducerConfigFile, 0, slog.Default())
		}()
	}

	if s.cfg.AppAgentConfigFile != "" {
		// The adapter re-reads this file on every call, so reinstalling the agent
		// (a new definition revision gets a new host row) needs no restart.
		s.appAgent = &appagent.Service{Store: &appagent.Store{DB: store}, Runtime: appagent.NewNanite(s.cfg.AppAgentConfigFile), Label: "Tachyon"}
		if err := s.appAgent.Start(ctx); err != nil {
			return fmt.Errorf("app agent: %w", err)
		}
	}
	s.startActions(approvalContext, store)
	s.startCardEffects(approvalContext, store)
	s.startCardEvents(approvalContext, store)
	s.startAgentsCollector(approvalContext, store)
	s.startItemState(approvalContext, store)
	s.startCardDelivery(approvalContext, store)

	assuranceConfig, err := approvals.LoadAssuranceConfig(s.cfg.ApprovalAssuranceConfigFile)
	if err != nil {
		return fmt.Errorf("approval assurance config: %w", err)
	}
	s.approvalTrusted, err = approvals.NewTrustedAPI(s.approvalInbox.Store, assuranceConfig, s.cfg.ApprovalCurrentWorkloadTrust, nil)
	if err != nil {
		return fmt.Errorf("approval assurance trust: %w", err)
	}

	if err := s.inventory.Start(ctx); err != nil {
		return fmt.Errorf("plugin admission: %w", err)
	}
	source, sourceErr := chatbridge.NewSource(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile)
	s.chat = &chatbridge.Owned{Store: store, Source: source, SourceID: s.cfg.Upstreams.TetherAddr, ConfigError: sourceErr, Interval: s.cfg.ChatSyncInterval}
	// The unified chat thread list and per-thread stream (both sources).
	s.chatThreads = &chatthreads.Handler{
		Agg: &chatthreads.Aggregator{App: s.appAgent, Chat: s.chat, State: &chatthreads.StateStore{DB: store}},
		Hub: &chatthreads.Hub{DB: store},
	}
	// Activity feed and context meter. The service seams below record into it; a failed record
	// never fails the change that caused it.
	rec := &chatactivity.Recorder{Store: &chatactivity.Store{DB: store}, Publish: func(ctx context.Context, thread, typ string, fields map[string]any) {
		_, _ = s.chatThreads.Hub.Publish(ctx, thread, typ, fields)
	}}
	s.chatActivity = &chatactivity.Handler{Rec: rec, Exists: func(ctx context.Context, ref string) error { _, err := s.chatThreads.Agg.Get(ctx, ref); return err }}
	ws := &chatworkspace.Store{DB: store, Dir: filepath.Join(s.cfg.DataDir, "workspace")}
	s.chatWorkspace = &chatworkspace.Handler{
		Store:  ws,
		Exists: func(ctx context.Context, ref string) error { _, err := s.chatThreads.Agg.Get(ctx, ref); return err },
		Published: func(ctx context.Context, thread, op, id string) {
			_, _ = s.chatThreads.Hub.Publish(ctx, thread, chatapi.EventWorkspace, map[string]any{"op": op, "id": id})
			if op == "share" {
				name := id
				if it, err := ws.Get(ctx, thread, id); err == nil {
					name = it.Name
				}
				rec.Quiet(ctx, thread, "file.shared", "user", "tachyon", []chatactivity.Cite{{Kind: "file", Ref: id, Label: name}}, chatactivity.Card("Shared "+name, "Shared with the agent", nil))
			}
		},
	}
	s.chatTopics = &chattopics.Handler{
		Store:      &chattopics.Store{DB: store},
		Exists:     func(ctx context.Context, ref string) error { _, err := s.chatThreads.Agg.Get(ctx, ref); return err },
		MessageSeq: s.chatThreads.MessageSeq,
		Published: func(ctx context.Context, thread, typ, op, id string) {
			_, _ = s.chatThreads.Hub.Publish(ctx, thread, typ, map[string]any{"op": op, "id": id})
			if op == "add" {
				rec.Quiet(ctx, thread, typ+".added", "user", "tachyon", nil, chatactivity.Card(strings.ToUpper(typ[:1])+typ[1:]+" added", "", nil))
			}
		},
	}
	s.chatPlan = &chatplan.Service{
		Store:  &chatplan.Store{DB: store},
		Exists: func(ctx context.Context, ref string) error { _, err := s.chatThreads.Agg.Get(ctx, ref); return err },
		ProjectOf: func(ctx context.Context, ref string) (string, error) {
			st, err := s.chatThreads.Agg.State.Get(ctx, ref)
			return st.ProjectID, err
		},
		Publish: func(ctx context.Context, thread, typ string, fields map[string]any) {
			_, _ = s.chatThreads.Hub.Publish(ctx, thread, typ, fields)
		},
		Note: func(ctx context.Context, thread string, p chatplan.Plan) {
			t, err := chatapi.ParseRef(thread)
			if err != nil || t.Source != chatapi.SourceApp || s.appAgent == nil {
				return
			}
			title := "Plan"
			if p.Title != nil && *p.Title != "" {
				title = *p.Title
			}
			_, _ = s.appAgent.AppendNote(ctx, t.ID, "plan", title, map[string]any{"plan_id": p.ID})
		},
		Notify: s.chatThreads.Notify,
		Activity: func(ctx context.Context, thread, by, reason string, p chatplan.Plan) {
			actor := "user"
			if by == "agent" {
				actor = "agent"
			}
			pr := p.Progress()
			title := "Plan"
			if p.Title != nil && *p.Title != "" {
				title = *p.Title
			}
			rec.Quiet(ctx, thread, "plan.changed", actor, "tachyon", nil, chatactivity.Card(title+": "+reason, fmt.Sprintf("%d of %d steps done", pr.Done, pr.Total), nil))
		},
	}
	s.chatThreads.Agg.PlanProgress = func(ctx context.Context, refs []string) map[string]chatthreads.Progress {
		out := map[string]chatthreads.Progress{}
		for ref, pr := range s.chatPlan.Store.Progress(ctx, refs) {
			out[ref] = chatthreads.Progress{Done: pr.Done, Total: pr.Total}
		}
		return out
	}
	if s.appAgent != nil && s.cfg.ChatClientContext {
		s.appAgent.ClientContext = func(ctx context.Context, id string) any {
			cc, err := ws.ClientContextFor(ctx, chatapi.ThreadRef{Source: chatapi.SourceApp, ID: id}.String())
			if err != nil || cc == nil {
				return nil
			}
			return cc
		}
	}
	if s.appAgent != nil {
		s.appAgent.OnTool = func(id string, t appagent.Tool) {
			status := t.Status
			if status != "ok" && status != "error" {
				return
			}
			rec.Quiet(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceApp, ID: id}.String(), "tool.call", "agent", "runtime", nil, chatactivity.Card(t.Name, status, nil))
		}
		s.appAgent.OnContext = func(id string, used, window int64, model string) {
			rec.SetContext(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceApp, ID: id}.String(), used, window, model)
		}
		s.appAgent.OnChange = func(id string) {
			s.chatThreads.Notify(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceApp, ID: id}.String())
		}
	}
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
	if _, err := s.chat.BackfillSessionLinks(ctx, apicache.Digest("chat-owner\n"+s.cfg.Upstreams.TetherAddr)); err != nil {
		return fmt.Errorf("chat session identity backfill: %w", err)
	}
	sources, err := objectsync.Sources(s.cfg.Upstreams)
	if err != nil {
		return fmt.Errorf("object sync sources: %w", err)
	}
	s.objectJobs, err = objectsync.OpenJobs(ctx, filepath.Join(s.cfg.DataDir, "jobs.db"), objectsync.New(store), sources)
	if err != nil {
		return fmt.Errorf("object sync jobs: %w", err)
	}
	// No schedules are created on boot. Operators validate a source with
	// sync-objects --enable; only those persisted jobs recur across restarts.
	s.objectJobs.Engine.Start()
	s.chat.Start()
	started = true
	return nil
}

// Stop retires plugin processes after HTTP has drained.
func (s *Server) Stop(ctx context.Context) error {
	if s.approvalCancel != nil {
		s.approvalCancel()
	}
	if s.agentsCollector != nil {
		s.agentsCollector.Wait(ctx) // its last batch must land before the store closes
	}
	var jobErr error
	if s.objectJobs != nil {
		jobErr = s.objectJobs.Close()
	}
	if s.chat != nil {
		s.chat.Stop()
	}
	if s.appAgent != nil {
		s.appAgent.Stop()
	}
	err := errors.Join(jobErr, s.inventory.Stop(ctx))
	if s.store != nil {
		err = errors.Join(err, s.store.Close())
	}
	return err
}

func (s *Server) cacheIdentity(scope string) (string, error) {
	u := s.cfg.Upstreams
	addr, token := u.TorqueURL, u.TorqueToken
	switch scope {
	case "activity":
		return apicache.Digest("activity-monitor\n" + u.ActivityURL), nil
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
	if s.objects != nil {
		// Local data is live and host-owned, independent of subprocess admission.
		providers := []dataverbs.Provider{dataverbs.NewLocal(s.objects)}
		if s.store != nil {
			providers = append(providers, &dataverbs.Actions{Service: &provideractions.Service{
				Store: s.store,
				Bind:  provideractions.Tether(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile),
			}})
		}
		mux.Handle(dataverbs.Prefix+"/", dataverbs.NewHTTP(providers, operator.RequireOperator))
		if s.store != nil {
			var cardTools *dataverbs.Cards
			if s.approvalInbox != nil {
				cardTools = &dataverbs.Cards{Service: s.approvalInbox.Service, Registry: approvals.CardRegistry, Receipt: s.cardReceiptView}
			}
			// Read-only Tesseract search and read for agents; nil (feature off) when no address is set.
			tesseractReader, err := tesseractread.New(s.cfg.Upstreams.TesseractURL, s.cfg.Upstreams.TesseractTokenFile)
			if err != nil {
				slog.Warn("tesseract reads for agents disabled", "error", err)
				tesseractReader = nil
			}
			var chatTools *dataverbs.ChatWorkspace
			if s.chatWorkspace != nil {
				chatTools = &dataverbs.ChatWorkspace{Store: s.chatWorkspace.Store, Thread: s.chatWorkspace.Exists}
			}
			var topicTools *dataverbs.ChatTopics
			if s.chatTopics != nil {
				topicTools = &dataverbs.ChatTopics{Store: s.chatTopics.Store, Thread: s.chatTopics.Exists, MessageSeq: s.chatTopics.MessageSeq,
					Published: func(ctx context.Context, thread, op, id string) {
						_, _ = s.chatThreads.Hub.Publish(ctx, thread, chatapi.EventTopic, map[string]any{"op": op, "id": id})
						if op == "add" && s.chatActivity != nil {
							s.chatActivity.Rec.Quiet(ctx, thread, "topic.added", "agent", "tachyon", nil, chatactivity.Card("Topic added", "", nil))
						}
					}}
			}
			var activityTools *dataverbs.ChatActivity
			if s.chatActivity != nil {
				activityTools = &dataverbs.ChatActivity{Rec: s.chatActivity.Rec, Thread: s.chatActivity.Exists}
			}
			var planTools *dataverbs.ChatPlan
			if s.chatPlan != nil {
				planTools = &dataverbs.ChatPlan{Service: s.chatPlan}
			}
			mux.Handle(dataverbs.AgentPrefix+"/", dataverbs.NewAgentHTTP(s.objects, &agentaccess.Store{DB: s.store}, s.actions, cardTools, torqueread.New(s.cfg.Upstreams.TorqueURL, s.cfg.Upstreams.TorqueToken), tesseractReader, chatTools, topicTools, planTools, activityTools))
			mux.Handle(dataverbs.AgentMCPPath, dataverbs.NewAgentMCP(s.objects, &agentaccess.Store{DB: s.store}, s.actions, cardTools, torqueread.New(s.cfg.Upstreams.TorqueURL, s.cfg.Upstreams.TorqueToken), tesseractReader, chatTools, topicTools, planTools, activityTools))
		}
	}
	if s.store != nil {
		ownedPlanner := planner.NewHTTP(s.store, planner.NewTasks(s.cfg.Upstreams.TorqueURL, s.cfg.Upstreams.TorqueToken))
		mux.Handle(planner.Prefix+"/", operator.RequireOperator(ownedPlanner))
		mux.Handle(planner.LegacyPrefix+"/", operator.RequireOperator(ownedPlanner))
		mux.HandleFunc("GET /api/plugins/planner/stream/events", ownedPlanner.Events)
		for _, prefix := range []string{planner.Prefix, "/api/plugins/planner/v1"} {
			mux.HandleFunc("GET "+prefix+"/tasks/{task}", ownedPlanner.TaskDetails)
			mux.HandleFunc("GET "+prefix+"/tasks/{task}/comments", ownedPlanner.TaskDetails)
		}
	}
	// Object management uses live reads and server-verified operator mutations.
	// Keep the whole prefix outside the generic plugin response cache.
	tetherRoutes := s.inventory.APIRoutes()
	mux.Handle("/api/plugins/console/v1/tether/", operator.RequireOperator(tetherRoutes))
	// Plan and agent reads expose hub objects, so unlike the rest of the prefix
	// they need operator assurance on GET as well.
	for _, p := range []string{"plans", "agents"} {
		mux.Handle("/api/plugins/console/v1/tether/"+p+"/", operator.RequireOperatorReads(tetherRoutes))
	}
	// The cite overlay's reads of Torque epics, projects, sprints and plans: projected by the plugin, and like the
	// Tether plan/agent reads they need operator assurance on GET. Outside the plugin response cache.
	mux.Handle("/api/plugins/console/v1/preview/", operator.RequireOperatorReads(tetherRoutes))
	trusted := s.approvalTrusted
	if trusted == nil {
		trusted = &approvals.TrustedAPI{}
	}
	mux.Handle(approvals.TrustedPrefix+"/", trusted)

	// Native queue reads are authoritative and never use the former Tangent cache.
	if s.store != nil {
		inbox := s.approvalInbox
		if inbox == nil {
			inbox = approvals.NewInbox(&approvals.Store{DB: s.store}, operator)
		}
		producer := s.approvalProducer
		if producer == nil {
			producer = &approvals.Producer{Service: inbox.Service}
		}
		if s.actions != nil && inbox == s.approvalInbox {
			inbox.ActionRun = s.actionRunView
		}
		if inbox == s.approvalInbox {
			inbox.CardReceipt = s.cardReceiptView
		}
		if s.actions != nil {
			mux.Handle(actionrun.Prefix+"/", operator.RequireOperatorReads(&actionrun.Handler{Service: s.actions}))
		}
		mux.Handle(approvals.Prefix+"/", inbox)
		mux.Handle(approvals.ProducerPrefix+"/", producer)
	} else {
		mux.HandleFunc(approvals.Prefix+"/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == approvals.Prefix+"/session" || r.URL.Path == approvals.Prefix+"/status" || r.URL.Path == approvals.Prefix+"/elevate" || r.URL.Path == approvals.Prefix+"/de-elevate" {
				operator.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Approval storage unavailable", http.StatusServiceUnavailable)
		})
	}
	if s.store != nil {
		elevation := approvals.ElevationSettings{DB: s.store}
		// Under the operator cookie's Path (approvals.CookiePath) so the browser
		// actually sends it; RequireOperator reads that cookie.
		mux.HandleFunc("GET "+approvals.ElevationSettingsPath, elevation.Handler)
		mux.Handle("PUT "+approvals.ElevationSettingsPath, operator.RequireOperator(http.HandlerFunc(elevation.Handler)))
	}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})
	if s.store != nil {
		outbox := &chatbridge.Outbox{Store: s.store, SourceID: s.cfg.Upstreams.TetherAddr, Identity: func() (string, error) { return apicache.Digest("chat-owner\n" + s.cfg.Upstreams.TetherAddr), nil }, Next: s.inventory.APIRoutes()}
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
	// Reads match the other chat history routes above (no operator session needed to
	// read); sends need operator assurance. Without a config file the routes answer
	// 503 and say why. Tightening reads is a one-word change to RequireOperatorReads.
	mux.Handle(appagent.Prefix+"/", operator.RequireOperator(&appagent.Handler{Service: s.appAgent, Disabled: "The app agent is not configured on this Tachyon (set TACHYON_APP_AGENT_CONFIG)."}))
	// Real chat thread routes, mounted one by one: each is more specific than the stub's
	// prefix below, so with TACHYON_CHAT_STUBS=1 these win and the stub serves the rest.
	// RequireOperator lets GETs through and guards every write.
	if s.chatThreads != nil {
		real := s.chatThreads.Mux()
		for _, route := range chatthreads.Routes {
			method, path, _ := strings.Cut(route, " ")
			mux.Handle(method+" "+chatapi.Prefix+path, operator.RequireOperator(real))
		}
	}
	if s.chatPlan != nil {
		real := (&chatplan.Handler{Service: s.chatPlan}).Mux()
		for _, route := range chatplan.Routes {
			method, path, _ := strings.Cut(route, " ")
			mux.Handle(method+" "+chatapi.Prefix+path, operator.RequireOperator(real))
		}
	}
	if s.chatActivity != nil {
		real := s.chatActivity.Mux()
		for _, route := range chatactivity.Routes {
			method, path, _ := strings.Cut(route, " ")
			mux.Handle(method+" "+chatapi.Prefix+path, operator.RequireOperator(real))
		}
	}
	if s.chatTopics != nil {
		real := s.chatTopics.Mux()
		for _, route := range chattopics.Routes {
			method, path, _ := strings.Cut(route, " ")
			mux.Handle(method+" "+chatapi.Prefix+path, operator.RequireOperator(real))
		}
	}
	if s.chatWorkspace != nil {
		real := s.chatWorkspace.Mux()
		for _, route := range chatworkspace.Routes {
			method, path, _ := strings.Cut(route, " ")
			mux.Handle(method+" "+chatapi.Prefix+path, operator.RequireOperator(real))
		}
		// File bytes need operator assurance on reads too.
		mux.Handle(chatworkspace.ContentRoute, operator.RequireOperatorReads(s.chatWorkspace.Content()))
	}
	// Chat contract stubs (dev only). Mounted explicitly: the /api/plugins/ catch-all below is
	// unguarded. Writes need an operator session; workspace content and the terminal family
	// need it on reads too. Real slices replace these mounts route by route.
	if s.cfg.ChatStubs {
		stub := chatapi.Stub{}
		if s.chatWorkspace == nil {
			mux.Handle("GET "+chatapi.WorkspaceContentPath, operator.RequireOperatorReads(stub.WorkspaceContent()))
		}
		mux.Handle(chatapi.Prefix+"/", operator.RequireOperator(stub.Chat()))
		mux.Handle(chatapi.TerminalPrefix+"/", operator.RequireOperatorReads(stub.Terminal()))
	}
	// Agents contract stubs (dev only). Mounted explicitly: the /api/plugins/ catch-all below is
	// unguarded. GETs are open; every write (nudge, restart, skip, settings) needs an operator session.
	if s.agentsCollector != nil {
		mux.Handle("GET "+agentscollector.Path, operator.RequireOperator(s.agentsCollector.Handler()))
		mux.Handle("GET "+agentscollector.BusPath, operator.RequireOperator(s.agentsCollector.BusHandler()))
		// Sessions, timeline, series, tokens, history, stream and settings. Controls (nudge, restart) are not here.
		(&agentsserve.Server{DB: s.store, Collector: s.agentsCollector}).Mount(mux, operator.RequireOperator)
	}
	if s.cfg.AgentsStubs {
		mux.Handle(agentsapi.Prefix+"/", operator.RequireOperator(agentsapi.Stub{}.Handler()))
	}
	// Forge (pull request) contract stubs (dev only), mounted explicitly under the forge and
	// console-state prefixes. GETs are open; every write needs an operator session.
	if s.cfg.ForgeStubs {
		h := operator.RequireOperator(forgeapi.Stub{}.Handler())
		mux.Handle(forgeapi.Prefix+"/", h)
		mux.Handle(forgeapi.StatePrefix, h)
		mux.Handle(forgeapi.StatePrefix+"/", h)
	}
	// Shared item-state store (PR2). GETs are open like every console read; PUT and bulk need an
	// operator session and refuse agents. With ForgeStubs on the forge stub owns this prefix instead.
	if s.itemState != nil && !s.cfg.ForgeStubs {
		h := operator.RequireOperator(s.itemState.Handler(forgeapi.StatePrefix))
		mux.Handle(forgeapi.StatePrefix, h)
		mux.Handle(forgeapi.StatePrefix+"/", h)
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
	return decodedRoute(mux)
}

// decodedRoute closes encoded-path aliases of guarded routes. The plugin router
// reads the decoded path, but the mux matches guarded prefixes on the escaped
// one, so plans%2f<id> or console%2fv1/... would skip the operator guard that the
// literal spelling gets. A request whose escaped path differs from the plain
// encoding of its decoded path is dispatched from the top on the decoded path:
// the same guards apply as for the literal route, and whatever falls through
// reaches the plugin exactly as before (it already routes on the decoded path).
func decodedRoute(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath != "" && r.URL.EscapedPath() != (&url.URL{Path: r.URL.Path}).EscapedPath() {
			again := r.Clone(r.Context())
			again.URL.RawPath = ""
			again.RequestURI = again.URL.RequestURI()
			r = again
		}
		mux.ServeHTTP(w, r)
	})
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
	if decl == nil || decl.Upstream == nil {
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
			if id == "console" && name == "activity" {
				q := url.Values{}
				for _, key := range []string{"since", "kind"} {
					if value := pr.In.URL.Query().Get(key); value != "" {
						q.Set(key, value)
					}
				}
				out.RawQuery = q.Encode()
				pr.Out.Header = http.Header{"Accept": {"text/event-stream"}}
				if cursor := pr.In.Header.Get("Last-Event-ID"); cursor != "" {
					pr.Out.Header.Set("Last-Event-ID", cursor)
				}
			}
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

// startActions wires the registered-action executor: agent-os runs locally and
// the hub over ssh when configured. A request can only name registry entries.
func (s *Server) startActions(ctx context.Context, store *localstore.Store) {
	transports := map[string]actionrun.Transport{actionrun.MachineAgentOS: actionrun.Local{}}
	if hub := s.cfg.ActionHub; hub.Addr != "" {
		if hub.PathPrefix == "" {
			hub.PathPrefix = "$HOME/.local/bin"
		}
		transports[actionrun.MachineHub] = &hub
	}
	items := &approvals.Store{DB: store}
	runs := &actionrun.Store{DB: store}
	manager := &actionrun.Manager{Store: runs, Items: items, Transports: transports, Base: ctx, DetachGrace: s.cfg.ActionDetachGrace}
	if err := manager.Recover(ctx); err != nil {
		slog.Warn("action runs not recovered", "err", err)
	}
	s.actions = &actionrun.Service{Store: runs, Manager: manager, Approval: s.approvalInbox.Service}
	go s.actions.Watch(ctx, 0)
}

// startCardEffects runs the effects of approved cards. The Torque target is off
// unless TACHYON_CARD_EFFECTS_TORQUE is set: letting an approval change Torque
// tasks is a deliberate grant, not a default.
func (s *Server) startCardEffects(ctx context.Context, store *localstore.Store) {
	s.cardFx = &cardfx.Store{DB: store}
	var targets []cardfx.Target
	if s.cfg.CardEffectsTorque {
		targets = append(targets, cardfx.NewTorque(s.cfg.Upstreams.TorqueURL, s.cfg.Upstreams.TorqueToken))
	}
	ex := cardfx.New(&approvals.Store{DB: store}, s.cardFx, targets...)
	if err := ex.Recover(ctx); err != nil {
		slog.Warn("card effects not recovered", "err", err)
	}
	go ex.Watch(ctx, 0)
}

// startAgentsCollector follows Tether's environment stream into the Agents read model. It stays off
// without a Tether address or when the credential would travel over plain http to a remote host.
func (s *Server) startAgentsCollector(ctx context.Context, store *localstore.Store) {
	if !s.cfg.AgentsCollector || strings.TrimSpace(s.cfg.Upstreams.TetherAddr) == "" {
		return
	}
	src, err := agentscollector.NewHTTPSource(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile)
	if err != nil {
		slog.Warn("agents collector is enabled but not configured; staying off", "err", err)
		return
	}
	cstore := &agentscollector.Store{DB: store}
	// Output payloads are reduced in memory by the derivation sink and never stored.
	live := &agentsderive.Live{Save: func(ctx context.Context, id string, d agentsderive.Saved) error {
		return cstore.SetDerived(ctx, id, agentscollector.Derived{OneLiner: d.OneLiner, CallsInTurn: d.CallsInTurn, At: d.At,
			CtxUsed: d.Meter.Used, CtxWindow: d.Meter.Window, CtxModel: d.Meter.Model, CtxSeen: d.Meter.Seen, Compactions: d.Meter.Compactions, LastClear: d.Meter.LastClear})
	}}
	s.agentsCollector = &agentscollector.Collector{Source: src, Store: cstore, Sink: live, TurnEnded: live.EndTurn}
	s.agentsCollector.Start(ctx)
}

// startItemState opens the shared console item-state store behind /console/state (PR2) and runs
// its snooze wake loop. Key prefixes are an allow-list; Alerts compat (AL1) adds its own.
func (s *Server) startItemState(ctx context.Context, store *localstore.Store) {
	s.itemState = &itemstate.Store{DB: store, Prefixes: []string{"pr-inbox:", "pr-assign:"}}
	go s.itemState.Maintain(ctx, itemstate.WakeInterval, slog.Default().Warn)
}

// startCardEvents publishes card.* events to the hub. It is off unless
// TACHYON_CARD_EVENTS=1 and both the hub URL (HOLLIS_EVENTS_URL) and the token
// file of the tachyon producer credential (HOLLIS_AUTH_TOKEN_FILE_TACHYON) are
// set. Tachyon never mints or stores that credential.
func (s *Server) startCardEvents(ctx context.Context, store *localstore.Store) {
	cfg := s.cfg.CardEvents
	if !cfg.Enabled {
		return
	}
	outbox := &cardevents.Store{DB: store}
	sender, err := cardevents.NewSender(outbox, cfg.URL, cfg.TokenFile)
	if err != nil {
		slog.Warn("card events are enabled but not configured; staying off", "err", err)
		return
	}
	collector := &cardevents.Collector{Items: &approvals.Store{DB: store}, Effects: s.cardFx, Outbox: outbox}
	go cardevents.Run(ctx, collector, sender, 0)
}

// startCardDelivery pushes resolved cards to the sinks the routing profile
// names. It is off unless TACHYON_CARD_DELIVERY_FILE is set; asking agents can
// always pull through card.await and card.get. A file that does not load
// leaves delivery off and says why, rather than half-routing.
func (s *Server) startCardDelivery(ctx context.Context, store *localstore.Store) {
	if s.cfg.CardDeliveryFile == "" {
		return
	}
	var notifier carddelivery.Notifier
	if client, err := chatbridge.NewNotifier(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile); err == nil {
		notifier = client
	}
	router, sinks, err := carddelivery.Load(s.cfg.CardDeliveryFile, notifier)
	if err != nil {
		slog.Warn("card delivery is configured but not usable; staying off", "err", err)
		return
	}
	d := carddelivery.New(&approvals.Store{DB: store}, &carddelivery.Store{DB: store}, router, carddelivery.HoldSink, sinks)
	go d.Watch(ctx, 0)
}

// cardReceiptView is the effects receipt attached to a card item, or nil.
func (s *Server) cardReceiptView(ctx context.Context, itemID string) any {
	if s.cardFx == nil {
		return nil
	}
	r, err := s.cardFx.Get(ctx, itemID)
	if err != nil || r == nil {
		return nil
	}
	return r
}

// actionRunView is the run attached to an approval item, or nil.
func (s *Server) actionRunView(ctx context.Context, itemID string) any {
	run, err := s.actions.Store.ByItem(ctx, itemID)
	if err != nil {
		return nil
	}
	return run
}

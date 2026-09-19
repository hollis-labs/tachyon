// Command tachyon serves the Tachyon Sysop UI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/plugins"
	"github.com/hollis-labs/tachyon/internal/webui"
)

const agentOpsPluginID = "agent-ops"

const (
	resourceTypeAgent           = "agent"
	resourceTypeAgentTool       = "agent-tool"
	resourceTypeSkill           = "skill"
	resourceTypeAgentSkill      = "agent-skill"
	resourceTypeAgentSkillGrant = "agent-skill-grant"
	resourceTypeMCPServer       = "mcp-server"
	resourceTypeAgentMCPServer  = "agent-mcp-server"
	resourceTypeReflex          = "reflex"
)

// unwrapCRUDResult pulls the inner JSON payload out of a crud/create,
// crud/read, or crud/update response envelope.
func unwrapCRUDResult(raw json.RawMessage) (json.RawMessage, error) {
	var res subprocess.CRUDResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Data, nil
}

// unwrapCRUDList pulls the inner JSON array out of a crud/list response
// envelope.
func unwrapCRUDList(raw json.RawMessage) (json.RawMessage, error) {
	var res subprocess.CRUDListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	items := res.Items
	if items == nil {
		items = []json.RawMessage{}
	}
	return json.Marshal(items)
}

// pluginProxy bundles the plumbing every agent-ops HTTP route needs: call
// the plugin over its CRUD wire contract, unwrap the envelope, write JSON,
// and log failures the same way everywhere. A route becomes one call to
// list/read/create/update/delete instead of ~25 lines of hand-rolled
// boilerplate — see cmd/tachyon/main.go's route table below for how each
// resource type (including agent-scoped sub-resources addressed by a
// "<agentID>::<subID>" compound ID) maps onto it.
type pluginProxy struct {
	mgr      *plugins.Manager
	pluginID string
	logger   *slog.Logger
}

func newPluginProxy(mgr *plugins.Manager, pluginID string, logger *slog.Logger) *pluginProxy {
	return &pluginProxy{mgr: mgr, pluginID: pluginID, logger: logger}
}

func (p *pluginProxy) fail(w http.ResponseWriter, op string, err error) {
	p.logger.Error("agent-ops proxy call failed", "op", op, "error", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func decodeBody(r *http.Request) (map[string]interface{}, error) {
	var data map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid request body")
	}
	return data, nil
}

// pathValue returns a filter/id-building function that reads a single
// named path parameter, for the common case (e.g. byID("id")).
func pathValue(param string) func(r *http.Request) string {
	return func(r *http.Request) string { return r.PathValue(param) }
}

// compoundID joins two path parameters into the "<agentID>::<subID>"
// compound ID the plugin's sub-resource resourceTypes expect.
func compoundID(agentParam, subParam string) func(r *http.Request) string {
	return func(r *http.Request) string { return r.PathValue(agentParam) + "::" + r.PathValue(subParam) }
}

// agentIDFilter builds a crud/list filter scoping to the agent named by
// the {id} path parameter.
func agentIDFilter(r *http.Request) map[string]interface{} {
	return map[string]interface{}{"agent_id": r.PathValue("id")}
}

// withAgentID builds a crud/create data-merge that adds the owning
// agent's ID (from the {id} path parameter) to the request body.
func withAgentID(r *http.Request) map[string]interface{} {
	return map[string]interface{}{"agent_id": r.PathValue("id")}
}

// list registers a handler for crud/list. filters is nil when the
// resource type takes no filters (a plain catalog list).
func (p *pluginProxy) list(resourceType string, filters func(r *http.Request) map[string]interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var f map[string]interface{}
		if filters != nil {
			f = filters(r)
		}
		raw, err := p.mgr.CallPlugin(r.Context(), p.pluginID, subprocess.MethodCRUDList, subprocess.CRUDParams{ResourceType: resourceType, Filters: f})
		if err != nil {
			p.fail(w, "list "+resourceType, err)
			return
		}
		result, err := unwrapCRUDList(raw)
		if err != nil {
			p.fail(w, "decode list "+resourceType, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	}
}

// read registers a handler for crud/read. id builds the resource's ID
// from the request — a single path parameter, or a compound ID.
func (p *pluginProxy) read(resourceType string, id func(r *http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := p.mgr.CallPlugin(r.Context(), p.pluginID, subprocess.MethodCRUDRead, subprocess.CRUDParams{ResourceType: resourceType, ID: id(r)})
		if err != nil {
			p.fail(w, "read "+resourceType, err)
			return
		}
		result, err := unwrapCRUDResult(raw)
		if err != nil {
			p.fail(w, "decode "+resourceType, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	}
}

// create registers a handler for crud/create. The JSON request body is
// decoded as the base data map; extra (when non-nil) merges in
// path-derived fields — e.g. an owning agent_id — on top of it.
func (p *pluginProxy) create(resourceType string, extra func(r *http.Request) map[string]interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := decodeBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if extra != nil {
			for k, v := range extra(r) {
				data[k] = v
			}
		}
		raw, err := p.mgr.CallPlugin(r.Context(), p.pluginID, subprocess.MethodCRUDCreate, subprocess.CRUDParams{ResourceType: resourceType, Data: data})
		if err != nil {
			p.fail(w, "create "+resourceType, err)
			return
		}
		result, err := unwrapCRUDResult(raw)
		if err != nil {
			p.fail(w, "decode created "+resourceType, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(result)
	}
}

// update registers a handler for crud/update (used for both PUT and
// PATCH routes).
func (p *pluginProxy) update(resourceType string, id func(r *http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := decodeBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, err := p.mgr.CallPlugin(r.Context(), p.pluginID, subprocess.MethodCRUDUpdate, subprocess.CRUDParams{ResourceType: resourceType, ID: id(r), Data: data})
		if err != nil {
			p.fail(w, "update "+resourceType, err)
			return
		}
		result, err := unwrapCRUDResult(raw)
		if err != nil {
			p.fail(w, "decode updated "+resourceType, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	}
}

// delete registers a handler for crud/delete.
func (p *pluginProxy) delete(resourceType string, id func(r *http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := p.mgr.CallPlugin(r.Context(), p.pluginID, subprocess.MethodCRUDDelete, subprocess.CRUDParams{ResourceType: resourceType, ID: id(r)})
		if err != nil {
			p.fail(w, "delete "+resourceType, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// command registers a handler for command/execute — the wire method for
// plugin verbs that don't fit the CRUD shape (e.g. "capabilities", a
// read-only declaration with no input, or "launch", which needs an
// agent_id built from the URL). buildArgs is nil for a no-input command;
// otherwise it returns the JSON-encoded Args string sent to the plugin.
// The plugin's CommandResult.Content is expected to already be a
// JSON-encoded body, written through as-is.
func (p *pluginProxy) command(commandName string, buildArgs func(r *http.Request) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var args string
		if buildArgs != nil {
			built, err := buildArgs(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			args = built
		}
		raw, err := p.mgr.CallPlugin(r.Context(), p.pluginID, subprocess.MethodCommandExecute, subprocess.CommandExecParams{Name: commandName, Args: args})
		if err != nil {
			p.fail(w, "command "+commandName, err)
			return
		}
		var res subprocess.CommandExecResult
		if err := json.Unmarshal(raw, &res); err != nil {
			p.fail(w, "decode command "+commandName, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(res.Content))
	}
}

// agentIDLaunchArgs builds the JSON Args string for the "launch" command,
// binding the launched session to the agent named in the URL.
func agentIDLaunchArgs(r *http.Request) (string, error) {
	b, err := json.Marshal(map[string]string{"agent_id": r.PathValue("id")})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func main() {
	addr := ":8093"
	if v := os.Getenv("TACHYON_ADDR"); v != "" {
		addr = v
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize plugin manager
	pluginMgr := plugins.NewManager(logger)

	// Load the hello plugin for proof-of-concept
	// In production, this would discover and load plugins from a configured directory
	helloPluginPath := "./plugins/hello/hello"
	if err := pluginMgr.LoadPlugin(ctx, helloPluginPath); err != nil {
		logger.Warn("failed to load hello plugin (build it with: go build -o plugins/hello/hello ./plugins/hello)", "error", err)
	}

	// Load the agent-ops plugin
	agentOpsPluginPath := "./plugins/agent-ops/agent-ops"
	if err := pluginMgr.LoadPlugin(ctx, agentOpsPluginPath); err != nil {
		logger.Warn("failed to load agent-ops plugin (build it with: make build-plugins)", "error", err)
	}

	mux := http.NewServeMux()

	// Same-origin API. The starter dashboard polls /api/health; replace
	// this with your application's real endpoints.
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Agent operations endpoints - proxy to agent-ops plugin's CRUDHandler
	// over plugin-sdk's standard crud/* wire methods.
	agentOps := newPluginProxy(pluginMgr, agentOpsPluginID, logger)

	// Capability declaration — what the active provider (Nanite today)
	// actually supports, for the UI to check before offering an action
	// rather than assuming Nanite's shape is the only shape. See
	// plugins/agent-ops/adapter.go's AgentCapabilities doc comment.
	mux.HandleFunc("GET /api/capabilities", agentOps.command("capabilities", nil))

	// Agents.
	mux.HandleFunc("GET /api/agents", agentOps.list(resourceTypeAgent, nil))
	mux.HandleFunc("GET /api/agents/{id}", agentOps.read(resourceTypeAgent, pathValue("id")))
	mux.HandleFunc("POST /api/agents", agentOps.create(resourceTypeAgent, nil))
	mux.HandleFunc("PUT /api/agents/{id}", agentOps.update(resourceTypeAgent, pathValue("id")))
	mux.HandleFunc("DELETE /api/agents/{id}", agentOps.delete(resourceTypeAgent, pathValue("id")))

	// Launch: creates a session bound to this agent as primary and
	// returns its ID. Deliberately minimal — Tachyon manages the agent,
	// not the session (Tether's authority per the ownership map); this
	// does not open or project a session view, just hands back the ID a
	// launch actually produced instead of leaving the button as a no-op.
	mux.HandleFunc("POST /api/agents/{id}/launch", agentOps.command("launch", agentIDLaunchArgs))

	// Tool grants: GET returns the full discoverable catalog with this
	// agent's grant state folded in; POST grants a tool_id from the body;
	// DELETE revokes one.
	mux.HandleFunc("GET /api/agents/{id}/tools", agentOps.list(resourceTypeAgentTool, agentIDFilter))
	mux.HandleFunc("POST /api/agents/{id}/tools", agentOps.create(resourceTypeAgentTool, withAgentID))
	mux.HandleFunc("DELETE /api/agents/{id}/tools/{toolId}", agentOps.delete(resourceTypeAgentTool, compoundID("id", "toolId")))

	// Skill catalog (read-only from Tachyon — authoring stays on Nanite's
	// own install/sync flow).
	mux.HandleFunc("GET /api/skills", agentOps.list(resourceTypeSkill, nil))

	// Agent skill assignment (discoverable, not yet approved to execute).
	mux.HandleFunc("GET /api/agents/{id}/skills", agentOps.list(resourceTypeAgentSkill, agentIDFilter))
	mux.HandleFunc("POST /api/agents/{id}/skills", agentOps.create(resourceTypeAgentSkill, withAgentID))
	mux.HandleFunc("DELETE /api/agents/{id}/skills/{skillId}", agentOps.delete(resourceTypeAgentSkill, compoundID("id", "skillId")))

	// Agent skill grant (approval to execute against the skill's current
	// content hash).
	mux.HandleFunc("GET /api/agents/{id}/skills/{slug}/grant", agentOps.read(resourceTypeAgentSkillGrant, compoundID("id", "slug")))
	mux.HandleFunc("POST /api/agents/{id}/skills/{slug}/grant", agentOps.create(resourceTypeAgentSkillGrant, func(r *http.Request) map[string]interface{} {
		return map[string]interface{}{"agent_id": r.PathValue("id"), "skill_slug": r.PathValue("slug")}
	}))
	mux.HandleFunc("DELETE /api/agents/{id}/skills/{slug}/grant", agentOps.delete(resourceTypeAgentSkillGrant, compoundID("id", "slug")))

	// MCP server catalog (read-only from Tachyon — registration stays on
	// Nanite's own Settings → MCP flow) and per-agent attach/detach.
	mux.HandleFunc("GET /api/mcp-servers", agentOps.list(resourceTypeMCPServer, nil))
	mux.HandleFunc("POST /api/agents/{id}/mcp-servers", agentOps.create(resourceTypeAgentMCPServer, withAgentID))
	mux.HandleFunc("DELETE /api/agents/{id}/mcp-servers/{serverName}", agentOps.delete(resourceTypeAgentMCPServer, compoundID("id", "serverName")))

	// Reflexes.
	mux.HandleFunc("GET /api/agents/{id}/reflexes", agentOps.list(resourceTypeReflex, agentIDFilter))
	mux.HandleFunc("POST /api/agents/{id}/reflexes", agentOps.create(resourceTypeReflex, withAgentID))
	mux.HandleFunc("PATCH /api/agents/{id}/reflexes/{reflexId}", agentOps.update(resourceTypeReflex, compoundID("id", "reflexId")))
	mux.HandleFunc("DELETE /api/agents/{id}/reflexes/{reflexId}", agentOps.delete(resourceTypeReflex, compoundID("id", "reflexId")))

	// Plugin registry endpoint — serves the registry.Response for the browser loader
	mux.HandleFunc("GET /api/plugins/registry", func(w http.ResponseWriter, r *http.Request) {
		resp := pluginMgr.BuildRegistry()

		payload, err := json.Marshal(resp)
		if err != nil {
			logger.Error("plugin registry serialization failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})

	// The Sysop UI — served from the embedded frontend build by go-webui.
	webui.Mount(mux)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("Tachyon listening on http://localhost%s/sysop/", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-sigCh
	logger.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := pluginMgr.Shutdown(shutdownCtx); err != nil {
		logger.Error("plugin shutdown failed", "error", err)
	}

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "error", err)
	}
}

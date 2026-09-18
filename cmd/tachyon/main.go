// Command tachyon serves the Tachyon Sysop UI.
package main

import (
	"context"
	"encoding/json"
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

const agentResourceType = "agent"

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

func main() {
	const addr = ":8080"

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
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		raw, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", subprocess.MethodCRUDList, subprocess.CRUDParams{ResourceType: agentResourceType})
		if err != nil {
			logger.Error("failed to list agents", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, err := unwrapCRUDList(raw)
		if err != nil {
			logger.Error("failed to decode agent list", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})

	mux.HandleFunc("GET /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		raw, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", subprocess.MethodCRUDRead, subprocess.CRUDParams{ResourceType: agentResourceType, ID: id})
		if err != nil {
			logger.Error("failed to get agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, err := unwrapCRUDResult(raw)
		if err != nil {
			logger.Error("failed to decode agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})

	mux.HandleFunc("POST /api/agents", func(w http.ResponseWriter, r *http.Request) {
		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		raw, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", subprocess.MethodCRUDCreate, subprocess.CRUDParams{ResourceType: agentResourceType, Data: data})
		if err != nil {
			logger.Error("failed to create agent", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, err := unwrapCRUDResult(raw)
		if err != nil {
			logger.Error("failed to decode created agent", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(result)
	})

	mux.HandleFunc("PUT /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		raw, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", subprocess.MethodCRUDUpdate, subprocess.CRUDParams{ResourceType: agentResourceType, ID: id, Data: data})
		if err != nil {
			logger.Error("failed to update agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, err := unwrapCRUDResult(raw)
		if err != nil {
			logger.Error("failed to decode updated agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})

	mux.HandleFunc("DELETE /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", subprocess.MethodCRUDDelete, subprocess.CRUDParams{ResourceType: agentResourceType, ID: id})
		if err != nil {
			logger.Error("failed to delete agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

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

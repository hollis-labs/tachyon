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

	"github.com/hollis-labs/tachyon/internal/plugins"
	"github.com/hollis-labs/tachyon/internal/webui"
)

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

	// Agent operations endpoints - proxy to agent-ops plugin
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		result, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", "agent-ops/list", nil)
		if err != nil {
			logger.Error("failed to list agents", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})

	mux.HandleFunc("GET /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		result, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", "agent-ops/get", map[string]string{"id": id})
		if err != nil {
			logger.Error("failed to get agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})

	mux.HandleFunc("POST /api/agents", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		result, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", "agent-ops/create", req)
		if err != nil {
			logger.Error("failed to create agent", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(result)
	})

	mux.HandleFunc("PUT /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var update map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		params := map[string]interface{}{
			"id":     id,
			"update": update,
		}

		result, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", "agent-ops/update", params)
		if err != nil {
			logger.Error("failed to update agent", "error", err, "id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})

	mux.HandleFunc("DELETE /api/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := pluginMgr.CallPlugin(r.Context(), "agent-ops", "agent-ops/delete", map[string]string{"id": id})
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

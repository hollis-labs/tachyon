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

	mux := http.NewServeMux()

	// Same-origin API. The starter dashboard polls /api/health; replace
	// this with your application's real endpoints.
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

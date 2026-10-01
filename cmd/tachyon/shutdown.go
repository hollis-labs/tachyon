package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

type pluginShutdown interface{ Shutdown(context.Context) error }

// HTTP gets the same short grace as startup rollback, on its own context.
// Closing listeners first prevents new requests racing plugin teardown.
const httpShutdownTimeout = startupRollbackTimeout

func shutdownHost(server *http.Server, mgr pluginShutdown, logger *slog.Logger, httpBudget time.Duration) {
	drain, cancel := context.WithTimeout(context.Background(), httpBudget)
	err := server.Shutdown(drain)
	cancel()
	if err != nil {
		logger.Error("HTTP shutdown failed; closing remaining connections", "stage", "http_drain", "timed_out", errors.Is(err, context.DeadlineExceeded), "error", err)
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("HTTP force close failed", "stage", "http_close", "error", closeErr)
		}
	}
	// Never carry HTTP's deadline into unload. Manager gives every plugin its
	// own short grace, then force-stops and reaps it before moving to the next.
	if err := mgr.Shutdown(context.Background()); err != nil {
		logger.Error("plugin shutdown failed", "stage", "plugin_shutdown", "timed_out", errors.Is(err, context.DeadlineExceeded), "error", err)
	}
}

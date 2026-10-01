package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/plugins"
)

type pluginRestarter interface{ RestartPlugin(string) error }

func newPluginRestartHandler(manager pluginRestarter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		result := struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}{ID: id, Status: "loaded"}
		status := http.StatusOK
		if err := manager.RestartPlugin(id); err != nil {
			result.Status = "unloaded"
			result.Error = err.Error()
			status = http.StatusInternalServerError
			if errors.Is(err, plugins.ErrPluginNotFound) {
				status = http.StatusNotFound
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	})
}

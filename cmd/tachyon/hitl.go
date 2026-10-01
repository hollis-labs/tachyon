package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hollis-labs/tachyon/internal/hitl"
	"github.com/hollis-labs/tachyon/internal/plugins"
)

// This guard is an origin check, not authentication. Local no-Origin clients work.
func hitlSameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, e := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return e == nil && u.Scheme == scheme && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func registerHITL(mux *http.ServeMux, mgr *plugins.Manager, bridge *hitl.Runtime, logger *slog.Logger) {
	mux.HandleFunc("POST /api/verb/{verb}", func(w http.ResponseWriter, r *http.Request) {
		var payload json.RawMessage
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil {
			http.Error(w, "payload exceeds limit", http.StatusRequestEntityTooLarge)
			return
		}
		if len(body) > 0 {
			if !json.Valid(body) {
				http.Error(w, "invalid request body", 400)
				return
			}
			payload = body
		}
		raw, identity, e := mgr.InvokeVerbCaptured(r.Context(), r.PathValue("verb"), payload)
		w.Header().Set("Content-Type", "application/json")
		if e != nil {
			logger.Error("verb invocation failed", "verb", r.PathValue("verb"), "error", e)
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": map[string]string{"code": "dispatch_error", "message": e.Error()}})
			return
		}
		raw = bridge.Attach(r.Context(), r.PathValue("verb"), payload, identity.Generation(), func() bool { return mgr.IdentityCurrent(identity) }, raw)
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("GET /api/hitl/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !hitlSameOrigin(r) {
			w.WriteHeader(403)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "origin_rejected", "continuation": "unavailable"})
			return
		}
		wait := 0
		var e error
		if text := r.URL.Query().Get("wait_ms"); text != "" {
			wait, e = strconv.Atoi(text)
		}
		if e != nil || wait < 0 || wait > hitl.MaxStatusWait {
			http.Error(w, "invalid wait_ms", 400)
			return
		}
		s, e := bridge.Status(r.Context(), r.PathValue("id"), wait)
		if e != nil {
			code := http.StatusBadGateway
			reason := "status_unavailable"
			if errors.Is(e, hitl.ErrStatusBusy) {
				code = http.StatusServiceUnavailable
				reason = "poll_busy"
			}
			if errors.Is(e, hitl.ErrCorrelation) {
				code = 410
				reason = "correlation_unavailable"
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": reason, "approved": false, "continuation": "unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(s)
	})
}

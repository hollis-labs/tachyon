package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/plugins"
)

func TestHTTPNavEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := plugins.NewManager(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/nav", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mgr.MergedNav())
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nav", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var emptyNav contract.NavDeclaration
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyNav); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(emptyNav.Groups) != 0 || len(emptyNav.Items) != 0 {
		t.Fatalf("expected empty nav initially, got %+v", emptyNav)
	}
}

// Ensure context is available in this file.
var _ = context.Background

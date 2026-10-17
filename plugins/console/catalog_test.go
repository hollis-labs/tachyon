package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

func TestCatalogReadsAndRejectsWrites(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != "GET" || r.URL.Path != "/catalog/agents" || r.Header.Get("Tether-Protocol") != "1" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[]}`))
	}))
	defer upstream.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewCatalogHandler(upstream.URL, token)
	res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: catalogPrefix + "/agents"})
	if res.Status != 200 || !called || res.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("read failed: %+v", res)
	}
	called = false
	for _, req := range []subprocess.HTTPRequest{
		{Method: "POST", Path: catalogPrefix + "/agents"},
		{Method: "POST", Path: catalogPrefix + "/plans"},
		{Method: "GET", Path: catalogPrefix + "/plans"},
		{Method: "GET", Path: catalogPrefix + "/agents/../reload"},
	} {
		res = h.Handle(context.Background(), req)
		if res.Status < 400 || called {
			t.Fatalf("forwarded unsupported operation: %+v", req)
		}
	}
}

func TestCatalogConfigurationAndRedirects(t *testing.T) {
	ctx := context.Background()
	req := subprocess.HTTPRequest{Method: "GET", Path: catalogPrefix + "/agents"}
	if got := NewCatalogHandler("", "").Handle(ctx, req); got.Status != 503 {
		t.Fatal(got.Status)
	}
	if got := NewCatalogHandler("http://remote.example", "/token").Handle(ctx, req); got.Status != 503 {
		t.Fatal(got.Status)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/catalog/reload", 302) }))
	defer redirect.Close()
	if got := NewCatalogHandler(redirect.URL, "").Handle(ctx, req); got.Status != 302 {
		t.Fatalf("followed redirect: %d", got.Status)
	}
}

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

func TestTesseractReadsAndRejectsEverythingElse(t *testing.T) {
	var got []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer on %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewTesseractHandler(upstream.URL, token)
	ctx := context.Background()
	for _, p := range []string{"/items/01ABC", "/items/01ABC/history", "/memory/revisions/01ABC", "/recall"} {
		res := h.Handle(ctx, subprocess.HTTPRequest{Method: "GET", Path: tesseractPrefix + p})
		if res.Status != 200 || res.Headers["Cache-Control"] != "no-store" {
			t.Fatalf("%s: %+v", p, res)
		}
	}
	if len(got) != 4 || got[0] != "GET /v1/items/01ABC" {
		t.Fatalf("upstream calls: %v", got)
	}
	got = nil
	for _, req := range []subprocess.HTTPRequest{
		{Method: "POST", Path: tesseractPrefix + "/items/01ABC"},
		{Method: "DELETE", Path: tesseractPrefix + "/items/01ABC"},
		{Method: "GET", Path: tesseractPrefix + "/recall/x"},
		{Method: "GET", Path: tesseractPrefix + "/admin/settings"},
		{Method: "GET", Path: tesseractPrefix + "/items/../admin/settings"},
		{Method: "GET", Path: tesseractPrefix + "/items/a/b/history"},
	} {
		if res := h.Handle(ctx, req); res.Status < 400 || len(got) != 0 {
			t.Fatalf("forwarded %+v", req)
		}
	}
}

func TestTesseractUnconfigured(t *testing.T) {
	res := NewTesseractHandler("", "").Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: tesseractPrefix + "/items/x"})
	if res.Status != 503 {
		t.Fatal(res.Status)
	}
}

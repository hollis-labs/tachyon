package main

import (
	"context"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestChatForwardsOnlyRosterAndSendWithHostCredential(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer host-token" || r.Header.Get("Tether-Protocol") != "1" || r.URL.RawQuery != "" {
			t.Error("wrong credential, protocol, or query")
		}
		if (r.Method != "GET" || r.URL.Path != "/chat/agents") && (r.Method != "POST" || r.URL.Path != "/chat/agents/pm/send") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("host-token"), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewChatHandler(upstream.URL, token)
	for _, req := range []subprocess.HTTPRequest{{Method: "GET", Path: chatPrefix + "/agents", RawQuery: "include_disabled=true"}, {Method: "POST", Path: chatPrefix + "/agents/pm/send"}} {
		if res := h.Handle(context.Background(), req); res.Status != 200 {
			t.Fatalf("forward failed: %d", res.Status)
		}
	}
	for _, req := range []subprocess.HTTPRequest{{Method: "DELETE", Path: chatPrefix + "/agents/pm"}, {Method: "GET", Path: chatPrefix + "/agents/pm/send"}, {Method: "POST", Path: chatPrefix + "/agents/../send"}} {
		if res := h.Handle(context.Background(), req); res.Status != 404 {
			t.Fatal("unsupported operation admitted")
		}
	}
	if calls != 2 {
		t.Fatal("unexpected upstream call")
	}
}

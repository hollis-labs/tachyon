package provideractions

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTetherPinsCredentialAndChecksReceipt(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/teams/form" || r.Header.Get("Tether-Protocol") != "1" || r.Header.Get("Idempotency-Key") != "stable-key" || r.Header.Get("Authorization") != "Bearer first-token" {
			t.Error("request contract changed", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"run":{"id":"run-1","team_id":"delivery","team_version":1,"status":"running"},"secret":"do-not-retain"}`))
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(path, []byte("first-token"), 0600); e != nil {
		t.Fatal(e)
	}
	bind := Tether(upstream.URL, path)
	bound, e := bind()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("second-token"), 0600); e != nil {
		t.Fatal(e)
	}
	rotated, e := bind()
	if e != nil || rotated.Identity() == bound.Identity() {
		t.Fatal("rotation not detected", e)
	}
	result, e := bound.Form(ctx, "stable-key", json.RawMessage(`{"team":{"id":"delivery","version":1}}`))
	if e != nil || calls != 1 || strings.Contains(string(result), "secret") {
		t.Fatal(string(result), e)
	}
}
func TestTetherRefusesRedirectsAndUnconfirmedResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, `{"message":"secret upstream diagnostics"}`},
		{"redirect", 307, `{}`},
		{"wrong-team", 200, `{"run":{"id":"r","team_id":"other","team_version":1}}`},
		{"malformed", 200, `not-json`},
		{"missing-run", 200, `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			bound, e := Tether(server.URL, "")()
			if e != nil {
				t.Fatal(e)
			}
			if _, e = bound.Form(ctx, "key", json.RawMessage(`{"team":{"id":"delivery","version":1}}`)); e == nil || strings.Contains(e.Error(), "secret upstream") {
				t.Fatal("unconfirmed response accepted or diagnostics leaked", e)
			}
		})
	}
}

package dataverbs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/provideractions"
)

func TestProviderActionTransportRequiresAuthorizationAndClosedInputs(t *testing.T) {
	db, e := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	provider := &Actions{Service: &provideractions.Service{Store: db, Bind: func() (provideractions.Bound, error) {
		t.Fatal("denied or malformed command reached provider")
		return nil, nil
	}}}
	authCalls := 0
	allow := false
	handler := NewHTTP([]Provider{provider}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCalls++
			if !allow {
				w.WriteHeader(403)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	invoke := func(method, verb, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, Prefix+"/providers/tether/verbs/"+verb, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	writes := []string{"teams.preview", "teams.apply", "runtime.preview", "actions.apply", "agents.publish_preview", "catalog.apply_preview", "actions.reconcile", "executions.project", "agents.definition_preview", "bindings.preview", "bindings.confirm", "bindings.revoke"}
	for _, verb := range writes {
		if w := invoke("GET", verb, ""); w.Code != 405 {
			t.Fatal(w.Code)
		}
		if w := invoke("POST", verb, `{}`); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if authCalls != len(writes) {
		t.Fatal("writes did not cross authorization")
	}
	for _, verb := range []string{"runtime.runs", "runtime.activity", "bindings.get", "bindings.list"} {
		if w := invoke("POST", verb, `{}`); w.Code != 405 {
			t.Fatal(w.Code)
		}
	}
	if w := invoke("GET", "runtime.runs", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	allow = true
	for _, body := range []string{`{"id":"a","id":"b"}`, `{"id":"a","actor":"operator"}`, `{"id":"a","payload":{"team":{}}}`} {
		if w := invoke("POST", "teams.apply", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := invoke("POST", "teams.roster", `{}`); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := invoke("POST", "executions.project", `{"id":"a","actor":"operator","target":"https://elsewhere"}`); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := invoke("POST", "actions.get", `{}`); w.Code != 405 {
		t.Fatal(w.Code)
	}
}

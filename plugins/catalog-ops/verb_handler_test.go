package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

type seen struct {
	method, path, query, auth, body string
}

func fakeTether(t *testing.T, status int, reply string) (*plugin, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = seen{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), string(b)}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tok, []byte("tth_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := newTetherAPI(srv.URL, tok)
	if err != nil {
		t.Fatal(err)
	}
	return &plugin{api: api}, got
}

func call(t *testing.T, p *plugin, verb, payload string) contract.ResultEnvelope {
	t.Helper()
	env, err := p.HandleVerb(context.Background(), verb, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestEveryDeclaredVerbIsHandled(t *testing.T) {
	p, _ := fakeTether(t, 200, `{}`)
	for verb := range p.Capabilities().Verbs {
		env := call(t, p, verb, `{}`)
		if env.Error != nil && env.Error.Code == "unknown_verb" {
			t.Errorf("%s is declared but not handled", verb)
		}
	}
}

func TestReadVerbsMapToRoutesAndSendBearer(t *testing.T) {
	cases := []struct{ verb, payload, method, path, query string }{
		{"catalog_agents", `{}`, "GET", "/catalog/agents", ""},
		{"catalog_projects", `{}`, "GET", "/catalog/projects", ""},
		{"catalog_launches", `{}`, "GET", "/catalog/launches", ""},
		{"catalog_files", `{"path":"agents/a.yaml"}`, "GET", "/catalog/files", "path=agents%2Fa.yaml"},
		{"catalog_audit", `{"limit":5}`, "GET", "/catalog/audit", "limit=5"},
		{"catalog_plan_get", `{"plan_id":"p1"}`, "GET", "/catalog/plans/p1", ""},
		{"registry_search", `{"kind":"projects","status":"*"}`, "GET", "/registry/projects", "status=%2A"},
		{"registry_get", `{"kind":"agents","urn":"msg://agent/tether/a1"}`, "GET", "/registry/agents/msg:%2F%2Fagent%2Ftether%2Fa1", ""},
	}
	for _, c := range cases {
		p, got := fakeTether(t, 200, `{"ok":true}`)
		env := call(t, p, c.verb, c.payload)
		if env.Status != contract.StatusOK {
			t.Errorf("%s: %+v", c.verb, env.Error)
			continue
		}
		if got.method != c.method || got.path != c.path || got.query != c.query {
			t.Errorf("%s: got %s %s?%s, want %s %s?%s", c.verb, got.method, got.path, got.query, c.method, c.path, c.query)
		}
		if got.auth != "Bearer tth_test" {
			t.Errorf("%s: Authorization = %q", c.verb, got.auth)
		}
	}
}

func TestWriteVerbs(t *testing.T) {
	p, got := fakeTether(t, 200, `{"id":"plan-1"}`)
	env := call(t, p, "catalog_plan", `{"name":"add-agent","task_id":"CW-1","files":[{"path":"agents/x.yaml","content":"id: x\n"}]}`)
	if env.Status != contract.StatusOK || got.method != "POST" || got.path != "/catalog/plans" {
		t.Fatalf("plan: %+v %+v", env, got)
	}
	if !strings.Contains(got.body, `"name":"add-agent"`) || !strings.Contains(got.body, `"agents/x.yaml"`) {
		t.Fatalf("plan body: %s", got.body)
	}
	call(t, p, "catalog_apply", `{"plan_id":"plan-1"}`)
	if got.method != "POST" || got.path != "/catalog/plans/plan-1/apply" {
		t.Fatalf("apply: %+v", got)
	}
	call(t, p, "catalog_reload", `{}`)
	if got.path != "/catalog/reload" {
		t.Fatalf("reload: %+v", got)
	}
	call(t, p, "registry_update", `{"kind":"projects","urn":"msg://project/tether/p1","patch":{"title":"T"}}`)
	if got.method != "PATCH" || !strings.Contains(got.body, `"last_updated_by":"tachyon:catalog-ops"`) {
		t.Fatalf("update: %+v", got)
	}
	call(t, p, "registry_update", `{"kind":"projects","urn":"msg://project/tether/p1","patch":{"title":"T","last_updated_by":"chris"}}`)
	if !strings.Contains(got.body, `"last_updated_by":"chris"`) {
		t.Fatalf("explicit author lost: %s", got.body)
	}
	call(t, p, "registry_deregister", `{"kind":"agents","urn":"msg://agent/tether/a1"}`)
	if got.method != "DELETE" {
		t.Fatalf("deregister: %+v", got)
	}
}

func TestNoApproveVerbExists(t *testing.T) {
	p, _ := fakeTether(t, 200, `{}`)
	for verb := range p.Capabilities().Verbs {
		if strings.Contains(verb, "approve") {
			t.Fatalf("%s: approval is a human act on the Tether socket", verb)
		}
	}
	if env := call(t, p, "catalog_approve", `{}`); env.Error == nil || env.Error.Code != "unknown_verb" {
		t.Fatalf("catalog_approve must not exist: %+v", env)
	}
}

func TestValidationNeverReachesTether(t *testing.T) {
	cases := []struct{ verb, payload string }{
		{"catalog_plan", `{"name":"n","files":[{"path":"../global.yaml","content":"x"}]}`},
		{"catalog_plan", `{"name":"n","files":[{"path":"/etc/x","content":"x"}]}`},
		{"catalog_plan", `{"name":"","files":[{"path":"agents/a.yaml","content":"x"}]}`},
		{"catalog_plan", `{"name":"n","rollback_of":"b","files":[{"path":"agents/a.yaml","content":"x"}]}`},
		{"catalog_files", `{"path":"../../etc/passwd"}`},
		{"catalog_apply", `{"plan_id":"a/b"}`},
		{"catalog_apply", `{}`},
		{"registry_get", `{"kind":"groups","urn":"msg://x"}`},
		{"registry_get", `{"kind":"agents","urn":"http://evil"}`},
		{"registry_register", `{"kind":"agents","profile":{}}`},
		{"registry_update", `{"kind":"agents","urn":"msg://a","patch":{}}`},
	}
	for _, c := range cases {
		p, got := fakeTether(t, 200, `{}`)
		env := call(t, p, c.verb, c.payload)
		if env.Error == nil || env.Error.Code != "validation" {
			t.Errorf("%s %s: %+v", c.verb, c.payload, env)
		}
		if got.method != "" {
			t.Errorf("%s %s reached Tether", c.verb, c.payload)
		}
	}
}

func TestUpstreamErrorMapping(t *testing.T) {
	p, _ := fakeTether(t, 409, `{"error":{"code":"conflict","message":"plan is not approved"}}`)
	env := call(t, p, "catalog_apply", `{"plan_id":"p"}`)
	if env.Error == nil || env.Error.Code != "conflict" || !strings.Contains(env.Error.Message, "plan is not approved") {
		t.Fatalf("%+v", env.Error)
	}
	p, _ = fakeTether(t, 500, `{"error":{"code":"internal","message":"open /home/x/.tether/secret: denied"}}`)
	env = call(t, p, "catalog_agents", `{}`)
	if env.Error == nil || strings.Contains(env.Error.Message, "/home/x") {
		t.Fatalf("500 text must not pass through: %+v", env.Error)
	}
}

func TestPlainHTTPToRemoteHostIsRefusedWithToken(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tok, []byte("tth_x\n"), 0o600)
	if _, err := newTetherAPI("tcp:100.64.0.9:8947", tok); err == nil {
		t.Fatal("expected refusal")
	}
	if _, err := newTetherAPI("https://hub.example.ts.net", tok); err != nil {
		t.Fatal(err)
	}
	if _, err := newTetherAPI("unix:/tmp/tetherd.sock", tok); err != nil {
		t.Fatalf("unix socket with token: %v", err)
	}
}

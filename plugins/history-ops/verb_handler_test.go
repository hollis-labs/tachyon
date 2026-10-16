package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

type seen struct{ method, path, query, auth string }

func fake(t *testing.T, status int, reply string) (*plugin, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tok, []byte("frag-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := newFragmentsAPI(srv.URL, tok)
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

const sid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestOnlyReadVerbsAndAllHandled(t *testing.T) {
	p, _ := fake(t, 200, `{}`)
	for verb, decl := range p.Capabilities().Verbs {
		if decl.Effect != contract.EffectReads {
			t.Errorf("%s has effect %s; history-ops is read-only", verb, decl.Effect)
		}
		if env := call(t, p, verb, `{}`); env.Error != nil && env.Error.Code == "unknown_verb" {
			t.Errorf("%s declared but not handled", verb)
		}
	}
}

func TestRoutesQueryAndBearer(t *testing.T) {
	cases := []struct{ verb, payload, path, query string }{
		{"history_status", `{}`, "/v1/transcripts/status", ""},
		{"history_facets", `{}`, "/v1/transcripts/facets", ""},
		{"history_projects", `{"q":"tach","limit":5}`, "/v1/transcripts/projects", "limit=5&q=tach"},
		{"history_session", `{"id":"` + sid + `","turn_offset":10,"turn_limit":50}`, "/v1/transcripts/sessions/" + sid, "turn_limit=50&turn_offset=10"},
		{"history_sessions", `{}`, "/v1/transcripts/sessions", ""},
		{"history_sessions", `{"q":"auth bug","mode":"hybrid","tool":"claude","kind":"main","since":"2026-10-01","sort":"tokens","order":"desc","has_error":false,"limit":20}`,
			"/v1/transcripts/sessions", "has_error=false&kind=main&limit=20&mode=hybrid&order=desc&q=auth+bug&since=2026-10-01&sort=tokens&tool=claude"},
	}
	for _, c := range cases {
		p, got := fake(t, 200, `{"ok":true}`)
		env := call(t, p, c.verb, c.payload)
		if env.Status != contract.StatusOK {
			t.Errorf("%s: %+v", c.verb, env.Error)
			continue
		}
		if got.method != "GET" || got.path != c.path || got.query != c.query {
			t.Errorf("%s: got %s %s?%s, want GET %s?%s", c.verb, got.method, got.path, got.query, c.path, c.query)
		}
		if got.auth != "Bearer frag-token" {
			t.Errorf("%s: Authorization = %q", c.verb, got.auth)
		}
	}
}

func TestValidationNeverReachesFragments(t *testing.T) {
	cases := []struct{ verb, payload string }{
		{"history_session", `{"id":"../../v1/config"}`},
		{"history_session", `{"id":"` + strings.ToUpper(sid) + `"}`},
		{"history_session", `{"id":"` + sid + `","turn_limit":9999}`},
		{"history_session", `{"id":"` + sid + `","turn_offset":-1}`},
		{"history_sessions", `{"q":"x","mode":"fuzzy"}`},
		{"history_sessions", `{"mode":"hybrid"}`},
		{"history_sessions", `{"sort":"cost"}`},
		{"history_sessions", `{"kind":"other"}`},
		{"history_sessions", `{"limit":5000}`},
		{"history_sessions", `{"since":"yesterday"}`},
		{"history_sessions", `{"q":"` + strings.Repeat("a", 501) + `"}`},
		{"history_projects", `{"limit":-1}`},
	}
	for _, c := range cases {
		p, got := fake(t, 200, `{}`)
		env := call(t, p, c.verb, c.payload)
		if env.Error == nil || env.Error.Code != "validation" {
			t.Errorf("%s %s: %+v", c.verb, c.payload, env)
		}
		if got.method != "" {
			t.Errorf("%s %s reached Fragments", c.verb, c.payload)
		}
	}
}

func TestErrorMappingHidesUpstreamText(t *testing.T) {
	for status, code := range map[int]string{401: "unauthenticated", 403: "forbidden", 404: "not_found", 503: "unavailable", 500: "provider_error"} {
		p, _ := fake(t, status, "secret path /home/x/transcripts.db")
		env := call(t, p, "history_status", `{}`)
		if env.Error == nil || env.Error.Code != code {
			t.Errorf("HTTP %d: %+v", status, env.Error)
		} else if strings.Contains(env.Error.Message, "/home/x") {
			t.Errorf("HTTP %d leaks upstream text: %s", status, env.Error.Message)
		}
	}
}

func TestClientRefusesUnsafeConfig(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tok, []byte("t\n"), 0o600)
	if _, err := newFragmentsAPI("http://fragments.nanite.cloud", tok); err == nil {
		t.Error("plain http to a remote host must be refused with a token")
	}
	if _, err := newFragmentsAPI("https://user:pw@fragments.nanite.cloud", tok); err == nil {
		t.Error("credentials in the URL must be refused")
	}
	if _, err := newFragmentsAPI("https://fragments.nanite.cloud/v1", tok); err == nil {
		t.Error("a path in the base URL must be refused")
	}
	for _, ok := range []string{"https://fragments.nanite.cloud", "http://127.0.0.1:8091", ""} {
		if _, err := newFragmentsAPI(ok, tok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/agentsapi"
)

// The agents routes in the served binary (slices B8a, B8b). Anonymous is 401; a key without
// tachyon.agents.read is refused by scope; a key with it reads Agent Ops, the ceremony meter, the bus
// feed and Needs you (the old guard let reads through), as does the owner. Writes need
// tachyon.agents.control and an operator.
func TestAgentsRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "agents-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	reader, other := key("tachyon.agents.read"), key("tachyon.planner.read")
	p := agentsapi.Prefix
	reads := []string{p + "/sessions", p + "/sessions?include_ended=1", p + "/settings", p + "/seats", p + "/collector", p + "/bus/events",
		"/api/plugins/console/v1/ceremony", "/api/plugins/console/v1/needs-you"}
	anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, path := range append(reads, p+"/sessions/nope") {
		anon.want(401, "GET", path, nil, xrw)
		if code := flatCode(t, keys.want(403, "GET", path, nil, other)); code != "scope_required" {
			t.Errorf("%s without the scope: %s", path, code)
		}
	}
	for _, path := range reads {
		keys.want(200, "GET", path, nil, reader)
		owner.want(200, "GET", path, nil, xrw)
	}
	if code := flatCode(t, owner.want(404, "GET", p+"/sessions/nope", nil, xrw)); code != "not_found" {
		t.Errorf("unknown session: %s", code)
	}
	// Writes (B8b): tachyon.agents.control and an operator. A key holding the scope is refused by
	// the operator check; the owner writes settings; with agents_controls off a nudge is unsupported
	// and a launch is refused before anything is sent.
	controller := key("tachyon.agents.read", "tachyon.agents.control")
	for _, w := range [][2]string{{"PUT", p + "/settings"}, {"POST", p + "/sessions/s1/nudge"}, {"POST", p + "/sessions/s1/restart"},
		{"POST", p + "/controls/r1/skip"}, {"POST", "/api/plugins/console/v1/tether/sessions"}, {"POST", "/api/plugins/console/v1/tether/sessions/s1/launch"}} {
		anon.want(401, w[0], w[1], map[string]any{}, xrw)
		if code := flatCode(t, keys.want(403, w[0], w[1], map[string]any{}, reader)); code != "scope_required" {
			t.Errorf("%s %s with the read scope: %s", w[0], w[1], code)
		}
		if code := flatCode(t, keys.want(403, w[0], w[1], map[string]any{}, controller)); code != "operator_required" {
			t.Errorf("%s %s for a key: %s", w[0], w[1], code)
		}
	}
	settings := owner.want(200, "GET", p+"/settings", nil, xrw)
	owner.want(200, "PUT", p+"/settings", json.RawMessage(settings), xrw)
	for _, path := range []string{"/api/plugins/console/v1/tether/sessions", "/api/plugins/console/v1/tether/sessions/s1/launch"} {
		if code := flatCode(t, owner.want(503, "POST", path, map[string]any{}, xrw)); code != "disabled" {
			t.Errorf("launch with agents_controls off: %s", code)
		}
	}
	// With no Tether configured, the launch dialog's reads answer 503 configuration.
	for _, path := range []string{"/api/plugins/console/v1/tether/workers", "/api/plugins/console/v1/tether/sessions", "/api/plugins/console/v1/catalog/launches"} {
		if code := flatCode(t, keys.want(503, "GET", path, nil, reader)); code != "configuration" {
			t.Errorf("%s without Tether: %s", path, code)
		}
	}
}

// With Tether and Torque configured and the collector switch off, the Agent Ops reads dial nothing:
// the collector is not started and its routes read the store.
func TestAgentsReadsDialNothing(t *testing.T) {
	up := countingListener(t)
	base, _ := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q }\n    torque: { url: %q }\n", up.addr, up.addr))
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	p := agentsapi.Prefix
	for _, path := range []string{p + "/sessions", p + "/settings", p + "/seats", p + "/collector", p + "/bus/events"} {
		c.want(200, "GET", path, nil, xrw)
	}
	if n := up.conns.Load(); n != 0 {
		t.Errorf("%d connections to the upstreams with the collector off", n)
	}
}

// The agents event stream ends when the server shuts down, so Serve returns within its grace period.
func TestAgentsStreamEndsOnShutdown(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	createOwner(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- (&tachyon{app: run.App, ready: func(a net.Addr) { addr <- a }}).serve(ctx) }()
	base := "http://" + (<-addr).String()
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	req, _ := http.NewRequest(http.MethodGet, base+agentsapi.Prefix+"/stream", nil)
	res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	begin := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve ended with %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not return within the grace period with the agents stream open")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("shutdown took %s with the agents stream open", took)
	}
}

// fakeAgentsTether records what Tachyon asks of it and answers the collector, the controls and the
// launch dialog with minimal valid bodies.
type fakeAgentsTether struct {
	mu   sync.Mutex
	reqs []string // "METHOD path?query"
	body map[string]string
}

func newFakeAgentsTether(t *testing.T) (*fakeAgentsTether, string) {
	t.Helper()
	f := &fakeAgentsTether{body: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI())
		f.body[r.Method+" "+r.URL.Path] = string(b)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/environment/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			<-r.Context().Done()
		case r.URL.Path == "/events":
			_, _ = io.WriteString(w, `{"events":[],"next_seq":0}`)
		case r.URL.Path == "/workers":
			_, _ = io.WriteString(w, `{"workers":[{"id":"w1"}]}`)
		case r.URL.Path == "/sessions" && r.Method == http.MethodPost:
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"s9"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeAgentsTether) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

// With agents_collector on, the collector resumes from the migrated cursor: it never takes Tether's
// environment snapshot (a full re-pull), and it asks for events after the copied sequence.
func TestAgentsCollectorResumesFromTheMigratedCursor(t *testing.T) {
	fake, url := newFakeAgentsTether(t)
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q }\n  features: { agents_collector: true }\n", url))
	createOwner(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	// The copied cursor: sequence 500, last event a minute ago.
	if _, err = database.From(run.App).ExecContext(ctx, `INSERT INTO agents_collector_state(id, env_id, last_seq, last_event_at_ns, strategy) VALUES (1, 'env', 500, ?, 'snapshot')`,
		time.Now().Add(-time.Minute).UnixNano()); err != nil {
		t.Fatal(err)
	}
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- (&tachyon{app: run.App, ready: func(a net.Addr) { addr <- a }}).serve(ctx) }()
	<-addr
	deadline := time.Now().Add(5 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		seen = fake.requests()
		if slices.ContainsFunc(seen, func(r string) bool { return strings.HasPrefix(r, "GET /environment/events") }) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if slices.ContainsFunc(seen, func(r string) bool { return strings.HasPrefix(r, "GET /environment/snapshot") }) {
		t.Errorf("the collector re-pulled the environment snapshot: %v", seen)
	}
	if !slices.Contains(seen, "GET /environment/events?after_seq=500") {
		t.Errorf("the collector did not resume after sequence 500: %v", seen)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not return with the collector running")
	}
}

// With agents_controls on, the launch dialog's operations reach Tether as the old proxy sent them:
// the reviewed path and query, only the allowed body fields, and a refusal for any other field.
func TestAgentsLaunchPassthrough(t *testing.T) {
	fake, url := newFakeAgentsTether(t)
	base, _ := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q }\n  features: { agents_controls: true }\n", url))
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	const tp = "/api/plugins/console/v1/tether"
	if body := c.want(200, "GET", tp+"/workers", nil, xrw); !strings.Contains(string(body), `"w1"`) {
		t.Errorf("workers: %s", body)
	}
	c.want(200, "GET", tp+"/sessions?cursor=c1&state=all", nil, xrw)
	c.want(201, "POST", tp+"/sessions", map[string]any{"launch": "pm", "idempotency_key": "k1"}, xrw)
	if code := flatCode(t, c.want(400, "POST", tp+"/sessions", map[string]any{"launch": "pm", "token": "x"}, xrw)); code != "invalid_request" {
		t.Errorf("an unreviewed field: %s", code)
	}
	c.want(200, "POST", tp+"/sessions/s9/launch", map[string]any{}, xrw)
	if code := flatCode(t, c.want(404, "GET", tp+"/sessions/bad.id", nil, xrw)); code != "not_found" {
		t.Errorf("a bad session id: %s", code)
	}
	got := fake.requests()
	for _, want := range []string{"GET /workers", "GET /sessions?cursor=c1&limit=1000&state=running", "POST /sessions", "POST /sessions/s9/launch"} {
		if !slices.Contains(got, want) {
			t.Errorf("Tether did not see %q: %v", want, got)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if b := fake.body["POST /sessions"]; !strings.Contains(b, `"launch":"pm"`) || strings.Contains(b, "token") {
		t.Errorf("launch body: %s", b)
	}
}

// A nudge in the served binary. With agents_controls off the session cannot be nudged (409) and
// nothing reaches Tether; with it on (and the messages credential set) the nudge goes as Tether mail
// to the session's agent.
func TestAgentsNudge(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprint("controls ", on), func(t *testing.T) {
			fake, url := newFakeAgentsTether(t)
			tok := filepath.Join(t.TempDir(), "messages.token")
			if err := os.WriteFile(tok, []byte("test-messages-credential"), 0o600); err != nil {
				t.Fatal(err)
			}
			base, app := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q }\n    tether_messages: { token: %q }\n  features: { agents_controls: %t }\n", url, "file://"+tok, on))
			now := time.Now().UnixNano()
			if _, err := database.From(app).ExecContext(context.Background(), `INSERT INTO agents_sessions(session_id, session_state, work_state, work_known, started_ns, last_activity_ns, updated_ns, agent_urn) VALUES ('s1', 'running', 'processing', 1, ?, ?, ?, 'msg://agent/x/builder')`, now, now, now); err != nil {
				t.Fatal(err)
			}
			c := newClient(t, base)
			c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
			nudge := map[string]any{"request_id": "r1", "text": "please write your handoff"}
			if !on {
				if code := flatCode(t, c.want(409, "POST", agentsapi.Prefix+"/sessions/s1/nudge", nudge, xrw)); code != "nudge_unsupported" {
					t.Errorf("nudge with controls off: %s", code)
				}
				if n := len(fake.requests()); n != 0 {
					t.Errorf("%d requests to Tether with controls off", n)
				}
				return
			}
			c.want(202, "POST", agentsapi.Prefix+"/sessions/s1/nudge", nudge, xrw)
			if !slices.Contains(fake.requests(), "POST /messages") {
				t.Fatalf("no mail sent: %v", fake.requests())
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if b := fake.body["POST /messages"]; !strings.Contains(b, `"to":"msg://agent/x/builder"`) || !strings.Contains(b, "please write your handoff") {
				t.Errorf("mail: %s", b)
			}
		})
	}
}

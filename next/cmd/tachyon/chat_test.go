package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/appagent"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatbridge"
)

// listener counts the connections made to it and answers none of them: a stand-in upstream that
// shows whether Tachyon dials it.
type listener struct {
	addr  string
	conns atomic.Int64
}

func countingListener(t *testing.T) *listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &listener{addr: "http://" + ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			l.conns.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return l
}

// chatOverride points Tether and the app agent at url, with the chat sync loop on or off.
func chatOverride(url string, chatSync bool) string {
	return loopbackOverride + fmt.Sprintf(`tachyon:
  upstreams:
    tether: { url: %q }
    app_agent: { url: %q, session: { host_settings: { id: h } } }
  features: { chat_sync: %t }
`, url, url, chatSync)
}

// The chat routes in the served binary, per route group: no session is 401; a key without the chat
// scope is refused a read, a read-only key a write (scope_required); a key with chat read and write
// is still refused a write and the workspace file content (operator_required: keys never hold
// tachyon.operator); the signed-in owner reads and writes. Threads come from the inbound chat bridge
// (a migrated Tether turn); an unknown thread is 404.
func TestChatRoutesAuth(t *testing.T) {
	up := countingListener(t)
	base, app := started(t, chatOverride(up.addr, false))
	ctx := context.Background()
	turn, _ := json.Marshal(chatbridge.Turn{ID: "turn1", AgentID: "pm", SessionID: "s1", Kind: "terminal", Content: "hello", Created: "2026-11-01T10:00:00Z", Revision: 1, State: "resolved"})
	if _, err := database.From(app).ExecContext(ctx, `INSERT INTO chat_turns(id, source, channel, publication_id, launch_id, session_id, created_at, revision, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"turn1", up.addr, "owner-inbox", "", "pm", "s1", "2026-11-01T10:00:00Z", 1, turn); err != nil {
		t.Fatal(err)
	}
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(ctx, identity.KeyInput{Name: "chat-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	readWrite := key("tachyon.chat.read", "tachyon.chat.write")
	readOnly := key("tachyon.chat.read")
	noChat := key("tachyon.planner.read")
	thread := chatapi.Prefix + "/threads/tether:s1"
	type call struct {
		method, path string
		body         any
	}
	// One read and one write per route group: threads, chat-turns, app agent, topics/bookmarks, plans,
	// workspace/scratchpad/settings, activity/context, work; plus GET continue, which writes.
	reads := []call{
		{"GET", chatapi.Prefix + "/threads", nil}, {"GET", thread, nil}, {"GET", thread + "/messages", nil},
		{"GET", chatbridge.Prefix + "/inbox", nil},
		{"GET", thread + "/topics", nil}, {"GET", thread + "/bookmarks", nil},
		{"GET", thread + "/plans", nil}, {"GET", thread + "/workspace", nil}, {"GET", thread + "/scratchpad", nil},
		{"GET", chatapi.Prefix + "/settings", nil}, {"GET", thread + "/activity", nil}, {"GET", thread + "/context", nil},
		{"GET", thread + "/work", nil},
	}
	writes := []call{
		{"POST", thread + "/read", nil}, {"PATCH", thread, map[string]any{"pinned": true}},
		{"POST", thread + "/messages", map[string]any{"text": "hi", "idempotency_key": "k1"}}, {"POST", thread + "/stop", nil},
		{"POST", chatbridge.Prefix + "/items/turn1/reply", map[string]any{"text": "hi"}},
		{"POST", appagent.Prefix + "/threads/a1/read", nil},
		{"POST", thread + "/topics", map[string]any{"title": "T", "message_ref": "turn1"}},
		{"POST", thread + "/plans", map[string]any{"title": "P"}},
		{"PUT", thread + "/scratchpad", map[string]any{"body": "x", "expected_revision": 0}},
		{"POST", thread + "/activity/comments", map[string]any{"body": "x"}},
		{"GET", thread + "/continue", nil},
		{"GET", thread + "/workspace/nope/content", nil},
	}
	keys := newClient(t, base)
	refused := func(name string, h map[string]string, c call, want string) {
		t.Helper()
		if code := flatCode(t, keys.want(403, c.method, c.path, c.body, h)); code != want {
			t.Errorf("%s %s %s: %s, want %s", name, c.method, c.path, code, want)
		}
	}
	anon := newClient(t, base)
	for _, c := range append(append(reads, writes...), call{"GET", appagent.Prefix + "/threads", nil}) {
		anon.want(401, c.method, c.path, c.body, xrw)
	}
	for _, c := range reads {
		keys.want(200, c.method, c.path, c.body, readOnly)
		refused("no-chat key", noChat, c, "scope_required")
	}
	for _, c := range writes {
		refused("read-only key", readOnly, c, "scope_required")
		refused("read+write key", readWrite, c, "operator_required")
	}

	owner := newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, c := range reads {
		owner.want(200, c.method, c.path, c.body, xrw)
	}
	if body := owner.want(200, "GET", thread+"/messages", nil, xrw); !bytes.Contains(body, []byte(`"hello"`)) {
		t.Errorf("messages: %s", body)
	}
	owner.want(200, "POST", thread+"/read", nil, xrw)
	owner.want(201, "POST", thread+"/topics", map[string]any{"title": "T", "message_ref": "turn1"}, xrw)
	owner.want(201, "POST", thread+"/plans", map[string]any{"title": "P"}, xrw)
	owner.want(201, "POST", thread+"/activity/comments", map[string]any{"body": "x"}, xrw)
	// The topic's message is resolved in its thread, as the old build did: one not in it is refused.
	if code := flatCode(t, owner.want(400, "POST", thread+"/topics", map[string]any{"title": "T", "message_ref": "nope"}, xrw)); code != "invalid" {
		t.Errorf("topic on a message outside the thread: %s", code)
	}
	if code := flatCode(t, owner.want(404, "GET", chatapi.Prefix+"/threads/tether:nope/topics", nil, xrw)); code != "not_found" {
		t.Errorf("unknown thread: %s", code)
	}
	// With chat_sync off, none of the reads above dialed the configured Tether or app agent.
	if n := up.conns.Load(); n != 0 {
		t.Errorf("%d connections to the upstream with chat_sync off", n)
	}
}

// No outbound connection with the switches off: with every upstream address empty (the manifest
// defaults) the binary boots and serves the thread routes; with Tether and the app agent configured
// but tachyon.features.chat_sync off, reading threads, messages and the inbox dials neither. Turning
// chat_sync on dials Tether (the control).
func TestChatNoOutboundWithSwitchesOff(t *testing.T) {
	base, _ := started(t, loopbackOverride)
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	if body := c.want(200, "GET", chatapi.Prefix+"/threads", nil, xrw); !bytes.Contains(body, []byte(`"threads":[]`)) {
		t.Errorf("threads with every upstream off: %s", body)
	}
	if code := flatCode(t, c.want(503, "GET", appagent.Prefix+"/threads", nil, xrw)); code != "not_configured" {
		t.Errorf("app agent off: %s", code)
	}

	off := countingListener(t)
	base, _ = started(t, chatOverride(off.addr, false))
	c = newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, p := range []string{chatapi.Prefix + "/threads", chatapi.Prefix + "/threads/tether:s1/messages", chatbridge.Prefix + "/inbox", appagent.Prefix + "/threads"} {
		c.do("GET", p, nil, xrw)
	}
	time.Sleep(300 * time.Millisecond)
	if n := off.conns.Load(); n != 0 {
		t.Fatalf("chat_sync off: %d connections to the upstream", n)
	}

	on := countingListener(t)
	started(t, chatOverride(on.addr, true))
	deadline := time.Now().Add(10 * time.Second)
	for on.conns.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if on.conns.Load() == 0 {
		t.Fatal("chat_sync on never dialed Tether: the control failed")
	}
}

// Both chat streams (every thread, and one thread's) end when the server shuts down: serve returns
// within the grace period with both open.
func TestChatStreamsEndOnShutdown(t *testing.T) {
	up := countingListener(t)
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, chatOverride(up.addr, false))
	createOwner(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	turn, _ := json.Marshal(chatbridge.Turn{ID: "turn1", AgentID: "pm", SessionID: "s1", Kind: "terminal", Content: "hello", Created: "2026-11-01T10:00:00Z", Revision: 1, State: "resolved"})
	if _, err = database.From(run.App).ExecContext(ctx, `INSERT INTO chat_turns(id, source, channel, publication_id, launch_id, session_id, created_at, revision, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"turn1", up.addr, "owner-inbox", "", "pm", "s1", "2026-11-01T10:00:00Z", 1, turn); err != nil {
		t.Fatal(err)
	}
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- (&tachyon{app: run.App, ready: func(a net.Addr) { addr <- a }}).serve(ctx) }()
	base := "http://" + (<-addr).String()
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, p := range []string{chatapi.Prefix + "/events", chatapi.Prefix + "/threads/tether:s1/events"} {
		req, _ := http.NewRequest(http.MethodGet, base+p, nil)
		res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("%s: %d %s", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	begin := time.Now()
	cancel() // what SIGTERM does (signal.NotifyContext)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve ended with %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not return within the grace period with the chat streams open")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("shutdown took %s with the chat streams open", took)
	}
}

// The chat roster is Tether's, passed through as Tether answers it (the old console plugin's proxy):
// the caller's query is dropped. With Tether not configured it is 503 configuration.
func TestChatRosterIsTethers(t *testing.T) {
	var gotQuery, gotProtocol atomic.Value
	tether := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/agents" {
			http.NotFound(w, r)
			return
		}
		gotQuery.Store(r.URL.RawQuery)
		gotProtocol.Store(r.Header.Get("Tether-Protocol"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"launch_id":"pm","agent_id":"a1","channel":"c","session":{"id":"s1","state":"idle","at_turn_boundary":true}}],"extra":1}`))
	}))
	defer tether.Close()
	base, _ := started(t, chatOverride(tether.URL, false))
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	body := c.want(200, "GET", chatapi.Prefix+"/agents?include_disabled=1", nil, xrw)
	if string(body) != `{"agents":[{"launch_id":"pm","agent_id":"a1","channel":"c","session":{"id":"s1","state":"idle","at_turn_boundary":true}}],"extra":1}` {
		t.Errorf("roster not passed through: %s", body)
	}
	if q, _ := gotQuery.Load().(string); q != "" {
		t.Errorf("the caller's query reached Tether: %q", q)
	}
	if p, _ := gotProtocol.Load().(string); p != "1" {
		t.Errorf("Tether-Protocol %q", p)
	}
	newClient(t, base).want(401, "GET", chatapi.Prefix+"/agents", nil, xrw)

	off, _ := started(t, loopbackOverride)
	c = newClient(t, off)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	if code := flatCode(t, c.want(503, "GET", chatapi.Prefix+"/agents", nil, xrw)); code != "configuration" {
		t.Errorf("roster with Tether off: %s", code)
	}
}

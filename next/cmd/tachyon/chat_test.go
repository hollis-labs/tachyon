package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/access"
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
		{"GET", thread + "/work", nil}, {"GET", chatapi.Prefix + "/outbox?launch_id=pm", nil},
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

// The outbox list shows the messages sent to one launch, owned by the configured Tether address's
// identity (the old build's digest), newest first; another owner's rows and another launch's are not
// listed.
func TestChatOutboxList(t *testing.T) {
	up := countingListener(t)
	base, app := started(t, chatOverride(up.addr, false))
	ctx := context.Background()
	mine, theirs := chatbridge.OutboxIdentity(up.addr), chatbridge.OutboxIdentity("http://elsewhere.test")
	for _, r := range []struct {
		identity, id, launch, body string
		at                         int64
	}{{mine, "k1", "pm", "first", 1}, {mine, "k2", "pm", "second", 2}, {mine, "k3", "other", "x", 3}, {theirs, "k4", "pm", "not mine", 4}} {
		if _, err := database.From(app).ExecContext(ctx, `INSERT INTO chat_outbox(identity, id, launch_id, body, created_at, status, error, session_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.identity, r.id, r.launch, r.body, r.at, "sent", "", "s1"); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	var got []chatbridge.Sent
	if err := json.Unmarshal(c.want(200, "GET", chatapi.Prefix+"/outbox?launch_id=pm", nil, xrw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "k2" || got[1].Key != "k1" || got[0].SessionID != "s1" {
		t.Fatalf("outbox = %+v", got)
	}
	if body := c.want(200, "GET", chatapi.Prefix+"/outbox?launch_id=nobody", nil, xrw); strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty launch: %s", body)
	}
	if n := up.conns.Load(); n != 0 {
		t.Errorf("listing the outbox dialed Tether %d time(s)", n)
	}
}

// The chat data verbs in the served binary (slice B6d): an agent key named after the thread's agent
// writes and lists its plan on a Tether thread through the agent data API; a key without the plan
// verbs does not see them; and nothing dials the configured upstream.
func TestChatVerbsServed(t *testing.T) {
	up := countingListener(t)
	base, app := started(t, chatOverride(up.addr, false))
	ctx := context.Background()
	turn, _ := json.Marshal(chatbridge.Turn{ID: "turn1", AgentID: "pm", SessionID: "s1", Kind: "terminal", Content: "hello", Created: "2026-11-01T10:00:00Z", Revision: 1, State: "resolved"})
	if _, err := database.From(app).ExecContext(ctx, `INSERT INTO chat_turns(id, source, channel, publication_id, launch_id, session_id, created_at, revision, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"turn1", up.addr, "owner-inbox", "", "pm", "s1", "2026-11-01T10:00:00Z", 1, turn); err != nil {
		t.Fatal(err)
	}
	acc := access.New(database.From(app), identity.From(app), nil)
	issue := func(name string, verbs ...string) map[string]string {
		k, _, err := acc.Issue(ctx, access.IssueInput{Name: name, Grants: access.Grants{Verbs: verbs}, TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	pm := issue("pm", "chat.plan.write", "chat.plan.list")
	other := issue("pm", "chat.topic.list")
	c := newClient(t, base)
	const verbs = "/api/agent/v1/data/providers/tachyon/verbs/"
	body := c.want(200, "POST", verbs+"chat.plan.write", map[string]any{"thread_ref": "tether:s1", "title": "Ship", "steps": []map[string]any{{"title": "one"}}}, pm)
	if !bytes.Contains(body, []byte(`"created_by":"agent"`)) {
		t.Errorf("plan write: %s", body)
	}
	in, _ := json.Marshal(map[string]string{"thread_ref": "tether:s1"})
	if body = c.want(200, "GET", verbs+"chat.plan.list?input="+url.QueryEscape(string(in)), nil, pm); !bytes.Contains(body, []byte(`"Ship"`)) {
		t.Errorf("plan list: %s", body)
	}
	c.want(404, "GET", verbs+"chat.plan.list?input="+url.QueryEscape(string(in)), nil, other)
	if body = c.want(200, "GET", "/api/agent/v1/data/providers", nil, other); bytes.Contains(body, []byte("chat.plan")) || !bytes.Contains(body, []byte("chat.topic.list")) {
		t.Errorf("discovery for a topics-only key: %s", body)
	}
	if n := up.conns.Load(); n != 0 {
		t.Errorf("%d connections to the upstream", n)
	}
}

// Sending to an agent in the served binary (slice B6c). With chat_sync off a send is refused and
// nothing dials Tether; with it on the owner's send reaches Tether's send endpoint once and the outbox
// lists it sent; a key, even with the chat write scope, is refused by the operator check.
func TestChatSendServed(t *testing.T) {
	sendPath := chatapi.Prefix + "/agents/pm/send"
	msg := map[string]string{"text": "hello", "idempotency_key": "k1"}
	t.Run("off", func(t *testing.T) {
		up := countingListener(t)
		base, _ := started(t, chatOverride(up.addr, false))
		owner := newClient(t, base)
		owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
		if code := flatCode(t, owner.want(503, "POST", sendPath, msg, xrw)); code != "disabled" {
			t.Errorf("send with chat_sync off: %s", code)
		}
		if n := up.conns.Load(); n != 0 {
			t.Errorf("%d connections to Tether with chat_sync off", n)
		}
	})
	t.Run("on", func(t *testing.T) {
		var sends atomic.Int64
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/chat/agents/pm/send" {
				sends.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"session_id":"s9"}`))
				return
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(fake.Close)
		base, app := started(t, chatOverride(fake.URL, true))
		owner := newClient(t, base)
		owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
		owner.want(200, "POST", sendPath, msg, xrw)
		if body := owner.want(200, "POST", sendPath, msg, xrw); !bytes.Contains(body, []byte(`"replayed":true`)) {
			t.Errorf("repeat: %s", body)
		}
		if body := owner.want(200, "GET", chatapi.Prefix+"/outbox?launch_id=pm", nil, xrw); !bytes.Contains(body, []byte(`"status":"sent"`)) {
			t.Errorf("outbox: %s", body)
		}
		if n := sends.Load(); n != 1 {
			t.Errorf("%d sends reached Tether, want 1", n)
		}
		k, _, err := identity.From(app).CreateKey(context.Background(), identity.KeyInput{Name: "send-test", Scopes: []string{"tachyon.chat.read", "tachyon.chat.write"}})
		if err != nil {
			t.Fatal(err)
		}
		keys := newClient(t, base)
		if code := flatCode(t, keys.want(403, "POST", sendPath, map[string]string{"text": "x", "idempotency_key": "k2"}, map[string]string{"Authorization": "Bearer " + k})); code != "operator_required" {
			t.Errorf("key send: %s", code)
		}
	})
}

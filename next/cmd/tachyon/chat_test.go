package main

import (
	"context"
	"testing"

	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
)

// The chat routes in the served binary, per route group: no session is 401; a key without the chat
// scope is refused a read, a read-only key a write (scope_required); a key with chat read and write
// is still refused a write and the workspace file content (operator_required: keys never hold
// tachyon.operator); the signed-in owner reads and writes. A thread must exist locally: an unknown
// one is 404.
func TestChatRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	ctx := context.Background()
	if _, err := database.From(app).ExecContext(ctx, `INSERT INTO app_agent_threads(id, agent, title, created_ns, updated_ns) VALUES (?, ?, ?, ?, ?)`, "t1", "pm", "Thread", 1, 1); err != nil {
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
	thread := chatapi.Prefix + "/threads/app:t1"
	type call struct {
		method, path string
		body         any
	}
	// One read and one write per route group (topics/bookmarks, plans, workspace/scratchpad/settings,
	// activity/context, work), plus GET continue, which writes.
	reads := []call{
		{"GET", thread + "/topics", nil}, {"GET", thread + "/bookmarks", nil},
		{"GET", thread + "/plans", nil}, {"GET", thread + "/workspace", nil}, {"GET", thread + "/scratchpad", nil},
		{"GET", chatapi.Prefix + "/settings", nil}, {"GET", thread + "/activity", nil}, {"GET", thread + "/context", nil},
		{"GET", thread + "/work", nil},
	}
	writes := []call{
		{"POST", thread + "/topics", map[string]any{"title": "T", "message_ref": "m1"}},
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
	for _, c := range append(reads, writes...) {
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
	owner.want(201, "POST", thread+"/topics", map[string]any{"title": "T", "message_ref": "m1"}, xrw)
	owner.want(201, "POST", thread+"/plans", map[string]any{"title": "P"}, xrw)
	owner.want(201, "POST", thread+"/activity/comments", map[string]any{"body": "x"}, xrw)
	if code := flatCode(t, owner.want(404, "GET", chatapi.Prefix+"/threads/app:nope/topics", nil, xrw)); code != "not_found" {
		t.Errorf("unknown thread: %s", code)
	}
}

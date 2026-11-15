package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// enqueueApproval puts one pending approval of class in the served binary's store.
func enqueueApproval(t *testing.T, inbox *approvals.Inbox, key, class string) string {
	t.Helper()
	var req hitl.EnqueueRequest
	raw := fmt.Sprintf(`{"contract_version":"1.0","kind":"approval","idempotency_key":%q,"source":{"application_id":"test-producer","agent_id":"test-agent"},"class":%q,"title":"Deploy","summary":"Deploy one resource","request":"Deploy resource revision one","op_digest":"sha256:%s"}`, key, class, strings.Repeat("a", 64))
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	h, err := inbox.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return h.ItemID
}

// The approval inbox in the served binary (slice B7). Only a signed-in person uses it: anonymous is
// 401; a key is refused by scope without the approvals scope, and with it is refused as a requester
// (403 requester_cannot_approve), reads and resolves alike, so no key ever resolves. The signed-in
// owner (an operator) lists, reads, presents and resolves.
func TestApprovalsRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	ctx := context.Background()
	inbox := approvals.NewInbox(&approvals.Store{DB: store.Wrap(database.From(app))}, nil)
	low := enqueueApproval(t, inbox, "b7-low", "low")
	medium := enqueueApproval(t, inbox, "b7-medium", "medium")
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(ctx, identity.KeyInput{Name: "approvals-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	both := key("tachyon.approvals.read", "tachyon.approvals.resolve")
	readOnly := key("tachyon.approvals.read")
	none := key("tachyon.planner.read")
	p := approvals.Prefix
	resolve := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "approved"}}
	type call struct {
		method, path string
		body         any
	}
	reads := []call{{"GET", p + "/inbox", nil}, {"GET", p + "/history", nil}, {"GET", p + "/items/" + low, nil}, {"GET", p + "/items/" + low + "/audit", nil}}
	writes := []call{
		{"POST", p + "/items/" + low + "/present", map[string]any{"expected_revision": 1, "presented_projection_revision": 1}},
		{"POST", p + "/items/" + low + "/resolve", resolve},
		{"POST", p + "/items/" + low + "/defer", map[string]any{"minutes": 5}},
		{"DELETE", p + "/items/" + low + "/defer", nil},
		{"POST", p + "/batch/resolve", map[string]any{}},
		{"POST", p + "/secrets", map[string]any{}},
	}
	keys := newClient(t, base)
	refused := func(name string, h map[string]string, c call, want string) {
		t.Helper()
		if code := flatCode(t, keys.want(403, c.method, c.path, c.body, h)); code != want {
			t.Errorf("%s %s %s: %s, want %s", name, c.method, c.path, code, want)
		}
	}
	anon := newClient(t, base)
	for _, c := range append(append(reads, writes...), call{"GET", p + "/events", nil}) {
		anon.want(401, c.method, c.path, c.body, xrw)
	}
	for _, c := range reads {
		refused("no-approvals key", none, c, "scope_required")
		refused("read key", readOnly, c, "requester_cannot_approve")
	}
	for _, c := range writes {
		refused("read-only key", readOnly, c, "scope_required")
		refused("read+resolve key", both, c, "requester_cannot_approve")
	}

	owner := newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	if body := owner.want(200, "GET", p+"/inbox", nil, xrw); !bytes.Contains(body, []byte(low)) || !bytes.Contains(body, []byte(medium)) {
		t.Errorf("inbox: %s", body)
	}
	owner.want(200, "GET", p+"/items/"+medium, nil, xrw)
	owner.want(200, "POST", p+"/items/"+low+"/resolve", resolve, xrw)
	if code := flatCode(t, owner.want(409, "POST", p+"/items/"+low+"/resolve", resolve, xrw)); code == "" {
		t.Error("a second resolve of a resolved item answered no code")
	}
	if code := flatCode(t, owner.want(404, "GET", p+"/items/nope", nil, xrw)); code != "not_found" {
		t.Errorf("unknown item: %s", code)
	}
	// Without tachyon.files.secret_key a secret submission is refused, and never stored.
	if code := flatCode(t, owner.want(503, "POST", p+"/secrets", map[string]any{"item_id": medium, "field": "x", "value": "v", "expected_revision": 1}, xrw)); code != "secrets_unavailable" {
		t.Errorf("secrets without a key: %s", code)
	}
}

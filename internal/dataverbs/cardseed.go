package dataverbs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// SeedShapes are the labelled TEST cards the operator route can create so the Approvals UI can be proven on a
// live queue: a plain approval, a destructive one (confirm phrase), one with alternatives, one that needs a
// reason to deny, three low-risk batchable ones, and a form with one secret field (f). Each targets test:noop, so deciding changes nothing.
var SeedShapes = []string{"a", "b", "c", "d", "e", "f"}

// Seed enqueues the cards of one shape and returns their item ids. tag makes the idempotency keys unique per call.
func (c *Cards) Seed(ctx context.Context, shape, tag string) ([]string, error) {
	if c == nil || c.Service == nil || c.Registry == nil {
		return nil, fmt.Errorf("cards are not enabled on this host")
	}
	type spec struct {
		title string
		extra map[string]any
	}
	noop := []map[string]any{{"target": "test:noop", "op": "update", "summary": "Nothing happens."}}
	del := []map[string]any{{"target": "test:noop", "op": "delete", "summary": "Pretend delete; nothing happens."}}
	items := []map[string]any{{"ref": "test:item/1", "label": "Test item 1", "kind": "test"}, {"ref": "test:item/2", "label": "Test item 2", "kind": "test"}}
	destructive := map[string]any{"irreversible": true, "summary": "Pretend delete of two test items."}
	var specs []spec
	switch shape {
	case "a":
		specs = []spec{{"TEST (a) approve or deny: medium command", map[string]any{"class": "medium", "effects": noop}}}
	case "b":
		specs = []spec{{"TEST (b) destructive: confirm phrase", map[string]any{"class": "medium", "effects": del, "affected_items": items, "destructive": destructive}}}
	case "c":
		specs = []spec{{"TEST (c) destructive with alternatives", map[string]any{"class": "medium", "effects": del, "affected_items": items, "destructive": destructive,
			"alternatives": []map[string]any{{"id": "archive", "label": "Archive instead", "effects": noop}}}}}
	case "d":
		specs = []spec{{"TEST (d) needs a reason to deny", map[string]any{"class": "medium", "effects": noop, "reason_required": true}}}
	case "e":
		for i := 1; i <= 3; i++ {
			specs = append(specs, spec{fmt.Sprintf("TEST (e%d) low-risk batchable", i), map[string]any{"class": "low", "effects": noop}})
		}
	case "f":
		specs = []spec{{"TEST (f) form with a secret field", map[string]any{"class": "low"}}}
	default:
		return nil, fmt.Errorf("shape must be one of a, b, c, d, e, f")
	}
	var ids []string
	for i, sp := range specs {
		expires := time.Now().UTC().Add(24 * time.Hour)
		kind := "card.approval"
		if shape == "f" {
			kind = "card.form"
		}
		req := hitl.EnqueueRequest{
			ContractVersion: hitl.ContractVersion, Kind: kind, IdempotencyKey: fmt.Sprintf("tachyon-test-seed:%s:%s:%d", shape, tag, i),
			Source: hitl.SourceAssertion{ApplicationID: "tachyon-test", ApplicationLabel: "Tachyon test seed", AgentID: "tachyon-test"}, ExpiresAt: &expires,
			Extra: map[string]json.RawMessage{},
		}
		set := func(k string, v any) {
			if b, err := json.Marshal(v); err == nil {
				req.Extra[k] = b
			}
		}
		set("title", sp.title)
		set("summary", "Seeded for live proof of the Approvals UI. Deciding changes nothing.")
		set("data", map[string]any{"seed": shape})
		if shape == "f" {
			set("data", map[string]any{"seed": shape, "fields": []map[string]any{
				{"id": "token", "type": "secret", "label": "Test secret (stored, never shown)", "required": true},
				{"id": "note", "type": "text", "label": "Note"},
			}})
		}
		for k, v := range sp.extra {
			set(k, v)
		}
		if err := c.Registry.Prepare(&req, "tachyon-test"); err != nil {
			return ids, err
		}
		if shape == "e" {
			// Prepare raises any card with effects to medium. The batchable shape is seeded low on purpose (the
			// class is not part of the op digest), so the UI has low-risk items to select.
			req.Extra["class"], _ = json.Marshal("low")
		}
		h, err := c.Service.Enqueue(ctx, req)
		if err != nil {
			return ids, err
		}
		ids = append(ids, h.ItemID)
	}
	return ids, nil
}

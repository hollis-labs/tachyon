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
// reason to deny, and three low-risk batchable ones. Each targets test:noop, so deciding changes nothing.
var SeedShapes = []string{"a", "b", "c", "d", "e"}

// Seed enqueues the cards of one shape and returns their item ids. tag makes the idempotency keys unique per call.
func (c *Cards) Seed(ctx context.Context, shape, tag string) ([]string, error) {
	if c == nil || c.Service == nil || c.Registry == nil {
		return nil, fmt.Errorf("cards are not enabled on this host")
	}
	type spec struct {
		title string
		extra map[string]any
	}
	noop := []map[string]any{{"target": "test:noop", "op": "annotate", "summary": "Nothing happens."}}
	del := []map[string]any{{"target": "test:noop", "op": "delete", "summary": "Pretend delete; nothing happens."}}
	items := []map[string]any{{"ref": "test:item/1", "label": "Test item 1", "kind": "test"}, {"ref": "test:item/2", "label": "Test item 2", "kind": "test"}}
	var specs []spec
	switch shape {
	case "a":
		specs = []spec{{"TEST (a) approve or deny: medium command", map[string]any{"class": "medium", "effects": noop}}}
	case "b":
		specs = []spec{{"TEST (b) destructive: confirm phrase", map[string]any{"class": "high", "effects": del, "affected_items": items, "destructive": true}}}
	case "c":
		specs = []spec{{"TEST (c) destructive with alternatives", map[string]any{"class": "medium", "effects": del, "affected_items": items, "destructive": true,
			"alternatives": []map[string]any{{"id": "archive", "label": "Archive instead", "effects": noop}}}}}
	case "d":
		specs = []spec{{"TEST (d) needs a reason to deny", map[string]any{"class": "medium", "effects": noop, "reason_required": true}}}
	case "e":
		for i := 1; i <= 3; i++ {
			specs = append(specs, spec{fmt.Sprintf("TEST (e%d) low-risk batchable", i), map[string]any{"class": "low", "effects": noop}})
		}
	default:
		return nil, fmt.Errorf("shape must be one of a, b, c, d, e")
	}
	var ids []string
	for i, sp := range specs {
		expires := time.Now().UTC().Add(24 * time.Hour)
		req := hitl.EnqueueRequest{
			ContractVersion: hitl.ContractVersion, Kind: "card.approval", IdempotencyKey: fmt.Sprintf("tachyon-test-seed:%s:%s:%d", shape, tag, i),
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
		for k, v := range sp.extra {
			set(k, v)
		}
		if err := c.Registry.Prepare(&req, "tachyon-test"); err != nil {
			return ids, err
		}
		h, err := c.Service.Enqueue(ctx, req)
		if err != nil {
			return ids, err
		}
		ids = append(ids, h.ItemID)
	}
	return ids, nil
}

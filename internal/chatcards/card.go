package chatcards

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tachyon/internal/approvals"
)

// Card is the chatapi Card schema: the thread-stream projection of a queue item.
type Card struct {
	ItemID                      string           `json:"item_id"`
	Kind                        string           `json:"kind"`
	Caption                     string           `json:"caption"`
	Cite                        []map[string]any `json:"cite"`
	Fields                      []map[string]any `json:"fields"`
	Actions                     []Action         `json:"actions"`
	Target                      map[string]any   `json:"target"`
	State                       string           `json:"state"`
	Class                       string           `json:"class"`
	Reversible                  bool             `json:"reversible"`
	ExpiresAt                   *int64           `json:"expires_at"`
	PresentedProjectionRevision int64            `json:"presented_projection_revision"`
	OpDigest                    string           `json:"op_digest"`
	Result                      map[string]any   `json:"result"`
	MessageRef                  *string          `json:"message_ref"`
}

// Action is one button on a card.
type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Key   string `json:"key,omitempty"`
}

// State maps a queue state to the card state the client shows.
func State(s hitl.State) string {
	switch s {
	case hitl.StateResolved:
		return "resolved"
	case hitl.StateExpired, hitl.StateCanceled, hitl.StateFailed:
		return "expired"
	case hitl.StateSuperseded:
		return "superseded"
	}
	return "pending"
}

func actionsFor(kind string) []Action {
	switch kind {
	case "card.approval":
		return []Action{{"approved", "Allow once", "y"}, {"denied", "Deny", "n"}}
	case "card.tool_approval":
		return []Action{{"approved", "Allow once", "y"}, {"denied", "Deny", "n"}}
	case "card.plan":
		return []Action{{"approved", "Approve plan", "y"}, {"changes_requested", "Request changes", "n"}}
	case "card.dispatch":
		return []Action{{"approved", "Dispatch", "y"}, {"denied", "Not now", "n"}}
	case "card.notice", "card.info":
		return []Action{}
	}
	return []Action{{"submitted", "Answer", "enter"}}
}

// Project builds the card for a record bound to a thread.
func Project(rec hitl.Record, ref Ref, now time.Time) Card {
	p := approvals.Project(rec, now, nil)
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &req)
	c := Card{ItemID: rec.ItemID, Kind: rec.Kind, Caption: p.Title, Cite: []map[string]any{}, Fields: []map[string]any{},
		Actions: actionsFor(rec.Kind), State: State(rec.State), Class: "medium", PresentedProjectionRevision: rec.Revision}
	if p.Risk == "low" {
		c.Class = "low"
	}
	if rec.Kind == "card.tool_approval" && c.Class == "low" {
		c.Actions = []Action{{"approved", "Allow once", "y"}, {"always", "Always in this chat", "a"}, {"denied", "Deny", "n"}}
	}
	if c.Caption == "" {
		c.Caption = rec.ItemID
	}
	var s string
	if json.Unmarshal(req.Extra["op_digest"], &s) == nil {
		c.OpDigest = s
	}
	var effects []map[string]any
	_ = json.Unmarshal(req.Extra["effects"], &effects)
	if len(effects) > 0 {
		t := map[string]any{}
		if target, _ := effects[0]["target"].(string); target != "" {
			scheme, rest, _ := strings.Cut(target, ":")
			t["scheme"], t["ref"] = scheme, rest
		}
		if op, _ := effects[0]["op"].(string); op != "" {
			t["op"] = op
		}
		if len(t) > 0 {
			c.Target = t
		}
	}
	c.Reversible = c.Class == "low" && len(effects) == 0
	if rec.ExpiresAt != nil {
		ms := rec.ExpiresAt.UnixMilli()
		c.ExpiresAt = &ms
	}
	if ref.MessageRef != "" {
		m := ref.MessageRef
		c.MessageRef = &m
	}
	var data struct {
		Fields  []map[string]any `json:"fields"`
		Options []any            `json:"options"`
		Multi   bool             `json:"multi"`
	}
	_ = json.Unmarshal(req.Extra["data"], &data)
	switch {
	case len(data.Fields) > 0:
		c.Fields = data.Fields
	case len(data.Options) > 0:
		c.Fields = []map[string]any{
			{"id": "answer", "type": "enum", "label": "Answer", "options": data.Options, "multi": data.Multi, "required": true},
			{"id": "note", "type": "text", "label": "Note", "required": false},
		}
	}
	for _, f := range c.Fields {
		if opts, ok := f["options"].([]any); ok {
			f["options"] = normalizeOptions(opts)
		}
	}
	if res, ok := rec.Outcome.(hitl.Resolved); ok {
		c.Result = map[string]any{"decision": res.Resolution.Response.Decision, "by": "user", "at": res.Resolution.ResolvedAt.UnixMilli()}
		if res.Resolution.Response.Note != "" {
			c.Result["note"] = res.Resolution.Response.Note
		}
		if raw, ok := res.Resolution.Response.Extra["answers"]; ok {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				c.Result["answers"] = v
			}
		}
	}
	return c
}

// normalizeOptions gives every option the one shape the client maps: {id, value, label, description, recommended}.
// Askers may send plain strings or {value|id, label, description?, recommended?}; the answer value is the id.
func normalizeOptions(in []any) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, o := range in {
		m := map[string]any{"description": "", "recommended": false}
		switch v := o.(type) {
		case string:
			m["id"], m["value"], m["label"] = v, v, v
		case map[string]any:
			id, _ := v["value"].(string)
			if id == "" {
				id, _ = v["id"].(string)
			}
			label, _ := v["label"].(string)
			if label == "" {
				label = id
			}
			m["id"], m["value"], m["label"] = id, id, label
			if d, ok := v["description"].(string); ok {
				m["description"] = d
			}
			if r, ok := v["recommended"].(bool); ok {
				m["recommended"] = r
			}
		default:
			continue
		}
		out = append(out, m)
	}
	return out
}

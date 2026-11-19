package objectsync

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

type TetherWorkstreams struct {
	remote
	scope objects.SourceScope
}

func NewTetherWorkstreams(addr, tokenFile string) (*TetherWorkstreams, error) {
	p, err := NewTetherProjects(addr, tokenFile)
	if err != nil {
		return nil, err
	}
	scope := p.scope
	scope.Collection, scope.Kind = "workstreams", objects.WorkstreamKind
	return &TetherWorkstreams{remote: p.remote, scope: scope}, nil
}
func (*TetherWorkstreams) Name() string                 { return "tether-workstreams" }
func (s *TetherWorkstreams) Scope() objects.SourceScope { return s.scope }

// workstreamEnvelopeKeys are the keys Tether's paginated workstream list may carry ("Follow next_cursor with
// unchanged filters; complete is live exhaustion, not a snapshot"). Anything else is still an unknown contract.
var workstreamEnvelopeKeys = map[string]bool{"workstreams": true, "next_cursor": true, "has_more": true, "complete": true, "limit": true, "ok": true}

// maxWorkstreamPages bounds one scan (1000 per page).
const maxWorkstreamPages = 50

type tetherWorkstream struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func (s *TetherWorkstreams) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	// The list is paginated newest-first. Pages are followed to the end, but the result is still not
	// authoritative: "complete" is live exhaustion, not a snapshot, so absence never implies deletion.
	var list []tetherWorkstream
	cursor, pages := "", 0
	for {
		path := "/workstreams?limit=1000"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var envelope map[string]json.RawMessage
		if _, err := s.get(ctx, path, "", &envelope); err != nil {
			return Snapshot{}, err
		}
		raw, ok := envelope["workstreams"]
		if !ok || string(raw) == "null" {
			return Snapshot{}, scanError("unrecognized Tether workstream envelope")
		}
		for k := range envelope {
			if !workstreamEnvelopeKeys[k] {
				return Snapshot{}, scanError("unrecognized Tether workstream envelope")
			}
		}
		var page []tetherWorkstream
		if err := json.Unmarshal(raw, &page); err != nil {
			return Snapshot{}, scanError("invalid Tether workstream inventory")
		}
		list = append(list, page...)
		pages++
		var next string
		if c, ok := envelope["next_cursor"]; ok && string(c) != "null" {
			if err := json.Unmarshal(c, &next); err != nil {
				return Snapshot{}, scanError("invalid Tether workstream cursor")
			}
		}
		if next == "" || next == cursor || len(page) == 0 {
			break
		}
		if pages >= maxWorkstreamPages {
			return Snapshot{}, scanError("Tether workstream list exceeds the page limit")
		}
		cursor = next
	}
	c := collect(s.scope, at)
	for _, w := range list {
		if w.Status != "active" && w.Status != "closed" {
			return Snapshot{}, scanError("unknown Tether workstream status")
		}
		name := w.Name
		if strings.TrimSpace(name) == "" {
			name = w.ID
		}
		d := objects.Workstream{Common: objects.Common{Name: name, Properties: map[string]any{"tether": map[string]any{
			"workflow_ref": w.WorkflowID, "created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
		}}}, Status: w.Status}
		if err := c.add(w.ID, w.UpdatedAt, false, d); err != nil {
			return Snapshot{}, err
		}
	}
	if err := progress(Progress{Pages: pages, Items: len(list), Checkpoint: "paged_inventory_observed"}); err != nil {
		return Snapshot{}, err
	}
	return c.snapshot, nil
}

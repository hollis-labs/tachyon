package objectsync

import (
	"context"
	"encoding/json"
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

func (s *TetherWorkstreams) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	// This endpoint currently returns only the newest 100 records, with no
	// pagination. Never infer deletion from absence, even for an empty response.
	var envelope map[string]json.RawMessage
	if _, err := s.get(ctx, "/workstreams", "", &envelope); err != nil {
		return Snapshot{}, err
	}
	raw, ok := envelope["workstreams"]
	if !ok || len(envelope) != 1 || string(raw) == "null" {
		return Snapshot{}, scanError("unrecognized Tether workstream envelope")
	}
	var list []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		WorkflowID string `json:"workflow_id"`
		Status     string `json:"status"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return Snapshot{}, scanError("invalid Tether workstream inventory")
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
	if err := progress(Progress{Pages: 1, Items: len(list), Checkpoint: "bounded_inventory_observed"}); err != nil {
		return Snapshot{}, err
	}
	return c.snapshot, nil
}

package dataverbs

import (
	"context"

	"github.com/hollis-labs/tachyon/internal/planner"
)

type portfolioList struct {
	DB      string            `json:"db"`
	Filters map[string]string `json:"filters,omitempty"`
	Sort    string            `json:"sort,omitempty"`
}
type portfolioID struct {
	DB string `json:"db"`
	ID string `json:"id"`
}
type portfolioCreate struct {
	DB     string       `json:"db"`
	ID     string       `json:"id"`
	Fields planner.Item `json:"fields"`
}
type portfolioPatch struct {
	DB               string       `json:"db"`
	ID               string       `json:"id"`
	ExpectedRevision int64        `json:"expected_revision"`
	Patch            planner.Item `json:"patch"`
}
type portfolioAction struct {
	DB               string       `json:"db"`
	ID               string       `json:"id"`
	ExpectedRevision int64        `json:"expected_revision"`
	Action           string       `json:"action"`
	Arguments        planner.Item `json:"arguments,omitempty"`
}
type portfolioOrder struct {
	DB    string                `json:"db"`
	Items []planner.OrderChange `json:"items"`
}
type portfolioPromote struct {
	ID               string       `json:"id"`
	ExpectedRevision int64        `json:"expected_revision"`
	ToDB             string       `json:"to_db"`
	TargetID         string       `json:"target_id"`
	Fields           planner.Item `json:"fields,omitempty"`
}

func registerPortfolio(p *Local) {
	s := &planner.Owned{Objects: p.service}
	register(p, "planner.portfolio.resolve", false, "Resolve a local UUID or unambiguous imported ID to its portfolio database and item.", []string{"id"}, func(ctx context.Context, q IDRequest, _ string) (any, error) { return s.ResolveReference(ctx, q.ID) })
	register(p, "planner.portfolio.relate", true, "Add/remove a validated local portfolio reference with a source revision check.", []string{"from", "to", "expected_revision", "add"}, func(ctx context.Context, q planner.RelationRequest, actor string) (any, error) {
		return s.Relate(ctx, q, actor)
	})
	register(p, "planner.portfolio.list", false, "List the owned portfolio database, including imported details and local revisions.", []string{"db"}, func(ctx context.Context, q portfolioList, _ string) (any, error) {
		return s.List(ctx, q.DB, q.Filters, q.Sort)
	})
	register(p, "planner.portfolio.get", false, "Read an owned portfolio item by UUID or unambiguous legacy ID.", []string{"db", "id"}, func(ctx context.Context, q portfolioID, _ string) (any, error) { return s.Get(ctx, q.DB, q.ID) })
	register(p, "planner.portfolio.schema", false, "Read the locally owned field/status schema.", []string{"db"}, func(_ context.Context, q struct {
		DB string `json:"db"`
	}, _ string) (any, error) {
		return planner.Schema(q.DB)
	})
	register(p, "planner.portfolio.create", true, "Create an owned portfolio item using a caller-retained UUID.", []string{"db", "id", "fields"}, func(ctx context.Context, q portfolioCreate, actor string) (any, error) {
		return s.Create(ctx, q.DB, q.ID, q.Fields, actor)
	})
	register(p, "planner.portfolio.patch", true, "Patch authored portfolio fields with revision checking; imported provenance is preserved.", []string{"db", "id", "expected_revision", "patch"}, func(ctx context.Context, q portfolioPatch, actor string) (any, error) {
		return s.Patch(ctx, q.DB, q.ID, q.ExpectedRevision, q.Patch, actor)
	})
	register(p, "planner.portfolio.action", true, "Append comments, add/remove links, decide/defer/reopen decisions or dismiss inbox items with revision checking.", []string{"db", "id", "expected_revision", "action"}, func(ctx context.Context, q portfolioAction, actor string) (any, error) {
		return s.Action(ctx, q.DB, q.ID, q.ExpectedRevision, q.Action, q.Arguments, actor)
	})
	register(p, "planner.portfolio.reorder", true, "Atomically reorder selected records using their expected revisions.", []string{"db", "items"}, func(ctx context.Context, q portfolioOrder, actor string) (any, error) {
		return s.Reorder(ctx, q.DB, q.Items, actor)
	})
	register(p, "planner.portfolio.promote", true, "Atomically promote and triage an inbox item into any other portfolio database.", []string{"id", "expected_revision", "to_db", "target_id"}, func(ctx context.Context, q portfolioPromote, actor string) (any, error) {
		return s.Promote(ctx, q.ID, q.ExpectedRevision, q.ToDB, q.TargetID, q.Fields, actor)
	})
}

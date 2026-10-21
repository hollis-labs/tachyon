package dataverbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

type operation struct {
	name, description string
	mutates           bool
	input             reflect.Type
	required          []string
	call              func(context.Context, json.RawMessage, string) (any, error)
}
type Local struct {
	service    *objects.Service
	operations map[string]operation
}

func (*Local) ID() string { return "tachyon" }

func NewLocal(s *objects.Service) *Local {
	p := &Local{service: s, operations: map[string]operation{}}
	registerPortfolio(p)
	register(p, "objects.list", false, "List local records with UUID keyset pagination.", nil, func(ctx context.Context, q objects.Filter, _ string) (any, error) {
		rows, err := s.List(ctx, q)
		return recordPage(rows, q.Limit), err
	})
	register(p, "objects.get", false, "Read a local record, including a tombstone.", []string{"id"}, func(ctx context.Context, q IDRequest, _ string) (any, error) { return s.Get(ctx, q.ID) })
	register(p, "objects.lookup_source", false, "Resolve an exact provider-qualified source identity.", []string{"provider", "instance", "collection", "kind", "external_id"}, func(ctx context.Context, q objects.SourceKey, _ string) (any, error) { return s.LookupSource(ctx, q) })
	register(p, "objects.create", true, "Create with a caller-retained UUID; reuse with different initial content conflicts.", []string{"id", "kind", "definition"}, func(ctx context.Context, q CreateRequest, actor string) (any, error) {
		return s.CreateWithID(ctx, q.ID, jsonDefinition{kind: q.Kind, raw: q.Definition}, q.Metadata, actor)
	})
	register(p, "objects.patch", true, "Merge authored fields/metadata using the expected revision; null releases local fields.", []string{"id", "expected_revision"}, func(ctx context.Context, q PatchRequest, actor string) (any, error) {
		if err := revision(q.RevisionRequest); err != nil {
			return nil, err
		}
		return s.Patch(ctx, q.ID, q.ExpectedRevision, objects.Patch{Definition: q.Definition, Metadata: q.Metadata}, actor)
	})
	for _, action := range []struct {
		name    string
		deleted bool
	}{{"delete", true}, {"restore", false}} {
		register(p, "objects."+action.name, true, "Set the local tombstone with a revision check; never deletes history or writes upstream.", []string{"id", "expected_revision"}, func(ctx context.Context, q RevisionRequest, actor string) (any, error) {
			if err := revision(q); err != nil {
				return nil, err
			}
			return s.SetDeleted(ctx, q.ID, q.ExpectedRevision, action.deleted, actor)
		})
	}
	register(p, "objects.take_ownership", true, "Explicitly adopt effective source data as a local master; retain provider provenance.", []string{"id", "expected_revision"}, func(ctx context.Context, q RevisionRequest, actor string) (any, error) {
		if err := revision(q); err != nil {
			return nil, err
		}
		return s.TakeOwnership(ctx, q.ID, q.ExpectedRevision, actor)
	})
	register(p, "objects.history", false, "Read immutable revisions in ascending order.", []string{"id"}, func(ctx context.Context, q HistoryRequest, _ string) (any, error) {
		if q.AfterRevision < 0 {
			return nil, invalid("after_revision must be nonnegative.")
		}
		if _, err := s.Get(ctx, q.ID); err != nil {
			return nil, err
		}
		rows, err := s.History(ctx, q.ID, q.AfterRevision, q.Limit)
		page := HistoryPage{Items: rows}
		if len(rows) > 0 && len(rows) == effectiveLimit(q.Limit) {
			page.NextAfterRevision = rows[len(rows)-1].Record.Revision
		}
		return page, err
	})
	register(p, "objects.links", false, "Read current typed relationships, including retained tombstone and promotion links.", []string{"id"}, func(ctx context.Context, q LinksRequest, _ string) (any, error) {
		if _, err := s.Get(ctx, q.ID); err != nil {
			return nil, err
		}
		rows, err := s.Links(ctx, q.ID, q.Incoming)
		return LinksResult{Items: rows}, err
	})
	register(p, "planner.list", false, "Query all twelve Planner kinds with intersecting filters and UUID pagination.", nil, func(ctx context.Context, q objects.PlannerFilter, _ string) (any, error) {
		rows, err := s.ListPlanner(ctx, q)
		return recordPage(rows, q.Limit), err
	})
	register(p, "executions.list", false, "Query retained session/run records; never starts or stops execution.", nil, func(ctx context.Context, q objects.ExecutionFilter, _ string) (any, error) {
		rows, err := s.ListExecutions(ctx, q)
		return recordPage(rows, q.Limit), err
	})
	register(p, "collections.members.list", false, "Read ordered members; pin the returned collection revision for subsequent pages.", []string{"id"}, func(ctx context.Context, q MembersRequest, _ string) (any, error) {
		return s.ListCollectionMembers(ctx, q.ID, q.ExpectedRevision, q.Offset, q.Limit)
	})
	register(p, "collections.members.set", true, "Replace ordered membership with a revision check; explicit null/empty clears it.", []string{"id", "expected_revision", "members"}, func(ctx context.Context, q SetMembersRequest, actor string) (any, error) {
		if err := revision(q.RevisionRequest); err != nil {
			return nil, err
		}
		return s.SetCollectionMembers(ctx, q.ID, q.ExpectedRevision, q.Members, actor)
	})
	register(p, "planner.promote", true, "Atomically promote an owned inbox item/idea using a retained target UUID and source revision.", []string{"source_id", "expected_revision", "target_id", "target_kind"}, func(ctx context.Context, q objects.PromotionRequest, actor string) (any, error) {
		return s.Promote(ctx, q, actor)
	})
	register(p, "planner.promotion.get", false, "Read the immutable promotion receipt by source ID.", []string{"id"}, func(ctx context.Context, q IDRequest, _ string) (any, error) { return s.GetPromotion(ctx, q.ID) })
	return p
}
func register[Q any](p *Local, name string, mutates bool, description string, required []string, fn func(context.Context, Q, string) (any, error)) {
	op := operation{name: name, mutates: mutates, description: description, input: reflect.TypeFor[Q](), required: required}
	op.call = func(ctx context.Context, raw json.RawMessage, actor string) (any, error) {
		var q Q
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			raw = json.RawMessage(`{}`)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, invalid("A JSON object is required.")
		}
		properties := shape(op.input)["properties"].(map[string]any)
		for key, value := range fields {
			schema, known := properties[key]
			if !known {
				return nil, invalid("Unknown command field: " + key)
			}
			if _, nonnullable := schema.(map[string]any)["type"]; nonnullable && bytes.Equal(value, []byte("null")) {
				return nil, invalid("Field cannot be null: " + key)
			}
		}
		for _, key := range required {
			value, ok := fields[key]
			if !ok || key != "members" && bytes.Equal(value, []byte("null")) {
				return nil, invalid("Required field: " + key)
			}
			if len(value) > 0 && value[0] == '"' {
				var text string
				_ = json.Unmarshal(value, &text)
				if strings.TrimSpace(text) == "" {
					return nil, invalid("Nonblank field required: " + key)
				}
			}
		}
		return fn(ctx, q, actor)
	}
	p.operations[name] = op
}
func (p *Local) Verbs() []Verb {
	out := make([]Verb, 0, len(p.operations))
	for _, op := range p.operations {
		out = append(out, Verb{Name: op.name, Mutates: op.mutates, Description: op.description, InputSchema: inputSchema(op.input, append([]string(nil), op.required...))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (p *Local) Invoke(ctx context.Context, name string, input json.RawMessage, actor string) (json.RawMessage, error) {
	op, ok := p.operations[name]
	if !ok {
		return nil, svcerr.New(svcerr.CodeNotFound, "Unknown data verb.")
	}
	if op.mutates && strings.TrimSpace(actor) == "" {
		return nil, svcerr.New(svcerr.CodePermission, "A verified mutation actor is required.")
	}
	if p.service == nil {
		return nil, svcerr.New("unavailable", "Local data service is unavailable.", svcerr.WithStatus(503))
	}
	result, err := op.call(ctx, input, actor)
	if err != nil {
		return nil, serviceError(err)
	}
	raw, err := json.Marshal(result)
	return raw, serviceError(err)
}
func revision(q RevisionRequest) error {
	if q.ExpectedRevision < 1 {
		return invalid("expected_revision must be positive.")
	}
	return nil
}
func effectiveLimit(limit int) int {
	if limit == 0 {
		return 100
	}
	return limit
}
func recordPage(rows []objects.Record, limit int) RecordsPage {
	page := RecordsPage{Items: rows}
	if len(rows) > 0 && len(rows) == effectiveLimit(limit) {
		page.NextAfterID = rows[len(rows)-1].ID
	}
	return page
}

// Keep dynamic definitions as raw JSON through the typed domain validation so
// arbitrary metadata/property numbers do not round through float64.
type jsonDefinition struct {
	kind objects.Kind
	raw  json.RawMessage
}

func (d jsonDefinition) ObjectKind() objects.Kind     { return d.kind }
func (d jsonDefinition) MarshalJSON() ([]byte, error) { return d.raw, nil }
func serviceError(err error) error {
	if err == nil {
		return nil
	}
	var known *svcerr.Error
	if errors.As(err, &known) {
		return err
	}
	switch {
	case errors.Is(err, planner.ErrOperatorRequired):
		return svcerr.Wrap(err, svcerr.CodePermission, "Approving, un-approving and sequencing themes requires operator authorization.")
	case errors.Is(err, objects.ErrInvalid):
		return svcerr.Wrap(err, svcerr.CodeInvalid, "Invalid object definition, reference or query.")
	case errors.Is(err, objects.ErrNotFound):
		return svcerr.Wrap(err, svcerr.CodeNotFound, "Local record or receipt not found.")
	case errors.Is(err, objects.ErrConflict):
		return svcerr.Wrap(err, svcerr.CodeConflict, "Revision or retained request conflicts; reload the record before editing.")
	case errors.Is(err, context.Canceled):
		return svcerr.Wrap(err, "cancelled", "Call cancelled; read back retained IDs before retrying a write.", svcerr.WithStatus(408))
	case errors.Is(err, context.DeadlineExceeded):
		return svcerr.Wrap(err, "timeout", "Call timed out; read back retained IDs before retrying a write.", svcerr.WithStatus(504))
	default:
		return svcerr.Wrap(err, svcerr.CodeInternal, "Local data operation failed.")
	}
}

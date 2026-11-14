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
	"github.com/hollis-labs/tachyon/next/internal/objects"
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
	register(p, "planner.list", false, "Query all twelve Planner kinds with intersecting filters and UUID pagination.", nil, func(ctx context.Context, q objects.PlannerFilter, _ string) (any, error) {
		rows, err := s.ListPlanner(ctx, q)
		return recordPage(rows, q.Limit), err
	})
	register(p, "objects.list", false, "List local records with UUID keyset pagination.", nil, func(ctx context.Context, q objects.Filter, _ string) (any, error) {
		rows, err := s.List(ctx, q)
		return recordPage(rows, q.Limit), err
	})
	register(p, "objects.get", false, "Read a local record, including a tombstone.", []string{"id"}, func(ctx context.Context, q IDRequest, _ string) (any, error) { return s.Get(ctx, q.ID) })
	register(p, "objects.lookup_source", false, "Resolve an exact provider-qualified source identity.", []string{"provider", "instance", "collection", "kind", "external_id"}, func(ctx context.Context, q objects.SourceKey, _ string) (any, error) { return s.LookupSource(ctx, q) })
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

func serviceError(err error) error {
	if err == nil {
		return nil
	}
	var known *svcerr.Error
	if errors.As(err, &known) {
		return err
	}
	switch {
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

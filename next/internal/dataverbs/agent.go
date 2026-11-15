package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/hollis-labs/libs/core/util/svcerr"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/planner"
)

// AgentPrefix is the agent data API: API keys mapped to a service identity only (access.RequireAgent).
const AgentPrefix = "/api/agent/v1/data"

// NewAgentHTTP serves the data verbs to agents. Every request is resolved to its agent first; the
// verbs it lists and accepts are the ones its credential allows (the key's tachyon.<verb> scope and
// the mapping's verbs), and every call that touches an object checks the object's kind against the
// credential's kinds. The mutation actor is "service:<identity id>", never a request field.
func NewAgentHTTP(local *Local, acc *access.Service) http.Handler {
	return access.RequireAgent(acc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, _ := access.FromContext(r.Context())
		p := &scoped{local: local, agent: agent}
		newHTTP(AgentPrefix, []Provider{p}, func(next http.Handler) http.Handler { return next }).ServeHTTP(w, r)
	}))
}

// scoped is the local provider narrowed to one agent's grant.
type scoped struct {
	local *Local
	agent access.Agent
}

func (*scoped) ID() string { return "tachyon" }

// Verbs are the wired verbs the agent may call.
func (p *scoped) Verbs() []Verb {
	var out []Verb
	for _, v := range p.local.Verbs() {
		if slices.Contains(access.Verbs(), v.Name) && p.agent.Allows(v.Name, "") {
			out = append(out, v)
		}
	}
	return out
}

func denied() error {
	return svcerr.New(svcerr.CodePermission, "This credential does not grant access to that operation or object kind.")
}

// Invoke checks the verb and, for the object verbs, the object kind, exactly as the old build did.
func (p *scoped) Invoke(ctx context.Context, name string, raw json.RawMessage, _ string) (json.RawMessage, error) {
	if !slices.Contains(access.Verbs(), name) || !p.agent.Allows(name, "") {
		return nil, denied()
	}
	allows := func(k objects.Kind) bool { return k != "" && p.agent.Allows(name, string(k)) }
	switch name {
	case "objects.list":
		var q objects.Filter
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !allows(q.Kind) {
			return nil, denied()
		}
	case "objects.lookup_source":
		var q objects.SourceKey
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !allows(q.Kind) {
			return nil, denied()
		}
	case "objects.get", "objects.history":
		var id string
		if name == "objects.get" {
			var q IDRequest
			if err := decode(raw, &q); err != nil {
				return nil, err
			}
			id = q.ID
		} else {
			var q HistoryRequest
			if err := decode(raw, &q); err != nil {
				return nil, err
			}
			id = q.ID
		}
		if p.local.service == nil {
			return nil, svcerr.New("unavailable", "Local data service is unavailable.", svcerr.WithStatus(503))
		}
		record, err := p.local.service.Get(ctx, id)
		if err != nil {
			return nil, serviceError(err)
		}
		if !allows(record.Kind) {
			return nil, denied()
		}
	case "planner.list":
		var q objects.PlannerFilter
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if q.Kind != "" {
			if !allows(q.Kind) {
				return nil, denied()
			}
		} else {
			// Requiring the complete kind set avoids post-filter pagination leaks and silently
			// dropping permitted records. Narrow credentials name a kind.
			for _, k := range access.Kinds() {
				if !allows(objects.Kind(k)) {
					return nil, denied()
				}
			}
		}
	case "planner.portfolio.resolve":
		var q IDRequest
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		return p.resolve(ctx, q.ID)
	case "planner.portfolio.list", "planner.portfolio.get", "planner.portfolio.schema", "planner.portfolio.create",
		"planner.portfolio.patch", "planner.portfolio.reorder", "planner.portfolio.assign", "planner.portfolio.unassign":
		var q struct {
			DB string `json:"db"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, invalid("A JSON object is required.")
		}
		if !p.database(name, q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.action":
		var q portfolioAction
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(name, q.DB) {
			return nil, denied()
		}
		switch q.Action {
		case "comments", "link-add", "link-remove", "decide", "defer", "reopen", "dismiss", "ready", "approve", "park", "unpark", "finish", "resequence", "append_state", "verify":
		default:
			return nil, denied()
		}
	case "planner.portfolio.relate":
		var q planner.RelationRequest
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(name, q.From.DB) || !p.database(name, q.To.DB) {
			return nil, denied()
		}
	default:
		// A verb the agent holds but this build does not serve yet.
		return nil, svcerr.New(svcerr.CodeNotFound, "Unknown data verb.")
	}
	// Local kinds are immutable. Authorized records may contain reference IDs, but this surface never
	// expands those references into other records.
	return p.local.Invoke(ctx, name, raw, "service:"+p.agent.Identity.ID)
}

// database reports a portfolio database whose kind the credential may use with verb.
func (p *scoped) database(verb, db string) bool {
	kind, ok := planner.DatabaseKind(db)
	return ok && p.agent.Allows(verb, string(kind))
}

// resolve searches only the databases the credential may read: a legacy ID must be unambiguous
// within the caller's view, without disclosing records of other kinds.
func (p *scoped) resolve(ctx context.Context, id string) (json.RawMessage, error) {
	var found planner.Item
	s := &planner.Owned{Objects: p.local.service}
	for _, db := range []string{"priorities", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"} {
		if !p.database("planner.portfolio.resolve", db) {
			continue
		}
		row, err := s.Get(ctx, db, id)
		if errors.Is(err, objects.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, serviceError(err)
		}
		if found != nil {
			return nil, serviceError(objects.ErrConflict)
		}
		found = planner.Item{"db": db, "id": row["id"], "item": row}
	}
	if found == nil {
		return nil, serviceError(objects.ErrNotFound)
	}
	return json.Marshal(found)
}

// ConsoleScope is the route scope a signed-in person needs for a console data verb: reads of the
// object verbs need tachyon.objects.read (docs/api-contract.md section 5). A verb with no scope here
// is refused on the console.
func ConsoleScope(verb string) (string, bool) {
	switch verb {
	case "objects.list", "objects.get", "objects.history", "objects.lookup_source":
		return "tachyon.objects.read", true
	case "planner.list", "planner.portfolio.resolve", "planner.portfolio.list", "planner.portfolio.get", "planner.portfolio.schema":
		return "tachyon.planner.read", true
	case "planner.portfolio.create", "planner.portfolio.patch", "planner.portfolio.action", "planner.portfolio.relate",
		"planner.portfolio.reorder", "planner.portfolio.assign", "planner.portfolio.unassign", "planner.portfolio.promote":
		return "tachyon.planner.write", true
	}
	return "", false
}

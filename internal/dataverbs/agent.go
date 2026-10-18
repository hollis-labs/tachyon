package dataverbs

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/objects"
)

const AgentPrefix = "/api/agent/v1/data"

// NewAgentHTTP authenticates every route, including discovery. Browser cookies
// and claimed actor headers cannot establish a service principal here.
func NewAgentHTTP(s *objects.Service, verifier auth.Verifier) http.Handler {
	guard, err := auth.New(auth.Config{Mode: auth.ModeEnforce, Verifier: verifier, OpenPath: func(string) bool { return false }})
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.FromContext(r.Context())
			if !ok || !principal.Verified {
				writeError(w, denied())
				return
			}
			p := &scopedLocal{local: NewLocal(s), principal: principal}
			newHTTP(AgentPrefix, []Provider{p}, nil).ServeHTTP(w, r)
		})).ServeHTTP(w, r)
	})
}

type scopedLocal struct {
	local     *Local
	principal auth.Principal
}

func (p *scopedLocal) ID() string { return p.local.ID() }
func (p *scopedLocal) Verbs() []Verb {
	out := []Verb{}
	for _, v := range p.local.Verbs() {
		if !v.Mutates && agentaccess.ReadVerb(v.Name) && agentaccess.Allows(p.principal, "verb", v.Name) {
			out = append(out, v)
		}
	}
	return out
}
func denied() error {
	return svcerr.New(svcerr.CodePermission, "This credential does not grant access to that operation or object kind.")
}
func (p *scopedLocal) kind(k objects.Kind) bool {
	return agentaccess.Allows(p.principal, "kind", string(k))
}
func (p *scopedLocal) Invoke(ctx context.Context, name string, raw json.RawMessage, _ string) (json.RawMessage, error) {
	if !agentaccess.ReadVerb(name) || !agentaccess.Allows(p.principal, "verb", name) {
		return nil, denied()
	}
	switch name {
	case "planner.list":
		var q objects.PlannerFilter
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if q.Kind != "" {
			if !p.kind(q.Kind) {
				return nil, denied()
			}
		} else {
			// Requiring the complete kind set avoids post-filter pagination leaks and
			// silently dropping permitted records. Narrow credentials specify a kind.
			for _, k := range agentaccess.PlannerKinds() {
				if !p.kind(objects.Kind(k)) {
					return nil, denied()
				}
			}
		}
	case "objects.list":
		var q objects.Filter
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if q.Kind == "" || !p.kind(q.Kind) {
			return nil, denied()
		}
	case "objects.lookup_source":
		var q objects.SourceKey
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.kind(q.Kind) {
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
		record, err := p.local.service.Get(ctx, id)
		if err != nil {
			return nil, serviceError(err)
		}
		if !p.kind(record.Kind) {
			return nil, denied()
		}
	default:
		return nil, denied()
	}
	// Local kinds are immutable. Authorized records may contain reference IDs,
	// but this surface never expands those references into other records.
	return p.local.Invoke(ctx, name, raw, "service:"+p.principal.ID)
}

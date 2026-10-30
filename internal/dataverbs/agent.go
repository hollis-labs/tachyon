package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
	"github.com/hollis-labs/tachyon/internal/tesseractread"
	"github.com/hollis-labs/tachyon/internal/torqueread"
)

const AgentPrefix = "/api/agent/v1/data"

// NewAgentHTTP authenticates every route, including discovery. Browser cookies
// and claimed actor headers cannot establish a service principal here.
func NewAgentHTTP(s *objects.Service, verifier auth.Verifier, services ...any) http.Handler {
	svc, cardSvc, torque, tess, chat, topics, plan, activity := agentServices(services)
	terminal := terminalServices(services)
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
			p := &scopedLocal{local: NewLocal(s), principal: principal, actions: svc, cards: cardSvc, torque: torque, tesseract: tess, chat: chat, topics: topics, plan: plan, activity: activity, terminal: terminal}
			// Authentication has already verified this request. scopedLocal checks
			// the verb/kinds and supplies the principal actor for every invocation.
			newHTTP(AgentPrefix, []Provider{p}, func(next http.Handler) http.Handler { return next }).ServeHTTP(w, r)
		})).ServeHTTP(w, r)
	})
}

type scopedLocal struct {
	local     *Local
	principal auth.Principal
	actions   *actionrun.Service
	cards     *Cards
	torque    *torqueread.Reader
	tesseract *tesseractread.Reader
	chat      *ChatWorkspace
	topics    *ChatTopics
	plan      *ChatPlan
	activity  *ChatActivity
	terminal  *ChatTerminal
}

// agentServices picks the optional host services out of the constructor's
// variadic list: a *actionrun.Service, a *Cards and a *torqueread.Reader, in any order.
func agentServices(services []any) (*actionrun.Service, *Cards, *torqueread.Reader, *tesseractread.Reader, *ChatWorkspace, *ChatTopics, *ChatPlan, *ChatActivity) {
	var actions *actionrun.Service
	var c *Cards
	var t *torqueread.Reader
	var te *tesseractread.Reader
	var chat *ChatWorkspace
	var topics *ChatTopics
	var plan *ChatPlan
	var activity *ChatActivity
	for _, s := range services {
		switch v := s.(type) {
		case *actionrun.Service:
			actions = v
		case *Cards:
			c = v
		case *torqueread.Reader:
			t = v
		case *tesseractread.Reader:
			te = v
		case *ChatWorkspace:
			chat = v
		case *ChatTopics:
			topics = v
		case *ChatPlan:
			plan = v
		case *ChatActivity:
			activity = v
		}
	}
	return actions, c, t, te, chat, topics, plan, activity
}

func (p *scopedLocal) ID() string { return p.local.ID() }
func (p *scopedLocal) Verbs() []Verb {
	out := []Verb{}
	for _, v := range p.local.Verbs() {
		if agentaccess.SupportedVerb(v.Name) && agentaccess.Allows(p.principal, "verb", v.Name) {
			out = append(out, v)
		}
	}
	if p.actions != nil {
		for _, v := range actionVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.torque != nil {
		for _, v := range torqueVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.tesseract != nil {
		for _, v := range tesseractVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.chat != nil {
		for _, v := range chatVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.topics != nil {
		for _, v := range chatTopicVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.activity != nil {
		for _, v := range chatActivityVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.plan != nil {
		for _, v := range chatPlanVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.terminal != nil {
		for _, v := range terminalVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
		}
	}
	if p.cards != nil {
		for _, v := range cardVerbs() {
			if agentaccess.Allows(p.principal, "verb", v.Name) {
				out = append(out, v)
			}
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
func (p *scopedLocal) database(db string) bool {
	kind, ok := planner.DatabaseKind(db)
	return ok && p.kind(kind)
}
func (p *scopedLocal) Invoke(ctx context.Context, name string, raw json.RawMessage, _ string) (json.RawMessage, error) {
	if !agentaccess.SupportedVerb(name) || !agentaccess.Allows(p.principal, "verb", name) {
		return nil, denied()
	}
	if agentaccess.ActionVerb(name) {
		return invokeAction(ctx, p.actions, name, raw, "service:"+p.principal.ID)
	}
	if agentaccess.TesseractVerb(name) {
		return invokeTesseract(ctx, p.tesseract, name, raw, p.principal)
	}
	if agentaccess.ChatWorkspaceVerb(name) {
		return invokeChat(ctx, p.chat, name, raw, p.principal)
	}
	if agentaccess.ChatActivityVerb(name) {
		return invokeChatActivity(ctx, p.activity, name, raw, p.principal)
	}
	if agentaccess.ChatPlanVerb(name) {
		return invokeChatPlan(ctx, p.plan, name, raw, p.principal)
	}
	if agentaccess.TerminalVerb(name) {
		return invokeTerminal(ctx, p.terminal, name, raw, p.principal)
	}
	if agentaccess.ChatTopicVerb(name) {
		return invokeChatTopic(ctx, p.topics, name, raw, p.principal)
	}
	if agentaccess.TorqueVerb(name) {
		return invokeTorque(ctx, p.torque, name, raw, p.principal)
	}
	if agentaccess.CardVerb(name) {
		return invokeCard(ctx, p.cards, name, raw, "service:"+p.principal.ID)
	}
	switch name {
	case "planner.portfolio.resolve":
		var q IDRequest
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		// Search only granted databases; a legacy ID must be unambiguous within
		// the caller's view, without disclosing records in other kinds.
		var found planner.Item
		s := &planner.Owned{Objects: p.local.service}
		for _, db := range []string{"priorities", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"} {
			if !p.database(db) {
				continue
			}
			row, err := s.Get(ctx, db, q.ID)
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
	case "planner.portfolio.list":
		var q portfolioList
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.get":
		var q portfolioID
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.schema":
		var q struct {
			DB string `json:"db"`
		}
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.create":
		var q portfolioCreate
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.patch":
		var q portfolioPatch
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.action":
		var q portfolioAction
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
		switch q.Action {
		case "comments", "link-add", "link-remove", "decide", "defer", "reopen", "dismiss", "ready", "approve", "park", "unpark", "finish", "resequence", "append_state", "verify":
		default:
			return nil, denied()
		}
	case "planner.portfolio.reorder":
		var q portfolioOrder
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.assign", "planner.portfolio.unassign":
		var q portfolioAssign
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.DB) {
			return nil, denied()
		}
	case "planner.portfolio.relate":
		var q planner.RelationRequest
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !p.database(q.From.DB) || !p.database(q.To.DB) {
			return nil, denied()
		}
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

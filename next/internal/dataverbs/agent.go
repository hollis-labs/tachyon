package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/planner"
)

// AgentPrefix is the agent data API: API keys mapped to a service identity only (access.RequireAgent).
const AgentPrefix = "/api/agent/v1/data"

// SessionHeader optionally carries the caller's Tether session id so the chat verbs can default to
// that session's thread (tether:<id>) with no thread_ref. It counts only from the pinned proxy.
const SessionHeader = "X-Tether-Session-Id"

// ContinuesHeader and LogicalAgentHeader carry, from the pinned Tether proxy only, the session the
// caller's session continues (a relaunch) and the logical agent it acts for. chat.plan.* uses them
// to carry a plan over to a replacement session of the same conversation.
const (
	ContinuesHeader    = "X-Tether-Continues-Session-Id"
	LogicalAgentHeader = "X-Tether-Logical-Agent-Id"
)

// Chat are the chat services the chat verbs call (slice B6d). A nil member leaves its verbs
// answering unavailable, as the old build did on a host without that service.
type Chat struct {
	Workspace *ChatWorkspace
	Topics    *ChatTopics
	Plan      *ChatPlan
	Activity  *ChatActivity
}

// NewAgentHTTP serves the data verbs to agents. Every request is resolved to its agent first; the
// verbs it lists and accepts are the ones its credential allows (the key's tachyon.<verb> scope and
// the mapping's verbs), and every call that touches an object checks the object's kind against the
// credential's kinds. The mutation actor is "service:<identity id>", never a request field.
func NewAgentHTTP(local *Local, acc *access.Service, chat Chat, native ...*approvals.Inbox) http.Handler {
	var inbox *approvals.Inbox
	if len(native) > 0 {
		inbox = native[0]
	}
	h := access.RequireAgent(acc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, _ := access.FromContext(r.Context())
		p := &scoped{local: local, agent: agent, chat: chat, gate: inbox, session: trustedSession(agent, r.Header.Get(SessionHeader))}
		ctx := withLineage(r.Context(), agent, r.Header)
		p.recordLineage(ctx)
		newHTTP(AgentPrefix, []Provider{p}, func(next http.Handler) http.Handler { return next }).ServeHTTP(w, r.WithContext(ctx))
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, AgentPrefix+"/providers/tachyon/verbs/gate.") {
			nativeGateHTTP(inbox, acc, w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// trustedSession returns the X-Tether-Session-Id value only when the agent is the pinned Tether
// proxy. From any other caller the header is ignored, so a forged header selects nothing.
func trustedSession(a access.Agent, header string) string {
	if !a.Identity.Proxy {
		return ""
	}
	return strings.TrimSpace(header)
}

type lineageKey struct{}

// lineage is the trusted continuation context of one proxied request.
type lineage struct{ agent, continues string }

// withLineage attaches the continuation headers to ctx, only for the pinned proxy.
func withLineage(ctx context.Context, a access.Agent, h http.Header) context.Context {
	if !a.Identity.Proxy {
		return ctx
	}
	l := lineage{agent: strings.TrimSpace(h.Get(LogicalAgentHeader)), continues: strings.TrimSpace(h.Get(ContinuesHeader))}
	if l == (lineage{}) {
		return ctx
	}
	return context.WithValue(ctx, lineageKey{}, l)
}

func lineageOf(ctx context.Context) lineage {
	l, _ := ctx.Value(lineageKey{}).(lineage)
	return l
}

// recordLineage notes the proxied session in the plan lineage on any request it makes, not only
// plan verbs, so a chain S2 -> S1 -> S0 resolves even when S1 never touched the plan. It also
// moves a continued conversation's plan to the session on first sight. Nothing is asked of the agent.
func (p *scoped) recordLineage(ctx context.Context) {
	l := lineageOf(ctx)
	if p.session == "" || l.agent == "" || p.chat.Plan == nil || p.chat.Plan.Service == nil {
		return
	}
	if _, err := chatapi.ParseRef("tether:" + p.session); err != nil {
		return
	}
	if _, err := chatapi.ParseRef("tether:" + l.continues); err != nil {
		l.continues = "" // not a session id: treat the session as a fresh start
	}
	p.chat.Plan.Service.Attach(ctx, p.session, l.agent, l.continues)
}

// scoped is the local provider narrowed to one agent's grant.
type scoped struct {
	local *Local
	gate  *approvals.Inbox
	agent access.Agent
	chat  Chat
	// session is the Tether session id the pinned proxy is acting for (SessionHeader); empty unless
	// the agent is the pinned proxy.
	session string
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
	for _, family := range []struct {
		on    bool
		verbs []Verb
	}{
		{p.chat.Workspace != nil, chatVerbs()},
		{p.chat.Topics != nil, chatTopicVerbs()},
		{p.chat.Plan != nil, chatPlanVerbs()},
		{p.chat.Activity != nil, chatActivityVerbs()},
	} {
		if !family.on {
			continue
		}
		for _, v := range family.verbs {
			if p.agent.Allows(v.Name, "") {
				out = append(out, v)
			}
		}
	}
	if p.gate != nil && p.gate.Native != nil && p.agent.Identity.ID == p.gate.Native.ProducerIdentity {
		for _, v := range gateVerbs() {
			if p.agent.Allows(v.Name, "") {
				out = append(out, v)
			}
		}
	}
	return out
}

// invokeChat dispatches a chat verb to its family, or reports false for any other verb.
func (p *scoped) invokeChat(ctx context.Context, name string, raw json.RawMessage) (json.RawMessage, bool, error) {
	var out json.RawMessage
	var err error
	switch name {
	case "chat.workspace.list", "chat.workspace.get":
		out, err = invokeChat(ctx, p.chat.Workspace, name, raw, p.agent, p.session)
	case "chat.topic.add", "chat.topic.list", "chat.bookmark.list", "chat.approvals.list":
		out, err = invokeChatTopic(ctx, p.chat.Topics, name, raw, p.agent, p.session)
	case "chat.plan.get", "chat.plan.list", "chat.plan.create", "chat.plan.move_step", "chat.plan.write", "chat.plan.step":
		out, err = invokeChatPlan(ctx, p.chat.Plan, name, raw, p.agent, p.session)
	case "chat.activity.append", "chat.panel.open":
		out, err = invokeChatActivity(ctx, p.chat.Activity, name, raw, p.agent, p.session)
	default:
		return nil, false, nil
	}
	return out, true, err
}

func denied() error {
	return svcerr.New(svcerr.CodePermission, "This credential does not grant access to that operation or object kind.")
}

// Invoke checks the verb and, for the object verbs, the object kind, exactly as the old build did.
func (p *scoped) Invoke(ctx context.Context, name string, raw json.RawMessage, _ string) (json.RawMessage, error) {
	if !slices.Contains(access.Verbs(), name) || !p.agent.Allows(name, "") {
		return nil, denied()
	}
	if strings.HasPrefix(name, "gate.") {
		return gateInvoke(ctx, p.gate, p.agent, name, raw)
	}
	if out, ok, err := p.invokeChat(ctx, name, raw); ok {
		return out, err
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
// object verbs need tachyon.objects.read and their writes tachyon.objects.write (docs/api-contract.md
// section 5). A verb with no scope here is refused on the console.
func ConsoleScope(verb string) (string, bool) {
	switch verb {
	case "objects.list", "objects.get", "objects.history", "objects.lookup_source":
		return "tachyon.objects.read", true
	case "objects.create", "objects.patch", "objects.delete", "objects.restore", "objects.take_ownership":
		return "tachyon.objects.write", true
	case "planner.list", "planner.portfolio.resolve", "planner.portfolio.list", "planner.portfolio.get", "planner.portfolio.schema":
		return "tachyon.planner.read", true
	case "planner.portfolio.create", "planner.portfolio.patch", "planner.portfolio.action", "planner.portfolio.relate",
		"planner.portfolio.reorder", "planner.portfolio.assign", "planner.portfolio.unassign", "planner.portfolio.promote":
		return "tachyon.planner.write", true
	}
	return "", false
}

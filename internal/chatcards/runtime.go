package chatcards

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tachyon/internal/appagent"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// Runtime turns the agent runtime's approval requests (Nanite approval.request, once scope) into cards and
// carries the operator's answer back. The wire answer is always allow-once or deny; "always in this chat" is
// a Tachyon rule that answers later requests of the same tool without asking.
type Runtime struct {
	B *Binder
	// Enqueue puts a card in the queue and returns its item id.
	Enqueue func(ctx context.Context, req hitl.EnqueueRequest) (string, error)
	// Respond sends the answer to the runtime for the thread.
	Respond func(ctx context.Context, thread, approvalID string, allow bool) error
	// Registry prepares the request (class, digest). Defaults to a new built-in registry.
	Registry *cards.Registry
	Source   string
}

const toolApprovalKind = "card.tool_approval"
const defaultToolTTL = 10 * time.Minute

// OnApproval is the appagent.Service.OnApproval hook; thread is the chat thread ref. It does not block: the work
// runs in a goroutine.
func (r *Runtime) OnApproval(thread string, a appagent.Approval) {
	go r.handle(context.Background(), thread, a)
}

func (r *Runtime) handle(ctx context.Context, thread string, a appagent.Approval) {
	class := "medium"
	if a.Class == "low" {
		class = "low"
	}
	var args map[string]any
	_ = json.Unmarshal(a.Input, &args)
	if r.B.Refs.Allows(ctx, thread, "", a.Tool, class, args) {
		_ = r.Respond(ctx, thread, a.ID, true)
		return
	}
	exp := a.ExpiresAt
	if exp.IsZero() || !exp.After(r.B.Refs.now()) {
		exp = r.B.Refs.now().Add(defaultToolTTL)
	}
	title := "Run " + a.Tool + "?"
	if a.Tool == "" {
		title = "The agent wants to run a tool"
	}
	req := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: toolApprovalKind, IdempotencyKey: "rt:" + a.ID,
		Source: hitl.SourceAssertion{ApplicationID: r.Source, ApplicationLabel: r.Source, AgentID: r.Source}, ExpiresAt: &exp,
		Extra: map[string]json.RawMessage{}}
	set := func(k string, v any) { req.Extra[k], _ = json.Marshal(v) }
	set("title", title)
	if a.Reason != "" {
		set("summary", a.Reason)
	}
	set("class", class)
	set("data", map[string]any{"tool": a.Tool, "input": args, "approval_id": a.ID, "call_id": a.CallID})
	set("runtime", map[string]any{"thread_ref": thread, "approval_id": a.ID, "tool": a.Tool, "args": args})
	reg := r.Registry
	if reg == nil {
		reg = cards.NewRegistry()
	}
	if err := reg.Prepare(&req, r.Source); err != nil {
		_ = r.Respond(ctx, thread, a.ID, false)
		return
	}
	id, err := r.Enqueue(ctx, req)
	if err != nil {
		_ = r.Respond(ctx, thread, a.ID, false)
		return
	}
	if err := r.B.Bind(ctx, id, toolApprovalKind, thread, ""); err != nil {
		_ = r.Respond(ctx, thread, a.ID, false)
	}
}

// Settled is called by the binder when a bound card ends. It answers the runtime for tool approvals and creates the
// rule for "always" on a low-class card. It reports whether the card was a tool approval (so the generic "card
// answered" message to the agent is skipped: the agent resumes on its own).
func (r *Runtime) Settled(ctx context.Context, rec hitl.Record) bool {
	if rec.Kind != toolApprovalKind {
		return false
	}
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &req)
	var rt struct {
		ThreadRef  string         `json:"thread_ref"`
		ApprovalID string         `json:"approval_id"`
		Tool       string         `json:"tool"`
		Args       map[string]any `json:"args"`
	}
	_ = json.Unmarshal(req.Extra["runtime"], &rt)
	var class string
	_ = json.Unmarshal(req.Extra["class"], &class)
	allow := false
	if res, ok := rec.Outcome.(hitl.Resolved); ok {
		switch res.Resolution.Response.Decision {
		case "approved":
			allow = true
		case "always":
			allow = true
			// A low-class approval becomes a rule for this tool. Medium and above stay one-off.
			_, _ = r.B.Refs.AddRule(ctx, rt.ThreadRef, "", rt.Tool, class, rt.Args, nil)
		}
	}
	_ = r.Respond(ctx, rt.ThreadRef, rt.ApprovalID, allow)
	return true
}

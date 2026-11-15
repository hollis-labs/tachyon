package needsyou

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Local sources: what waits on the operator inside Tachyon itself, where most asks happen today (in chat).

// ChatAsk is a chat thread with open turns that ask the operator (chatthreads: blocked_on_user).
type ChatAsk struct {
	Ref       string // thread_ref
	Title     string
	AgentID   string // the agent's launch id: the chat row is chat://tether/launch/<id>
	Agent     string // display label
	Questions int
	Approvals int
	LastAt    time.Time
}

// Chats lists chat threads with open asks.
type Chats interface {
	OpenAsks(ctx context.Context) ([]ChatAsk, error)
}

// ApprovalQueue summarizes Tachyon's approvals queue (internal/approvals): what is pending and not deferred.
type ApprovalQueue struct {
	Pending int
	High    int // pending at risk high
	Oldest  time.Time
}

// Approvals reads the approvals queue.
type Approvals interface {
	Waiting(ctx context.Context, now time.Time) (ApprovalQueue, error)
}

func (a *Aggregator) local(ctx context.Context, now time.Time, r *Result) {
	if a.Chats == nil {
		r.Sources["chat"] = SourceState{Error: "chat is not available"}
	} else if asks, err := a.Chats.OpenAsks(ctx); err != nil {
		r.Sources["chat"] = SourceState{Error: "chat threads unavailable: " + err.Error()}
	} else {
		r.Sources["chat"] = SourceState{OK: true}
		for _, c := range asks {
			if c.Questions+c.Approvals == 0 {
				continue
			}
			kind := "question"
			if c.Approvals > 0 {
				kind = "approval"
			}
			it := Item{ID: "chat:" + c.Ref, Source: "chat", Kind: kind, Why: "reply", Title: firstOf(oneLine(c.Title), c.Agent),
				From: firstOf(c.Agent, c.AgentID), Since: c.LastAt, Detail: counted(c.Approvals, "approval") + joinIf(counted(c.Approvals, "approval"), counted(c.Questions, "question"))}
			if c.AgentID != "" {
				it.Link = "/chat/" + url.PathEscape("chat://tether/launch/"+url.PathEscape(c.AgentID))
			}
			r.Items = append(r.Items, it)
		}
	}
	if a.Approvals == nil {
		r.Sources["approvals"] = SourceState{Error: "the approvals queue is not available"}
	} else if q, err := a.Approvals.Waiting(ctx, now); err != nil {
		r.Sources["approvals"] = SourceState{Error: "approvals queue unavailable: " + err.Error()}
	} else {
		r.Sources["approvals"] = SourceState{OK: true}
		if q.Pending > 0 {
			detail := ""
			if q.High > 0 {
				detail = fmt.Sprintf("%d high risk", q.High)
			}
			r.Items = append(r.Items, Item{ID: "approvals:queue", Source: "approvals", Kind: "approval", Why: "decide",
				Title: counted(q.Pending, "approval") + " waiting in the approvals queue", From: "Tachyon", Since: q.Oldest,
				Critical: q.High > 0, Detail: detail, Link: "/approvals"})
		}
	}
}

func counted(n int, what string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func joinIf(a, b string) string {
	if a != "" && b != "" {
		return ", " + b
	}
	return b
}

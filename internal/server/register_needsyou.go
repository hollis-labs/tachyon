package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/chatthreads"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/needsyou"
)

func init() { registerServerMount(mountNeedsYou) }

// mountNeedsYou serves GET needs-you (CW-20261114-0023c) behind the operator guard: what waits on @chrispian,
// read live from Tether with the read token and from the collector's seat statuses.
func mountNeedsYou(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	a := &needsyou.Aggregator{}
	if addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr); addr != "" && s.cfg.Upstreams.TetherTokenFile != "" {
		if base, c, err := builderread.NewTetherClient(addr, s.cfg.Upstreams.TetherTokenFile); err == nil {
			a.Tether = &needsyou.HTTPTether{Base: base, Client: c}
		}
	}
	if s.agentsCollector != nil {
		a.Statuses = s.agentsCollector.Store
	}
	if s.chatThreads != nil && s.chatThreads.Agg != nil {
		a.Chats = chatAsks{s.chatThreads.Agg}
	}
	if s.store != nil {
		a.Approvals = approvalQueue{s.store}
	}
	mux.Handle("GET "+needsyou.Path, operator.RequireOperator(a.Handler()))
}

// chatAsks reads the chat threads' open asks (blocked_on_user), archived threads excluded.
type chatAsks struct{ agg *chatthreads.Aggregator }

func (c chatAsks) OpenAsks(ctx context.Context) ([]needsyou.ChatAsk, error) {
	all, err := c.agg.Summaries(ctx)
	if err != nil {
		return nil, err
	}
	var out []needsyou.ChatAsk
	for _, t := range all {
		if t.Archived || t.BlockedOnUser == 0 {
			continue
		}
		approvals := t.BlockedBreakdown.Approvals
		out = append(out, needsyou.ChatAsk{Ref: t.Ref, Title: t.Title, AgentID: t.Agent.AgentID, Agent: t.Agent.Label,
			Questions: t.BlockedOnUser - approvals, Approvals: approvals, LastAt: time.UnixMilli(t.LastAt).UTC()})
	}
	return out, nil
}

// approvalQueue reads Tachyon's approvals queue: pending, not deferred.
type approvalQueue struct{ db *localstore.Store }

func (q approvalQueue) Waiting(ctx context.Context, now time.Time) (needsyou.ApprovalQueue, error) {
	store := &approvals.Store{DB: q.db}
	var out needsyou.ApprovalQueue
	// Counts cover the whole pending set; the pages (at most 100, newest first) are walked only to find the oldest.
	f := approvals.Filter{Limit: 100, Order: "created"}
	var last string
	for page := 0; page < 50; page++ {
		ids, next, counts, err := store.Pending(ctx, f, now)
		if err != nil {
			return out, err
		}
		if page == 0 {
			pending, _ := counts["pending"].(int)
			deferred, _ := counts["deferred"].(int)
			out.Pending = max(pending-deferred, 0)
			if byRisk, ok := counts["by_risk"].(map[string]int); ok {
				out.High = byRisk["high"]
			}
		}
		if len(ids) > 0 {
			last = ids[len(ids)-1]
		}
		if next == "" {
			break
		}
		f.After = next
	}
	if last != "" {
		if rec, err := store.Get(ctx, last); err == nil {
			out.Oldest = rec.CreatedAt.UTC()
		}
	}
	return out, nil
}

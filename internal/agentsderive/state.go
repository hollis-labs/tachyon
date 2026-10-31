// Package agentsderive turns collected facts into what the Agents monitor shows: session state,
// one-liners, context and token figures, compaction markers and sparklines. It is pure: no I/O.
//
// Rule from the design: raw model output and raw tool arguments are read in memory, reduced to
// derived fields, and dropped. Nothing here stores or logs an input.
package agentsderive

import "time"

// DefaultStaleAfter is the stale_after_s setting default (addendum 1, pending confirmation).
const DefaultStaleAfter = 15 * time.Minute

// States and stale reasons mirror agentsapi.
const (
	Working = "working"
	Waiting = "waiting"
	Idle    = "idle"
	Stale   = "stale"
	Unknown = "unknown"
	Ended   = "ended" // not shown on Sessions; goes to History

	NoEvents      = "no_events"
	NotAlive      = "not_alive"
	WorkerOffline = "worker_offline"
)

// Input is everything the state rules read. Pointers are tri-state: nil means Tether did not say.
type Input struct {
	SessionState    string // running | detached | orphaned | ended ...
	WorkState       string // idle | processing | awaiting_approval | awaiting_input; "" = unknown
	PendingApproval *bool
	PendingQuestion *bool
	LastActivity    time.Time // zero = never seen
	Alive           *bool     // health.alive
	WorkerOnline    *bool     // the hosting worker or Chimera still sees it
	ProcessGone     bool      // the Chimera scan reports the process gone
}

type Result struct {
	State       string
	StaleReason string
	StaleSince  time.Time
	NeedsYou    string // approval | question | ""
	Inferred    bool   // working because output arrived moments ago; Tether sent no work state
}

// OutputWorking is how recent the last output must be to call a session working when no work state is known.
const OutputWorking = 20 * time.Second

func yes(p *bool) bool { return p != nil && *p }
func no(p *bool) bool  { return p != nil && !*p }

// Derive applies the rules of addendum 1 over design 3.1:
//
//   - ended sessions are Ended (History, not Sessions);
//   - waiting = awaiting_* work state, or a pending approval/question; needs_you carries which;
//   - stale when the session is dead (not alive, orphaned, process gone) or its worker is offline,
//     whatever else it looks like, and when it is working or waiting with no event for staleAfter;
//   - idle is NEVER stale by age, so a long-silent idle session with a live process stays idle and
//     the row shows last_activity_at;
//   - a stale session that is also waiting keeps needs_you;
//   - unknown = no work state, no activity and no health.
func Derive(in Input, now time.Time, staleAfter time.Duration) Result {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	switch in.SessionState {
	case "ended", "stopped", "exited", "failed":
		return Result{State: Ended}
	}
	var r Result
	switch {
	case yes(in.PendingApproval) || in.WorkState == "awaiting_approval":
		r.NeedsYou = "approval"
	case yes(in.PendingQuestion) || in.WorkState == "awaiting_input":
		r.NeedsYou = "question"
	}
	base := Unknown
	switch {
	case r.NeedsYou != "":
		base = Waiting
	case in.WorkState == "processing":
		base = Working
	case in.WorkState == "idle":
		base = Idle
	case in.WorkState == "" && !in.LastActivity.IsZero() && now.Sub(in.LastActivity) >= 0 && now.Sub(in.LastActivity) <= OutputWorking:
		base, r.Inferred = Working, true // the stream carries no work-state events; fresh output means it is producing
	case !in.LastActivity.IsZero() || no(in.Alive):
		base = Idle // activity seen but no work state: quiet, not unknown
	}
	r.State = base
	switch {
	case no(in.Alive) || in.SessionState == "orphaned" || in.ProcessGone:
		r.State, r.StaleReason, r.StaleSince = Stale, NotAlive, since(in.LastActivity, now)
	case no(in.WorkerOnline):
		r.State, r.StaleReason, r.StaleSince = Stale, WorkerOffline, since(in.LastActivity, now)
	case (base == Working || base == Waiting) && !in.LastActivity.IsZero() && now.Sub(in.LastActivity) > staleAfter:
		r.State, r.StaleReason, r.StaleSince = Stale, NoEvents, in.LastActivity
	}
	return r
}

func since(t, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}

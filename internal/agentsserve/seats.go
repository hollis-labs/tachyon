package agentsserve

import (
	"net/http"
	"sort"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
)

// Seat is a seat status card (CW-20261114-0023d): what the seat declared (Tether seat.status, kept by the collector)
// beside what Tachyon sees it doing (its live session). It mirrors Tether's GET /status/{@handle} card, built from
// local data so the tab costs no call per seat.
type Seat struct {
	AgentURN string `json:"agent_urn"`
	Handle   string `json:"handle"` // as the seat declared it; "" when it never has
	Name     string `json:"name"`   // the live session's agent name, else the handle
	// Effective is the declared state while it has not expired; "undeclared" when the seat is online without one;
	// "unknown" when it is neither (Tether's rule for the card's effective).
	Effective string                      `json:"effective"`
	Declared  *agentscollector.SeatStatus `json:"declared"`
	Live      *SeatLive                   `json:"live"`
}

// SeatLive is the seat's newest live session.
type SeatLive struct {
	SessionID      string  `json:"session_id"`
	State          string  `json:"state"` // the session state: working, waiting, idle, stale, unknown
	NeedsYou       *string `json:"needs_you"`
	LastActivityAt *int64  `json:"last_activity_at"`
}

// seatOrder puts what needs someone first.
var seatOrder = map[string]int{"blocked": 0, "waiting-decision": 1, "waiting-agent": 2, "working": 3, "available": 4, "paused": 5, "undeclared": 6, "offline": 7, "unknown": 8}

func (s *Server) seats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.Collector == nil || s.Collector.Store == nil {
		fail(w, 503, "unavailable", "Seat statuses are not collected.")
		return
	}
	declared, err := s.Collector.Store.SeatStatuses(ctx, s.now())
	if err != nil {
		internal(w)
		return
	}
	sessions, err := s.all(ctx)
	if err != nil {
		internal(w)
		return
	}
	by := map[string]*Seat{}
	for i := range declared {
		d := declared[i]
		by[d.AgentURN] = &Seat{AgentURN: d.AgentURN, Handle: d.Handle, Name: d.Handle, Effective: d.State, Declared: &d}
	}
	newest := map[string]int64{}
	for _, sess := range sessions {
		if sess.State == StateEnded || sess.Agent.URN == nil || *sess.Agent.URN == "" {
			continue
		}
		urn := *sess.Agent.URN
		if at, seen := newest[urn]; seen && at >= sess.StartedAt {
			continue // a seat's newest live session speaks for it
		}
		newest[urn] = sess.StartedAt
		seat := by[urn]
		if seat == nil {
			seat = &Seat{AgentURN: urn, Effective: "undeclared"}
			by[urn] = seat
		}
		if sess.Agent.Name != "" {
			seat.Name = sess.Agent.Name
		}
		seat.Live = &SeatLive{SessionID: sess.ID, State: sess.State, NeedsYou: sess.NeedsYou.Reason, LastActivityAt: sess.LastActivityAt}
	}
	out := make([]Seat, 0, len(by))
	for _, seat := range by {
		if seat.Name == "" {
			seat.Name = seat.AgentURN
		}
		out = append(out, *seat)
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := seatOrder[out[i].Effective], seatOrder[out[j].Effective]
		if oi != oj {
			return oi < oj
		}
		return out[i].Name < out[j].Name
	})
	reply(w, 200, map[string]any{"seats": out})
}

package agentsserve

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/agentscollector"
	"github.com/hollis-labs/tachyon/next/internal/objects"
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
	// SetByOther: the status was set by someone other than the seat or the operator (a manager, or a caller that
	// is neither): shown as a warning, since the seat did not say it.
	SetByOther bool `json:"set_by_other"`
	// Stale: the declaration no longer describes the seat: it has no live session (and did not declare itself
	// offline), or it declared more than StaleAfter ago and its session has done nothing since. Stale cards sort
	// below current ones.
	Stale bool `json:"stale"`
}

// StaleAfter is how old a declaration may be, with no session activity since, before its card is stale.
const StaleAfter = 4 * time.Hour

// operatorForms are the operator's addresses (tether#554 adds the canonical one).
var operatorForms = map[string]bool{"msg://user/local/operator": true, "msg://user/local/chrispian": true}

// SeatLive is the seat's newest live session.
type SeatLive struct {
	SessionID      string  `json:"session_id"`
	State          string  `json:"state"` // the session state: working, waiting, idle, stale, unknown
	NeedsYou       *string `json:"needs_you"`
	LastActivityAt *int64  `json:"last_activity_at"`
}

// seatOrder puts what needs someone first.
var seatOrder = map[string]int{"blocked": 0, "waiting-decision": 1, "waiting-agent": 2, "working": 3, "available": 4, "paused": 5, "undeclared": 6, "offline": 7, "unknown": 8}

// seatAliases maps each form a seat's address takes to its registry URN, from the synced Tether catalog
// (catalog/agents: catalog id and urn). A session reports its agent as msg://agent/agent-mux/<catalog id> while
// the seat's own status comes from its registry URN (agt_...); without this a seat would get two cards.
func (s *Server) seatAliases(ctx context.Context) map[string]string {
	out := map[string]string{}
	svc := objects.New(s.DB)
	f := objects.Filter{Kind: objects.AgentKind, Limit: 500}
	for page := 0; page < 20; page++ {
		rows, err := svc.List(ctx, f)
		if err != nil {
			return out
		}
		for _, r := range rows {
			src := r.Source
			if src == nil || src.DeletedAt != nil || src.Key.Provider != "tether" || src.Key.Collection != "catalog/agents" {
				continue
			}
			var d struct {
				Properties struct {
					Tether struct {
						URN       string `json:"urn"`
						CatalogID string `json:"catalog_id"`
					} `json:"tether"`
				} `json:"properties"`
			}
			if json.Unmarshal(src.Definition, &d) != nil {
				continue
			}
			t := d.Properties.Tether
			if t.URN == "" {
				continue
			}
			out[t.URN] = t.URN
			for _, id := range []string{t.CatalogID, src.Key.ExternalID} {
				if id != "" && !strings.ContainsAny(id, "/ ?#") {
					out[agentMuxURNPrefix+id] = t.URN
				}
			}
		}
		if len(rows) < f.Limit {
			break
		}
		f.AfterID = rows[len(rows)-1].ID
	}
	return out
}

func (s *Server) seats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.Collector == nil || s.Collector.Store == nil {
		fail(w, 503, "unavailable", "Seat statuses are not collected.")
		return
	}
	now := s.now()
	declared, err := s.Collector.Store.SeatStatuses(ctx, now)
	if err != nil {
		internal(w)
		return
	}
	sessions, err := s.all(ctx)
	if err != nil {
		internal(w)
		return
	}
	aliases := s.seatAliases(ctx)
	canon := func(urn string) string {
		if c, ok := aliases[urn]; ok {
			return c
		}
		return urn
	}
	by := map[string]*Seat{}
	for i := range declared {
		d := declared[i]
		key := canon(d.AgentURN)
		if old := by[key]; old != nil && old.Declared != nil && !d.At.After(old.Declared.At) {
			continue // two address forms declared: the newest declaration speaks
		}
		by[key] = &Seat{AgentURN: key, Handle: d.Handle, Name: d.Handle, Effective: d.State, Declared: &d}
	}
	newest := map[string]int64{}
	ownSessions := map[string]map[string]bool{} // seat -> its session ids, live or ended
	for _, sess := range sessions {
		if sess.Agent.URN == nil || *sess.Agent.URN == "" {
			continue
		}
		key := canon(*sess.Agent.URN)
		if ownSessions[key] == nil {
			ownSessions[key] = map[string]bool{}
		}
		ownSessions[key][sess.ID] = true
		if sess.State == StateEnded {
			continue
		}
		if at, seen := newest[key]; seen && at >= sess.StartedAt {
			continue // a seat's newest live session speaks for it
		}
		newest[key] = sess.StartedAt
		seat := by[key]
		if seat == nil {
			seat = &Seat{AgentURN: key, Effective: "undeclared"}
			by[key] = seat
		}
		if sess.Agent.Name != "" {
			seat.Name = sess.Agent.Name
		}
		seat.Live = &SeatLive{SessionID: sess.ID, State: sess.State, NeedsYou: sess.NeedsYou.Reason, LastActivityAt: sess.LastActivityAt}
	}
	out := make([]Seat, 0, len(by))
	for key, seat := range by {
		if seat.Name == "" {
			seat.Name = seat.AgentURN
		}
		if d := seat.Declared; d != nil {
			setBy := canon(d.SetBy)
			sid, isSession := strings.CutPrefix(d.SetBy, "msg://session/local/")
			seat.SetByOther = d.SetBy != "" && setBy != key && !operatorForms[d.SetBy] && !(isSession && ownSessions[key][sid]) //nolint:staticcheck // carried as written
			quiet := seat.Live == nil || seat.Live.LastActivityAt == nil || *seat.Live.LastActivityAt < d.At.UnixMilli()
			seat.Stale = d.State != "offline" && (seat.Live == nil || (now.Sub(d.At) > StaleAfter && quiet))
		}
		out = append(out, *seat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stale != out[j].Stale {
			return !out[i].Stale // current cards first
		}
		oi, oj := seatOrder[out[i].Effective], seatOrder[out[j].Effective]
		if oi != oj {
			return oi < oj
		}
		return out[i].Name < out[j].Name
	})
	reply(w, 200, map[string]any{"seats": out})
}

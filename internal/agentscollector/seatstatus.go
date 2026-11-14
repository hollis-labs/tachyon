package agentscollector

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/hollis-labs/libs/security/redact"
)

// SeatStatus is a seat's declared status (Tether seat.status, docs/typed-messages.md "Seat status"): what the seat
// says it is doing, not what Tachyon infers. Every field is validated on the way in; a field that does not pass is
// dropped, and an event without an agent or a known state is not kept.
type SeatStatus struct {
	AgentURN  string     `json:"agent_urn"`
	Handle    string     `json:"handle"`
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	Ref       string     `json:"ref"`
	Owner     string     `json:"owner"`
	ExpiresAt *time.Time `json:"expires_at"`
	SetBy     string     `json:"set_by"`
	At        time.Time  `json:"at"`
	Seq       int64      `json:"-"`
}

// SeatStates are Tether's declared states.
var SeatStates = map[string]bool{"available": true, "working": true, "waiting-decision": true, "waiting-agent": true, "blocked": true, "paused": true, "offline": true}

var (
	handleRE    = regexp.MustCompile(`^@[a-z][a-z0-9_-]{0,63}$`)
	urnRE       = regexp.MustCompile(`^msg://[A-Za-z0-9._:/@-]{1,200}$`)
	seatRefRE   = regexp.MustCompile(`^(task:[A-Za-z0-9._-]{1,60}|https://[^\s]{1,300})$`)
	seatSetByRE = regexp.MustCompile(`^[A-Za-z0-9._:/@-]{1,200}$`)
)

// seatStatusOf reads a seat.status payload.
func seatStatusOf(f Frame, body []byte) *SeatStatus {
	var p struct {
		AgentURN  string `json:"agent_urn"`
		Handle    string `json:"handle"`
		State     string `json:"state"`
		Reason    string `json:"reason"`
		Ref       string `json:"ref"`
		Owner     string `json:"owner"`
		ExpiresAt string `json:"expires_at"`
		SetBy     string `json:"set_by"`
	}
	if json.Unmarshal(body, &p) != nil || !urnRE.MatchString(p.AgentURN) || !SeatStates[p.State] {
		return nil
	}
	s := &SeatStatus{AgentURN: p.AgentURN, State: p.State, At: f.At, Seq: f.Seq}
	if handleRE.MatchString(p.Handle) {
		s.Handle = p.Handle
	}
	if handleRE.MatchString(p.Owner) {
		s.Owner = p.Owner
	}
	if seatRefRE.MatchString(p.Ref) {
		s.Ref = p.Ref
	}
	if seatSetByRE.MatchString(p.SetBy) {
		s.SetBy = p.SetBy
	}
	s.Reason = EventSummary(redact.Text(p.Reason)) // one line, redacted, bounded like a summary
	if t, err := time.Parse(time.RFC3339Nano, p.ExpiresAt); err == nil {
		t = t.UTC()
		s.ExpiresAt = &t
	}
	return s
}

// upsertSeatStatus keeps the newest status per seat: an older event (a replay, a backfill) never overwrites it.
func upsertSeatStatus(ctx context.Context, tx *sql.Tx, s *SeatStatus, at int64) error {
	var exp any
	if s.ExpiresAt != nil {
		exp = s.ExpiresAt.UnixNano()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO seat_status(agent_urn,handle,state,reason,ref,owner,expires_at_ns,set_by,at_ns,tether_seq) VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(agent_urn) DO UPDATE SET handle=excluded.handle,state=excluded.state,reason=excluded.reason,ref=excluded.ref,owner=excluded.owner,
 expires_at_ns=excluded.expires_at_ns,set_by=excluded.set_by,at_ns=excluded.at_ns,tether_seq=excluded.tether_seq WHERE excluded.tether_seq>seat_status.tether_seq`,
		s.AgentURN, s.Handle, s.State, s.Reason, s.Ref, s.Owner, exp, s.SetBy, at, s.Seq)
	return err
}

// SeatStatuses returns every seat's latest declared status that has not expired at now, newest first.
func (s *Store) SeatStatuses(ctx context.Context, now time.Time) ([]SeatStatus, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT agent_urn,handle,state,reason,ref,owner,expires_at_ns,set_by,at_ns,tether_seq FROM seat_status
 WHERE expires_at_ns IS NULL OR expires_at_ns>? ORDER BY at_ns DESC`, now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeatStatus{}
	for rows.Next() {
		var st SeatStatus
		var exp sql.NullInt64
		var at int64
		if err := rows.Scan(&st.AgentURN, &st.Handle, &st.State, &st.Reason, &st.Ref, &st.Owner, &exp, &st.SetBy, &at, &st.Seq); err != nil {
			return nil, err
		}
		st.At = time.Unix(0, at).UTC()
		if exp.Valid {
			t := time.Unix(0, exp.Int64).UTC()
			st.ExpiresAt = &t
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SeatHistory is a source that can list past seat.status events, newest first (Tether GET /events?kind=).
type SeatHistory interface {
	SeatStatusHistory(ctx context.Context, limit int) ([]Frame, error)
}

func (s *httpSource) SeatStatusHistory(ctx context.Context, limit int) ([]Frame, error) {
	q := url.Values{"kind": {"seat.status"}, "order": {"desc"}, "limit": {strconv.Itoa(limit)}}
	var w struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := s.get(ctx, "/events", q, 16<<20, &w); err != nil {
		return nil, err
	}
	var out []Frame
	for _, raw := range w.Events {
		if f, ok := ParseFrame(raw); ok && f.Seat != nil {
			out = append(out, f)
		}
	}
	return out, nil
}

// BackfillSeatStatuses keeps each seat's newest declared status from frames (any order): a status declared before
// the collector kept them, or while it was down, would otherwise stay unseen, because Tether emits nothing when a
// seat re-sets the same status. An older frame never overwrites a newer row.
func (s *Store) BackfillSeatStatuses(ctx context.Context, frames []Frame) (int, error) {
	n := 0
	err := s.DB.Writer.Submit(ctx, "seat_status_backfill", func(ctx context.Context, tx *sql.Tx) error {
		for _, f := range frames {
			if f.Seat == nil {
				continue
			}
			at := f.At.UnixNano()
			if f.At.IsZero() {
				continue // without a time the card could not say when it was declared
			}
			if err := upsertSeatStatus(ctx, tx, f.Seat, at); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// seatBackfillLimit is how many recent seat.status events the backfill reads (one page; Tether's filtered history
// answered 200 in about 2s on agent-os, within the source's 20s request bound).
const seatBackfillLimit = 200

// seatBackfillRetry is the first wait between backfill attempts; it doubles each time (a variable so tests can
// shorten it). Tether's filtered history answers in about 2s but spikes past the source's bound (48s seen).
var seatBackfillRetry = time.Minute

// backfillSeats runs once per collector start, in the background: Tether's filtered history can be slow, and the
// live stream must not wait for it. A failed attempt is retried, up to five attempts, waiting longer each time.
func (c *Collector) backfillSeats(ctx context.Context) {
	h, ok := c.Source.(SeatHistory)
	if !ok {
		return
	}
	wait := seatBackfillRetry
	for attempt := 1; attempt <= 5; attempt++ {
		frames, err := h.SeatStatusHistory(ctx, seatBackfillLimit)
		if err == nil {
			n, serr := c.Store.BackfillSeatStatuses(ctx, frames)
			if serr == nil {
				slog.Info("agents collector: seat statuses backfilled", "events", n)
				return
			}
			err = serr
		}
		slog.Warn("agents collector: seat status backfill failed", "attempt", attempt, "err", errText(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait *= 2
	}
}

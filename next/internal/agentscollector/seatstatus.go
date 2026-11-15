package agentscollector

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/next/internal/store"
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
func upsertSeatStatus(ctx context.Context, tx store.Tx, s *SeatStatus, at int64) error {
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
	defer func() { _ = rows.Close() }()
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

// SeatCards is a source that can read one seat's status card (Tether GET /status/{@handle}): a cheap, keyed read
// on the hub, unlike a kind-filtered event-history scan, which this backfill must never run.
type SeatCards interface {
	SeatCard(ctx context.Context, handle string) (*SeatStatus, error)
}

// seatCardTimeout bounds each card read.
const seatCardTimeout = 5 * time.Second

func (s *httpSource) SeatCard(ctx context.Context, handle string) (*SeatStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, seatCardTimeout)
	defer cancel()
	var card struct {
		Handle   string `json:"handle"`
		AgentURN string `json:"agent_urn"`
		Expired  bool   `json:"expired"`
		Declared *struct {
			AgentURN  string    `json:"agent_urn"`
			State     string    `json:"state"`
			Reason    string    `json:"reason"`
			Ref       string    `json:"ref"`
			Owner     string    `json:"owner"`
			Since     time.Time `json:"since"`
			ExpiresAt time.Time `json:"expires_at"`
			SetBy     string    `json:"set_by"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"declared"`
	}
	if err := s.get(ctx, "/status/"+url.PathEscape(handle), url.Values{}, 64<<10, &card); err != nil {
		return nil, err
	}
	d := card.Declared
	if d == nil || card.Expired {
		return nil, nil
	}
	// Validated exactly as a live seat.status: the same fields, the same rules.
	body := map[string]any{"agent_urn": firstNonEmpty(d.AgentURN, card.AgentURN), "handle": card.Handle, "state": d.State, "reason": d.Reason,
		"ref": d.Ref, "owner": d.Owner, "set_by": d.SetBy}
	if !d.ExpiresAt.IsZero() {
		body["expires_at"] = d.ExpiresAt.Format(time.RFC3339Nano)
	}
	raw, _ := json.Marshal(body)
	at := d.UpdatedAt
	if at.IsZero() {
		at = d.Since
	}
	if at.IsZero() {
		return nil, nil // without a time the card could not say when it was declared
	}
	return seatStatusOf(Frame{At: at.UTC()}, raw), nil
}

// BackfillSeatStatus stores a status read from a card. It never replaces a row the live stream wrote (seq > 0):
// it only fills a seat with no row, or refreshes a backfilled row with a newer declaration.
func (s *Store) BackfillSeatStatus(ctx context.Context, st *SeatStatus) error {
	var exp any
	if st.ExpiresAt != nil {
		exp = st.ExpiresAt.UnixNano()
	}
	return s.DB.Writer.Submit(ctx, "seat_status_backfill", func(ctx context.Context, tx store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO seat_status(agent_urn,handle,state,reason,ref,owner,expires_at_ns,set_by,at_ns,tether_seq) VALUES(?,?,?,?,?,?,?,?,?,0)
ON CONFLICT(agent_urn) DO UPDATE SET handle=excluded.handle,state=excluded.state,reason=excluded.reason,ref=excluded.ref,owner=excluded.owner,
 expires_at_ns=excluded.expires_at_ns,set_by=excluded.set_by,at_ns=excluded.at_ns WHERE seat_status.tether_seq=0 AND excluded.at_ns>seat_status.at_ns`,
			st.AgentURN, st.Handle, st.State, st.Reason, st.Ref, st.Owner, exp, st.SetBy, st.At.UnixNano())
		return err
	})
}

// catalogHandles are the default @handles of the synced Tether catalog agents (a seat's handle is its catalog id
// unless the catalog overrides it; an overridden one is simply not found).
func (s *Store) catalogHandles(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT DISTINCT external_id FROM object_sources WHERE provider='tether' AND collection='catalog/agents' AND deleted_at IS NULL ORDER BY external_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && handleRE.MatchString("@"+id) {
			out = append(out, "@"+id)
		}
	}
	return out, rows.Err()
}

// backfillSeats runs once per collector start, in the background: a status declared before Tachyon kept them, or
// while the collector was down, is otherwise unseen until it changes (Tether emits nothing when a seat re-sets the
// same status). It reads each catalog seat's card in turn, each bounded by seatCardTimeout.
func (c *Collector) backfillSeats(ctx context.Context) {
	cards, ok := c.Source.(SeatCards)
	if !ok {
		return
	}
	handles, err := c.Store.catalogHandles(ctx)
	if err != nil {
		slog.Warn("agents collector: seat backfill could not list seats", "err", errText(err))
		return
	}
	stored, failed := 0, 0
	for _, h := range handles {
		if ctx.Err() != nil {
			return
		}
		st, err := cards.SeatCard(ctx, h)
		if err != nil {
			failed++
			continue
		}
		if st == nil {
			continue
		}
		if err := c.Store.BackfillSeatStatus(ctx, st); err != nil {
			failed++
			continue
		}
		stored++
	}
	slog.Info("agents collector: seat statuses backfilled from cards", "seats", len(handles), "stored", stored, "failed", failed)
}

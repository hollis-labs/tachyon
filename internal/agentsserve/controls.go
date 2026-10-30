package agentsserve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"net/http"
	"strings"

	"github.com/hollis-labs/tachyon/internal/agentsapi"
)

// Sender delivers a nudge to an agent as Tether mail (the control token holds the messages scope). It returns
// the Tether message id.
type Sender interface {
	Send(ctx context.Context, to, subject, body string, meta map[string]string) (string, error)
}

// Interrupt and restart need Tether verbs that do not exist yet (CW-20261030-0025). Each is gated on a
// capability flag, never faked: with the flag off the request answers 409 and nothing is sent.
const (
	interruptReason = "Tether has no session interrupt yet."
	restartReason   = "Tether has no session restart yet."
)

// control is the ControlRequest of the contract.
type control struct {
	RequestID        string  `json:"request_id"`
	Kind             string  `json:"kind"`
	SessionID        string  `json:"session_id"`
	State            string  `json:"state"`
	Step             *string `json:"step"`
	Error            *string `json:"error"`
	Interrupt        *string `json:"interrupt"`
	MessageID        *string `json:"message_id"`
	Handoff          bool    `json:"handoff"`
	NewSessionID     *string `json:"new_session_id"`
	DiscardedPending *int64  `json:"discarded_pending"`
	WaitEndsAt       *int64  `json:"wait_ends_at"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

const controlCols = `request_id,kind,session_id,state,step,error,interrupt,message_id,handoff,new_session_id,discarded_pending,wait_ends_at,created_ms,updated_ms FROM agents_controls`

func scanControl(sc interface{ Scan(...any) error }) (control, error) {
	var c control
	var step, errS, intr, mid, nsid sql.NullString
	var dp, we sql.NullInt64
	var hand int
	if err := sc.Scan(&c.RequestID, &c.Kind, &c.SessionID, &c.State, &step, &errS, &intr, &mid, &hand, &nsid, &dp, &we, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	str := func(n sql.NullString) *string {
		if !n.Valid {
			return nil
		}
		return &n.String
	}
	c.Step, c.Error, c.Interrupt, c.MessageID, c.NewSessionID = str(step), str(errS), str(intr), str(mid), str(nsid)
	c.Handoff = hand != 0
	if dp.Valid {
		c.DiscardedPending = &dp.Int64
	}
	if we.Valid {
		c.WaitEndsAt = &we.Int64
	}
	return c, nil
}

func (s *Server) loadControl(ctx context.Context, id string) (control, bool) {
	c, err := scanControl(s.DB.Reader.QueryRowContext(ctx, `SELECT `+controlCols+` WHERE request_id=?`, id))
	return c, err == nil
}

// saveControl writes the row and the matching replayable stream event in one transaction.
func (s *Server) saveControl(ctx context.Context, c control, insert bool) error {
	c.UpdatedAt = s.now().UnixMilli()
	if insert {
		c.CreatedAt = c.UpdatedAt
	}
	ev, _ := json.Marshal(map[string]any{"request_id": c.RequestID, "state": c.State, "kind": c.Kind, "session_id": c.SessionID})
	return s.DB.Writer.Submit(ctx, "agents_control", func(ctx context.Context, tx *sql.Tx) error {
		h := 0
		if c.Handoff {
			h = 1
		}
		if insert {
			if _, err := tx.ExecContext(ctx, `INSERT INTO agents_controls(request_id,kind,session_id,state,step,error,interrupt,message_id,handoff,new_session_id,discarded_pending,wait_ends_at,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				c.RequestID, c.Kind, c.SessionID, c.State, c.Step, c.Error, c.Interrupt, c.MessageID, h, c.NewSessionID, c.DiscardedPending, c.WaitEndsAt, c.CreatedAt, c.UpdatedAt); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE agents_controls SET state=?,step=?,error=?,interrupt=?,message_id=?,updated_ms=? WHERE request_id=?`,
			c.State, c.Step, c.Error, c.Interrupt, c.MessageID, c.UpdatedAt, c.RequestID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO agents_stream(type,at_ns,data_json) VALUES('control',?,?)`, s.now().UnixNano(), string(ev))
		return err
	})
}

// capabilities: nudge needs a sender and an addressable, live session; interrupt and restart stay off.
func (s *Server) capabilities(sess Session) Capabilities {
	return Capabilities{
		Nudge:     s.Sender != nil && sess.Agent.URN != nil && sess.State != StateEnded,
		Interrupt: Interrupt{Reason: strp(interruptReason)},
	}
}

func (s *Server) nudge(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.one(w, r)
	if !ok {
		return
	}
	var b struct {
		RequestID string `json:"request_id"`
		Text      string `json:"text"`
		Interrupt bool   `json:"interrupt"`
	}
	if !decodeBody(w, r, 16<<10, &b) {
		return
	}
	if !idRE.MatchString(b.RequestID) || strings.TrimSpace(b.Text) == "" || utf8.RuneCountInString(b.Text) > agentsapi.MaxNudgeText {
		fail(w, 400, "invalid", "request_id and text (1 to 4000 characters) are required.")
		return
	}
	if old, ok := s.loadControl(r.Context(), b.RequestID); ok { // a retry of the same request
		if old.SessionID != sess.ID || old.Kind != agentsapi.ControlNudge {
			fail(w, 409, "conflict", "That request_id belongs to another request.")
			return
		}
		reply(w, 202, old)
		return
	}
	if !s.capabilities(sess).Nudge {
		fail(w, 409, "nudge_unsupported", "This session cannot receive nudges.")
		return
	}
	if b.Interrupt { // never silently downgrade: the client offers "send without interrupt"
		fail(w, 409, "interrupt_unsupported", interruptReason)
		return
	}
	c := control{RequestID: b.RequestID, Kind: agentsapi.ControlNudge, SessionID: sess.ID, State: "queued"}
	if err := s.saveControl(r.Context(), c, true); err != nil {
		internal(w)
		return
	}
	id, err := s.Sender.Send(r.Context(), *sess.Agent.URN, "Message from the operator", b.Text, map[string]string{"source": "tachyon-agents-nudge", "request_id": b.RequestID})
	if err != nil {
		msg := errors.New("Tether could not deliver the message.").Error()
		c.State, c.Error = "failed", &msg
	} else {
		c.State = "delivered"
		if id != "" {
			c.MessageID = &id
		}
	}
	if err := s.saveControl(r.Context(), c, false); err != nil {
		internal(w)
		return
	}
	if saved, ok := s.loadControl(r.Context(), c.RequestID); ok {
		c = saved
	}
	reply(w, 202, c)
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.one(w, r)
	if !ok {
		return
	}
	var b struct {
		RequestID         string `json:"request_id"`
		Handoff           bool   `json:"handoff"`
		ExpectedSessionID string `json:"expected_session_id"`
	}
	if !decodeBody(w, r, 16<<10, &b) {
		return
	}
	if !idRE.MatchString(b.RequestID) || b.ExpectedSessionID == "" {
		fail(w, 400, "invalid", "request_id and expected_session_id are required.")
		return
	}
	if b.ExpectedSessionID != sess.ID {
		fail(w, 409, "stale_session", "expected_session_id is not the agent's current session.")
		return
	}
	fail(w, 409, "restart_unsupported", restartReason)
}

func (s *Server) getControl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("request_id")
	if c, ok := s.loadControl(r.Context(), id); ok && idRE.MatchString(id) {
		reply(w, 200, c)
		return
	}
	fail(w, 404, "not_found", "No such control request.")
}

// skipControl skips the restart handoff wait; no restart can exist yet, so it never has a wait to skip.
func (s *Server) skipControl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("request_id")
	c, ok := s.loadControl(r.Context(), id)
	if !ok || !idRE.MatchString(id) {
		fail(w, 404, "not_found", "No such control request.")
		return
	}
	if c.Kind != agentsapi.ControlRestart || c.State != "waiting" {
		fail(w, 409, "not_waiting", "This request is not waiting for a handoff.")
		return
	}
	fail(w, 409, "restart_unsupported", restartReason)
}

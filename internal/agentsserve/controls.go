package agentsserve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentsapi"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
)

// Sender delivers a nudge to an agent as Tether mail (the control token holds the messages scope). It returns
// the Tether message id.
type Sender interface {
	Send(ctx context.Context, to, subject, body string, meta map[string]string) (string, error)
}

// Interrupter cancels the running turn of a session (Tether: a reply with interrupt:true to its latest routed
// message) and delivers body as the next turn. It returns the interrupt result for ControlRequest.interrupt.
type Interrupter interface {
	Interrupt(ctx context.Context, sessionID, body string) (string, error)
}

// Restarter stops a session and resumes its logical agent (Tether session stop, then logical-agent resume).
type Restarter interface {
	Stop(ctx context.Context, sessionID string) error
	Resume(ctx context.Context, logicalAgentID, key string) (newSessionID string, err error)
}

const (
	interruptReason = "This session has no routed turn Tachyon can interrupt."
	restartReason   = "This agent has no logical agent to resume."
	// handoffWait is how long a restart waits for the agent to write its handoff before it proceeds.
	handoffWait = 60 * time.Second
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
		} else if _, err := tx.ExecContext(ctx, `UPDATE agents_controls SET state=?,step=?,error=?,interrupt=?,message_id=?,new_session_id=?,wait_ends_at=?,updated_ms=? WHERE request_id=?`,
			c.State, c.Step, c.Error, c.Interrupt, c.MessageID, c.NewSessionID, c.WaitEndsAt, c.UpdatedAt, c.RequestID); err != nil {
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
		Interrupt: Interrupt{Available: s.Interrupter != nil && sess.State != StateEnded, Reason: s.interruptWhy(sess)},
		Restart:   s.Restarter != nil && sess.Agent.LogicalAgentID != nil && sess.State != StateEnded,
	}
}

func (s *Server) interruptWhy(sess Session) *string {
	if s.Interrupter != nil && sess.State != StateEnded {
		return nil
	}
	return strp("Interrupt is not available for this session.")
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
	if b.Interrupt && !s.capabilities(sess).Interrupt.Available { // never silently downgrade
		fail(w, 409, "interrupt_unsupported", interruptReason)
		return
	}
	c := control{RequestID: b.RequestID, Kind: agentsapi.ControlNudge, SessionID: sess.ID, State: "queued"}
	if b.Interrupt {
		res, err := s.Interrupter.Interrupt(r.Context(), sess.ID, b.Text)
		switch {
		case errors.Is(err, chatbridge.ErrNothingToInterrupt), errors.Is(err, chatbridge.ErrNotReplyable):
			fail(w, 409, "interrupt_unsupported", interruptReason)
			return
		case err != nil:
			msg := "Tether could not interrupt the session."
			c.State, c.Error = "failed", &msg
		default:
			c.State, c.Interrupt = "interrupted_then_delivered", &res
		}
		if err := s.saveControl(r.Context(), c, true); err != nil {
			internal(w)
			return
		}
		saved, _ := s.loadControl(r.Context(), c.RequestID)
		reply(w, 202, saved)
		return
	}
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
	if old, ok := s.loadControl(r.Context(), b.RequestID); ok { // a retry of the same request
		if old.SessionID != sess.ID || old.Kind != agentsapi.ControlRestart {
			fail(w, 409, "conflict", "That request_id belongs to another request.")
			return
		}
		reply(w, 202, old)
		return
	}
	if !s.capabilities(sess).Restart {
		fail(w, 409, "restart_unsupported", restartReason)
		return
	}
	c := control{RequestID: b.RequestID, Kind: agentsapi.ControlRestart, SessionID: sess.ID, State: "requested", Handoff: b.Handoff}
	if err := s.saveControl(r.Context(), c, true); err != nil {
		internal(w)
		return
	}
	skip := make(chan struct{})
	s.skips.Store(c.RequestID, skip)
	go s.runRestart(c, sess, skip)
	saved, _ := s.loadControl(r.Context(), c.RequestID)
	reply(w, 202, saved)
}

// runRestart walks requested -> [handoff_sent -> waiting] -> stopping -> stopped -> resuming -> done. A failure
// records the step it stopped at.
func (s *Server) runRestart(c control, sess Session, skip chan struct{}) {
	defer s.skips.Delete(c.RequestID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	set := func(state string) { c.State = state; _ = s.saveControl(ctx, c, false) }
	fail := func(step string, err error) {
		msg := "Tether could not complete the restart: " + step + "."
		c.Step, c.Error = &step, &msg
		_ = err
		set("failed")
	}
	if c.Handoff && s.Sender != nil && sess.Agent.URN != nil {
		if _, err := s.Sender.Send(ctx, *sess.Agent.URN, "Restart: write your handoff", "The operator is restarting this session. Write your handoff to Tesseract now.", map[string]string{"source": "tachyon-agents-restart", "request_id": c.RequestID}); err == nil {
			set("handoff_sent")
			end := s.now().Add(handoffWait).UnixMilli()
			c.WaitEndsAt = &end
			set("waiting")
			select {
			case <-skip:
			case <-time.After(handoffWait):
			case <-ctx.Done():
			}
		}
	}
	set("stopping")
	if err := s.Restarter.Stop(ctx, sess.ID); err != nil {
		fail("stopping", err)
		return
	}
	set("stopped")
	set("resuming")
	id, err := s.Restarter.Resume(ctx, *sess.Agent.LogicalAgentID, "tachyon-restart-"+c.RequestID)
	if err != nil {
		fail("resuming", err)
		return
	}
	c.NewSessionID = &id
	set("done")
}

func (s *Server) getControl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("request_id")
	if c, ok := s.loadControl(r.Context(), id); ok && idRE.MatchString(id) {
		reply(w, 200, c)
		return
	}
	fail(w, 404, "not_found", "No such control request.")
}

// skipControl ends the restart handoff wait early.
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
	if ch, ok := s.skips.LoadAndDelete(id); ok {
		close(ch.(chan struct{}))
	}
	reply(w, 202, c)
}

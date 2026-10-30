package chatterminal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrLimit    = errors.New("limit reached")
)

// Run states. The first three exist before a process starts and show on the proposal block.
const (
	StatePending  = "pending_approval"
	StateRunning  = "running"
	StateFinished = "finished"
	StateFailed   = "failed"
	StateStopped  = "stopped"
	StateDenied   = "denied"
	StateExpired  = "expired"
)

func terminalState(s string) bool {
	switch s {
	case StateFinished, StateFailed, StateStopped, StateDenied, StateExpired:
		return true
	}
	return false
}

// Approval records how a run came to be allowed.
type Approval struct {
	Mode      string  `json:"mode"` // user | policy
	ItemID    *string `json:"item_id"`
	RuleID    *string `json:"rule_id"`
	DecidedBy *string `json:"decided_by"`
	DecidedAt *int64  `json:"decided_at"`
}

// Run is the run record. Field names that actionrun's Run JSON also has (run_id, command, state, exit_code,
// output, requested_by, reason, op_digest, started_at, ended_at, duration_ms) keep its names.
type Run struct {
	RunID           string   `json:"run_id"`
	ThreadRef       string   `json:"thread_ref"`
	MachineID       string   `json:"machine_id"`
	Machine         string   `json:"machine"`
	SSHUser         string   `json:"ssh_user"`
	Command         string   `json:"command"`
	State           string   `json:"state"`
	RequestedBy     string   `json:"requested_by"`
	Reason          string   `json:"reason"`
	OpDigest        string   `json:"op_digest"`
	Approval        Approval `json:"approval"`
	ExitCode        *int     `json:"exit_code"`
	Output          string   `json:"output"`
	OutputRef       string   `json:"output_ref"`
	Truncated       bool     `json:"truncated"`
	CloseOnComplete bool     `json:"close_on_complete"`
	ClosedAt        *int64   `json:"closed_at"`
	StoppedBy       *string  `json:"stopped_by"`
	Error           string   `json:"error,omitempty"`
	MessageRef      string   `json:"message_ref,omitempty"`
	Supersedes      *string  `json:"supersedes"`
	StartedAt       *int64   `json:"started_at"`
	EndedAt         *int64   `json:"ended_at"`
	DurationMS      *int64   `json:"duration_ms"`
	CreatedAt       int64    `json:"created_at"`
}

// Proposal is the approval record of one run.
type Proposal struct {
	ID            string  `json:"id"`
	RunID         string  `json:"run_id"`
	ThreadRef     string  `json:"thread_ref"`
	SessionID     *string `json:"session_id"`
	MachineID     string  `json:"machine_id"`
	Command       string  `json:"command"`
	CommandDigest string  `json:"command_digest"`
	Reason        string  `json:"reason"`
	RequestedBy   string  `json:"requested_by"`
	State         string  `json:"state"`
	ItemID        *string `json:"item_id"`
	CreatedAt     int64   `json:"created_at"`
	ExpiresAt     int64   `json:"expires_at"`
	Supersedes    *string `json:"supersedes"`
}

// Session is one interactive pane. Only counts are kept, never content.
type SessionRow struct {
	ID        string  `json:"id"`
	ThreadRef *string `json:"thread_ref"`
	MachineID string  `json:"machine_id"`
	SSHUser   string  `json:"ssh_user"`
	State     string  `json:"state"`
	OpenedBy  string  `json:"opened_by"`
	OpenedAt  int64   `json:"opened_at"`
	EndedAt   *int64  `json:"ended_at"`
	BytesIn   int64   `json:"bytes_in,omitempty"`
	BytesOut  int64   `json:"bytes_out,omitempty"`
	WSPath    string  `json:"ws_path"`
}

type Store struct {
	DB  *localstore.Store
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Digest binds the approved bytes to what runs: machine, user and the exact command string.
func Digest(machine, user, command string) string {
	h := sha256.New()
	for _, p := range []string{"terminal.run/v1", machine, user, command} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

const runCols = `run_id,thread_ref,machine_id,ssh_user,command,op_digest,reason,requested_by,state,approval_mode,rule_id,decided_by,decided_ns,exit_code,output_tail,output_truncated,close_on_complete,closed_ns,stopped_by,error,COALESCE(message_ref,''),supersedes,started_ns,ended_ns,duration_ms,created_ns`

type rowScanner interface{ Scan(...any) error }

func ms(ns sql.NullInt64) *int64 {
	if !ns.Valid {
		return nil
	}
	v := ns.Int64 / int64(time.Millisecond)
	return &v
}

func str(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

func scanRun(row rowScanner) (Run, error) {
	var r Run
	var rule, by, stoppedBy, sup sql.NullString
	var decided, exit, closed, started, ended, dur sql.NullInt64
	var trunc, coc int
	var created int64
	if err := row.Scan(&r.RunID, &r.ThreadRef, &r.MachineID, &r.SSHUser, &r.Command, &r.OpDigest, &r.Reason, &r.RequestedBy, &r.State, &r.Approval.Mode,
		&rule, &by, &decided, &exit, &r.Output, &trunc, &coc, &closed, &stoppedBy, &r.Error, &r.MessageRef, &sup, &started, &ended, &dur, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	r.Approval.RuleID, r.Approval.DecidedBy, r.Approval.DecidedAt = str(rule), str(by), ms(decided)
	if exit.Valid {
		v := int(exit.Int64)
		r.ExitCode = &v
	}
	r.Truncated, r.CloseOnComplete = trunc != 0, coc != 0
	r.ClosedAt, r.StoppedBy, r.Supersedes = ms(closed), str(stoppedBy), str(sup)
	r.StartedAt, r.EndedAt = ms(started), ms(ended)
	if dur.Valid {
		r.DurationMS = &dur.Int64
	}
	r.CreatedAt = created / int64(time.Millisecond)
	r.Machine, r.OutputRef = r.MachineID, r.RunID
	return r, nil
}

func (s *Store) Run(ctx context.Context, id string) (Run, error) {
	return scanRun(s.DB.Reader.QueryRowContext(ctx, `SELECT `+runCols+` FROM terminal_runs WHERE run_id=?`, id))
}

// Runs lists runs newest first. closed: nil = any, true = closed only, false = not closed.
func (s *Store) Runs(ctx context.Context, thread, state string, closed *bool, limit int) ([]Run, error) {
	q := `SELECT ` + runCols + ` FROM terminal_runs WHERE 1=1`
	var args []any
	if thread != "" {
		q += ` AND thread_ref=?`
		args = append(args, thread)
	}
	if state != "" {
		q += ` AND state=?`
		args = append(args, state)
	}
	if closed != nil {
		if *closed {
			q += ` AND closed_ns IS NOT NULL`
		} else {
			q += ` AND closed_ns IS NULL`
		}
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q += ` ORDER BY created_ns DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NewRun is everything a proposal needs.
type NewRun struct {
	Thread, Machine, User, Command, Reason, RequestedBy string
	SessionID                                           string
	Mode                                                string // user | policy
	RuleID                                              string
	Supersedes                                          string // proposal id
	CloseOnComplete                                     bool
	TTL                                                 time.Duration
}

// Create inserts the run and its proposal in one transaction. A policy-allowed run starts out running.
func (s *Store) Create(ctx context.Context, n NewRun) (Run, Proposal, error) {
	now := s.now()
	runID, propID := "run_"+uuid.NewString(), "prop_"+uuid.NewString()
	digest := Digest(n.Machine, n.User, n.Command)
	state, propState := StatePending, "pending"
	var started any
	var decidedBy any
	var decided any
	if n.Mode == "policy" {
		state, propState = StateRunning, "approved"
		started, decided, decidedBy = now.UnixNano(), now.UnixNano(), "rule:"+n.RuleID
	}
	var oldRun sql.NullString
	err := s.DB.Writer.Submit(ctx, "terminal_create", func(ctx context.Context, tx *sql.Tx) error {
		if n.Supersedes != "" {
			res, err := tx.ExecContext(ctx, `UPDATE terminal_proposals SET state='superseded' WHERE id=? AND state='pending'`, n.Supersedes)
			if err != nil {
				return err
			}
			if c, _ := res.RowsAffected(); c == 0 {
				return ErrConflict
			}
			if err := tx.QueryRowContext(ctx, `SELECT run_id FROM terminal_proposals WHERE id=?`, n.Supersedes).Scan(&oldRun); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET state='denied', ended_ns=?, decided_by='operator', decided_ns=? WHERE run_id=? AND state='pending_approval'`, now.UnixNano(), now.UnixNano(), oldRun.String); err != nil {
				return err
			}
		}
		var sup any
		if n.Supersedes != "" {
			sup = n.Supersedes
		}
		var rule any
		if n.RuleID != "" {
			rule = n.RuleID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO terminal_runs(run_id,thread_ref,machine_id,ssh_user,command,op_digest,reason,requested_by,state,approval_mode,rule_id,decided_by,decided_ns,close_on_complete,supersedes,started_ns,created_ns)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			runID, n.Thread, n.Machine, n.User, n.Command, digest, n.Reason, n.RequestedBy, state, n.Mode, rule, decidedBy, decided, boolInt(n.CloseOnComplete), sup, started, now.UnixNano()); err != nil {
			return err
		}
		var sess any
		if n.SessionID != "" {
			sess = n.SessionID
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO terminal_proposals(id,run_id,thread_ref,session_id,machine_id,command,command_digest,reason,requested_by,state,created_ns,expires_ns,supersedes)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			propID, runID, n.Thread, sess, n.Machine, n.Command, digest, n.Reason, n.RequestedBy, propState, now.UnixNano(), now.Add(n.TTL).UnixNano(), sup)
		return err
	})
	if err != nil {
		return Run{}, Proposal{}, err
	}
	run, err := s.Run(ctx, runID)
	if err != nil {
		return Run{}, Proposal{}, err
	}
	p, err := s.Proposal(ctx, propID)
	return run, p, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const propCols = `id,run_id,thread_ref,session_id,machine_id,command,command_digest,reason,requested_by,state,item_id,created_ns,expires_ns,supersedes`

func scanProposal(row rowScanner) (Proposal, error) {
	var p Proposal
	var sess, item, sup sql.NullString
	var created, expires int64
	if err := row.Scan(&p.ID, &p.RunID, &p.ThreadRef, &sess, &p.MachineID, &p.Command, &p.CommandDigest, &p.Reason, &p.RequestedBy, &p.State, &item, &created, &expires, &sup); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Proposal{}, ErrNotFound
		}
		return Proposal{}, err
	}
	p.SessionID, p.ItemID, p.Supersedes = str(sess), str(item), str(sup)
	p.CreatedAt, p.ExpiresAt = created/int64(time.Millisecond), expires/int64(time.Millisecond)
	return p, nil
}

func (s *Store) Proposal(ctx context.Context, id string) (Proposal, error) {
	return scanProposal(s.DB.Reader.QueryRowContext(ctx, `SELECT `+propCols+` FROM terminal_proposals WHERE id=?`, id))
}

func (s *Store) Proposals(ctx context.Context, thread, state string) ([]Proposal, error) {
	q := `SELECT ` + propCols + ` FROM terminal_proposals WHERE 1=1`
	var args []any
	if thread != "" {
		q += ` AND thread_ref=?`
		args = append(args, thread)
	}
	if state != "" {
		q += ` AND state=?`
		args = append(args, state)
	}
	rows, err := s.DB.Reader.QueryContext(ctx, q+` ORDER BY created_ns DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Approve moves a pending, unexpired proposal to approved and its run to running, atomically. The digest
// is re-derived from the stored fields: a row whose bytes no longer hash to the approved digest is refused.
func (s *Store) Approve(ctx context.Context, id, who string) (Run, error) {
	now := s.now()
	var runID string
	expired := false
	err := s.DB.Writer.Submit(ctx, "terminal_approve", func(ctx context.Context, tx *sql.Tx) error {
		var p struct {
			run, machine, command, digest, state string
			expires                              int64
		}
		if err := tx.QueryRowContext(ctx, `SELECT run_id,machine_id,command,command_digest,state,expires_ns FROM terminal_proposals WHERE id=?`, id).
			Scan(&p.run, &p.machine, &p.command, &p.digest, &p.state, &p.expires); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if p.state != "pending" {
			return ErrConflict
		}
		if p.expires <= now.UnixNano() {
			_, _ = tx.ExecContext(ctx, `UPDATE terminal_proposals SET state='expired' WHERE id=?`, id)
			_, _ = tx.ExecContext(ctx, `UPDATE terminal_runs SET state='expired', ended_ns=? WHERE run_id=? AND state='pending_approval'`, now.UnixNano(), p.run)
			expired = true // commit the expiry, then refuse
			return nil
		}
		var user string
		if err := tx.QueryRowContext(ctx, `SELECT ssh_user FROM terminal_runs WHERE run_id=?`, p.run).Scan(&user); err != nil {
			return err
		}
		if Digest(p.machine, user, p.command) != p.digest {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE terminal_proposals SET state='approved' WHERE id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET state='running', decided_by=?, decided_ns=?, started_ns=? WHERE run_id=? AND state='pending_approval'`, who, now.UnixNano(), now.UnixNano(), p.run)
		runID = p.run
		return err
	})
	if err != nil {
		return Run{}, err
	}
	if expired {
		return Run{}, ErrConflict
	}
	return s.Run(ctx, runID)
}

// Deny ends a pending proposal and its run.
func (s *Store) Deny(ctx context.Context, id, who string) (Run, error) {
	now := s.now()
	var runID string
	err := s.DB.Writer.Submit(ctx, "terminal_deny", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE terminal_proposals SET state='denied' WHERE id=? AND state='pending'`, id)
		if err != nil {
			return err
		}
		if c, _ := res.RowsAffected(); c == 0 {
			var one int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM terminal_proposals WHERE id=?`, id).Scan(&one); err != nil {
				return ErrNotFound
			}
			return ErrConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT run_id FROM terminal_proposals WHERE id=?`, id).Scan(&runID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE terminal_runs SET state='denied', decided_by=?, decided_ns=?, ended_ns=? WHERE run_id=? AND state='pending_approval'`, who, now.UnixNano(), now.UnixNano(), runID)
		return err
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, runID)
}

// ExpireDue marks pending proposals past their TTL expired and returns the affected runs.
func (s *Store) ExpireDue(ctx context.Context) ([]Run, error) {
	now := s.now().UnixNano()
	var ids []string
	err := s.DB.Writer.Submit(ctx, "terminal_expire", func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT run_id FROM terminal_proposals WHERE state='pending' AND expires_ns<=?`, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx, `UPDATE terminal_proposals SET state='expired' WHERE state='pending' AND expires_ns<=?`, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE terminal_runs SET state='expired', ended_ns=? WHERE state='pending_approval' AND run_id IN (SELECT run_id FROM terminal_proposals WHERE state='expired')`, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	var out []Run
	for _, id := range ids {
		if r, err := s.Run(ctx, id); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// Finish records a run's end. A run already in a terminal state is left alone, so a Stop that races with
// the process exit keeps whichever was written first.
func (s *Store) Finish(ctx context.Context, id, state string, exit *int, tail string, truncated bool, errText string, started time.Time) (Run, error) {
	now := s.now()
	var code any
	if exit != nil {
		code = *exit
	}
	err := s.DB.Writer.Submit(ctx, "terminal_finish", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET state=?, exit_code=?, output_tail=?, output_truncated=?, error=?, ended_ns=?, duration_ms=?,
			closed_ns=CASE WHEN close_on_complete=1 THEN ? ELSE closed_ns END
			WHERE run_id=? AND state IN ('running')`,
			state, code, tail, boolInt(truncated), errText, now.UnixNano(), now.Sub(started).Milliseconds(), now.UnixNano(), id)
		return err
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, id)
}

// SetStopped notes who asked for the stop; it does not change the state (Finish does, when ssh exits).
func (s *Store) SetStopped(ctx context.Context, id, who string) error {
	return s.DB.Writer.Submit(ctx, "terminal_stopped_by", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET stopped_by=? WHERE run_id=? AND state='running' AND stopped_by IS NULL`, who, id)
		return err
	})
}

func (s *Store) SetMessage(ctx context.Context, runID, messageRef string) error {
	return s.DB.Writer.Submit(ctx, "terminal_message", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET message_ref=? WHERE run_id=?`, messageRef, runID)
		return err
	})
}

// Patch changes close_on_complete (any time) and closed (terminal states only: a running card cannot be
// closed away from its output).
func (s *Store) Patch(ctx context.Context, id string, closeOnComplete, closed *bool) (Run, error) {
	now := s.now()
	err := s.DB.Writer.Submit(ctx, "terminal_patch", func(ctx context.Context, tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM terminal_runs WHERE run_id=?`, id).Scan(&state); err != nil {
			return ErrNotFound
		}
		if closeOnComplete != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET close_on_complete=? WHERE run_id=?`, boolInt(*closeOnComplete), id); err != nil {
				return err
			}
		}
		if closed != nil {
			if *closed {
				if !terminalState(state) {
					return ErrConflict
				}
				_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET closed_ns=COALESCE(closed_ns,?) WHERE run_id=?`, now.UnixNano(), id)
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET closed_ns=NULL WHERE run_id=?`, id)
			return err
		}
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, id)
}

// --- interactive sessions ---

func (s *Store) CreateSession(ctx context.Context, thread, machine, user, by string, maxOpen int) (SessionRow, error) {
	now := s.now()
	id := "ts_" + uuid.NewString()
	var tref any
	if thread != "" {
		tref = thread
	}
	err := s.DB.Writer.Submit(ctx, "terminal_session_open", func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM terminal_sessions WHERE state<>'closed' AND opened_by=?`, by).Scan(&n); err != nil {
			return err
		}
		if n >= maxOpen {
			return ErrLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO terminal_sessions(id,thread_ref,machine_id,ssh_user,state,opened_by,opened_ns) VALUES(?,?,?,?,'open',?,?)`, id, tref, machine, user, by, now.UnixNano())
		return err
	})
	if err != nil {
		return SessionRow{}, err
	}
	return s.Session(ctx, id)
}

func (s *Store) Session(ctx context.Context, id string) (SessionRow, error) {
	var r SessionRow
	var tref sql.NullString
	var opened int64
	var ended sql.NullInt64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT id,thread_ref,machine_id,ssh_user,state,opened_by,opened_ns,ended_ns,bytes_in,bytes_out FROM terminal_sessions WHERE id=?`, id).
		Scan(&r.ID, &tref, &r.MachineID, &r.SSHUser, &r.State, &r.OpenedBy, &opened, &ended, &r.BytesIn, &r.BytesOut)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.ThreadRef, r.OpenedAt, r.EndedAt = str(tref), opened/int64(time.Millisecond), ms(ended)
	r.WSPath = SessionsPrefixPath + "/" + r.ID + "/ws"
	return r, nil
}

func (s *Store) EndSession(ctx context.Context, id string, in, out int64) error {
	now := s.now()
	return s.DB.Writer.Submit(ctx, "terminal_session_close", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE terminal_sessions SET state='closed', ended_ns=COALESCE(ended_ns,?), bytes_in=bytes_in+?, bytes_out=bytes_out+? WHERE id=?`, now.UnixNano(), in, out, id)
		return err
	})
}

// CloseStaleSessions closes sessions a restart left open; there is no process to reattach.
func (s *Store) CloseStaleSessions(ctx context.Context) error {
	now := s.now()
	return s.DB.Writer.Submit(ctx, "terminal_session_sweep", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE terminal_sessions SET state='closed', ended_ns=? WHERE state<>'closed'`, now.UnixNano()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET state='failed', error='Tachyon restarted while this ran.', ended_ns=? WHERE state='running'`, now.UnixNano())
		return err
	})
}

func cleanCommand(c string) (string, bool) {
	c = strings.TrimSpace(c)
	return c, c != "" && len(c) <= 4096 && !strings.ContainsRune(c, 0)
}

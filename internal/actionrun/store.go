package actionrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Run states. pending_approval and approved precede execution.
const (
	StatePending      = "pending_approval"
	StateWaitingInput = "waiting_input" // approved; an interactive run waits for the owner to attach
	StateApproved     = "approved"      // approved; starting
	StateRunning      = "running"
	StateSucceeded    = "succeeded"
	StateFailed       = "failed"
	StateRefused      = "refused" // digest mismatch or registry no longer allows it
	StateDenied       = "denied"
	StateExpired      = "expired"
)

func terminalState(s string) bool {
	switch s {
	case StateSucceeded, StateFailed, StateRefused, StateDenied, StateExpired:
		return true
	}
	return false
}

// Run is the persisted record of one requested action.
type Run struct {
	ID          string            `json:"run_id"`
	ItemID      string            `json:"item_id"`
	Machine     string            `json:"machine"`
	Action      string            `json:"action"`
	Params      map[string]string `json:"params"`
	Argv        []string          `json:"argv"`
	Command     string            `json:"command"`
	OpDigest    string            `json:"op_digest"`
	Class       string            `json:"class"`
	Labels      string            `json:"labels,omitempty"`
	RequestedBy string            `json:"requested_by"`
	Reason      string            `json:"reason,omitempty"`
	State       string            `json:"state"`
	Terminal    bool              `json:"terminal"`
	Interactive bool              `json:"interactive"`
	User        string            `json:"user,omitempty"`
	ExitCode    *int              `json:"exit_code,omitempty"`
	Output      string            `json:"output,omitempty"`
	Error       string            `json:"error,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	EndedAt     *time.Time        `json:"ended_at,omitempty"`
}

// Store persists runs through Tachyon's one SQLite writer.
type Store struct{ DB *localstore.Store }

const runColumns = "id,item_id,machine,action,params,argv,op_digest,class,labels,requested_by,reason,state,run_user,exit_code,output,error,created_ns,started_ns,ended_ns"

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var params, argv string
	var exit, started, ended sql.NullInt64
	var created int64
	if err := row.Scan(&r.ID, &r.ItemID, &r.Machine, &r.Action, &params, &argv, &r.OpDigest, &r.Class, &r.Labels, &r.RequestedBy, &r.Reason, &r.State, &r.User, &exit, &r.Output, &r.Error, &created, &started, &ended); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	if err := json.Unmarshal([]byte(params), &r.Params); err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal([]byte(argv), &r.Argv); err != nil {
		return Run{}, err
	}
	spec := Spec{Argv: r.Argv}
	r.Command = spec.Command()
	if act, ok := ActionByID(r.Action); ok {
		r.Interactive = act.Interactive
	}
	r.Terminal = terminalState(r.State)
	r.CreatedAt = time.Unix(0, created).UTC()
	if exit.Valid {
		v := int(exit.Int64)
		r.ExitCode = &v
	}
	if started.Valid {
		t := time.Unix(0, started.Int64).UTC()
		r.StartedAt = &t
	}
	if ended.Valid {
		t := time.Unix(0, ended.Int64).UTC()
		r.EndedAt = &t
	}
	return r, nil
}

var ErrNotFound = errors.New("action run not found")

func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	return scanRun(s.DB.Reader.QueryRowContext(ctx, "SELECT "+runColumns+" FROM action_runs WHERE id=?", id))
}

func (s *Store) ByItem(ctx context.Context, itemID string) (Run, error) {
	return scanRun(s.DB.Reader.QueryRowContext(ctx, "SELECT "+runColumns+" FROM action_runs WHERE item_id=?", itemID))
}

func (s *Store) Insert(ctx context.Context, r Run) error {
	params, _ := json.Marshal(r.Params)
	argv, _ := json.Marshal(r.Argv)
	return s.DB.Writer.Submit(ctx, "action_run_insert", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO action_runs("+runColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?,?,?,NULL,NULL)",
			r.ID, r.ItemID, r.Machine, r.Action, string(params), string(argv), r.OpDigest, r.Class, r.Labels, r.RequestedBy, r.Reason, r.State, r.User, "", "", r.CreatedAt.UnixNano())
		return err
	})
}

// Transition moves a run from one state to another only if it is still in from;
// it reports whether it won the transition.
func (s *Store) Transition(ctx context.Context, id, from, to string, mutate func(*sql.Tx) error) (bool, error) {
	won := false
	err := s.DB.Writer.Submit(ctx, "action_run_transition", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE action_runs SET state=? WHERE id=? AND state=?", to, id, from)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		won = n == 1
		if won && mutate != nil {
			return mutate(tx)
		}
		return nil
	})
	return won, err
}

func (s *Store) Started(ctx context.Context, id, user string, at time.Time) error {
	return s.DB.Writer.Submit(ctx, "action_run_started", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE action_runs SET run_user=?,started_ns=? WHERE id=?", user, at.UnixNano(), id)
		return err
	})
}

// Finish records the terminal result.
func (s *Store) Finish(ctx context.Context, id, from, state string, exit *int, output, errText string, at time.Time) error {
	_, err := s.Transition(ctx, id, from, state, func(tx *sql.Tx) error {
		var code any
		if exit != nil {
			code = *exit
		}
		_, err := tx.ExecContext(ctx, "UPDATE action_runs SET exit_code=?,output=?,error=?,ended_ns=? WHERE id=?", code, output, errText, at.UnixNano(), id)
		return err
	})
	return err
}

// Open lists runs that have not reached a terminal state.
func (s *Store) Open(ctx context.Context) ([]Run, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT "+runColumns+" FROM action_runs WHERE state IN (?,?,?,?) ORDER BY created_ns", StatePending, StateWaitingInput, StateApproved, StateRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

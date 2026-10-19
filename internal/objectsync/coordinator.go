package objectsync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

const runTimeout = 2 * time.Minute
const leaseDuration = 3 * time.Minute
const stamp = "2006-01-02T15:04:05.000000000Z"

var ErrBusy = errors.New("source sync already running")

type State struct {
	SourceID      string              `json:"source_id"`
	Scope         objects.SourceScope `json:"scope"`
	RunID         string              `json:"run_id"`
	Phase         string              `json:"phase"`
	StartedAt     string              `json:"started_at"`
	FinishedAt    string              `json:"finished_at"`
	LastSuccessAt string              `json:"last_success_at"`
	Progress
	Authoritative bool   `json:"authoritative"`
	Problem       string `json:"problem,omitempty"`
}

type Coordinator struct {
	store   *localstore.Store
	objects *objects.Service
}

func New(store *localstore.Store) *Coordinator {
	return &Coordinator{store: store, objects: objects.New(store)}
}

func (c *Coordinator) State(ctx context.Context, scope objects.SourceScope) (State, error) {
	s := State{SourceID: sourceID(scope), Scope: scope}
	err := c.store.Reader.QueryRowContext(ctx, `SELECT run_id,phase,started_at,finished_at,last_success_at,pages,items,checkpoint,authoritative,problem FROM object_sync_state WHERE source_id=?`, s.SourceID).Scan(&s.RunID, &s.Phase, &s.StartedAt, &s.FinishedAt, &s.LastSuccessAt, &s.Pages, &s.Items, &s.Checkpoint, &s.Authoritative, &s.Problem)
	if errors.Is(err, sql.ErrNoRows) {
		s.Phase = "never_run"
		err = nil
	}
	return s, err
}

func (c *Coordinator) begin(ctx context.Context, source Source, runID, fireID string, at time.Time) error {
	id := sourceID(source.Scope())
	scope, _ := json.Marshal(source.Scope())
	return c.store.Writer.Submit(ctx, "object_sync_begin", func(ctx context.Context, tx *sql.Tx) error {
		if fireID != "" {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM object_sync_runs WHERE source_id=? AND fire_id=? AND phase='succeeded'`, id, fireID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return scheduler.ErrDuplicateJob
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO object_sync_state(source_id,scope_json) VALUES(?,?) ON CONFLICT DO NOTHING`, id, string(scope)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE object_sync_state SET run_id=?,lease_until=?,phase='scanning',started_at=?,finished_at='',pages=0,items=0,checkpoint='',authoritative=0,problem='' WHERE source_id=? AND (lease_until='' OR lease_until<=?)`, runID, at.Add(leaseDuration).Format(stamp), at.Format(stamp), id, at.Format(stamp))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBusy
		}
		// A dead process keeps its failed attempt in history, and the new scan
		// starts at page one. No partially staged inventory is ever reconciled.
		if _, err := tx.ExecContext(ctx, `UPDATE object_sync_runs SET phase='interrupted',finished_at=?,problem='sync lease expired' WHERE source_id=? AND phase IN ('scanning','applying')`, at.Format(stamp), id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO object_sync_runs(id,source_id,fire_id,started_at,phase) VALUES(?,?,?,?,'scanning')`, runID, id, fireID, at.Format(stamp))
		return err
	})
}

func (c *Coordinator) progress(ctx context.Context, id, runID, phase string, p Progress, authoritative bool) error {
	return c.store.Writer.Submit(ctx, "object_sync_progress", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE object_sync_state SET phase=?,pages=?,items=?,checkpoint=?,authoritative=? WHERE source_id=? AND run_id=?`, phase, p.Pages, p.Items, p.Checkpoint, authoritative, id, runID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBusy
		}
		_, err = tx.ExecContext(ctx, `UPDATE object_sync_runs SET phase=?,pages=?,items=?,authoritative=? WHERE id=?`, phase, p.Pages, p.Items, authoritative, runID)
		return err
	})
}
func (c *Coordinator) finish(id, runID string, success bool, problem string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	phase := "failed"
	if success {
		phase = "succeeded"
	}
	now := time.Now().UTC().Format(stamp)
	return c.store.Writer.Submit(ctx, "object_sync_finish", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE object_sync_state SET phase=?,finished_at=?,lease_until='',problem=?,last_success_at=CASE WHEN ? THEN ? ELSE last_success_at END WHERE source_id=? AND run_id=?`, phase, now, problem, success, now, id, runID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBusy
		}
		_, err = tx.ExecContext(ctx, `UPDATE object_sync_runs SET phase=?,finished_at=?,problem=? WHERE id=?`, phase, now, problem, runID)
		return err
	})
}

// Run gathers a complete scan before applying any records. Progress checkpoints
// are diagnostic, not resumable page cursors: a retry starts a fresh inventory.
// Object application is atomic; a crash before finish can repeat an idempotent
// reconciliation, but can never mark a failed partial fetch as a good snapshot.
func (c *Coordinator) Run(ctx context.Context, source Source, fireID string) (out State, err error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	at := time.Now().UTC()
	runID := uuid.NewString()
	id := sourceID(source.Scope())
	if err = c.begin(ctx, source, runID, fireID, at); err != nil {
		return out, err
	}
	var p Progress
	success := false
	problem := "sync interrupted"
	defer func() {
		finishErr := c.finish(id, runID, success, problem)
		err = errors.Join(err, finishErr)
		readCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var stateErr error
		out, stateErr = c.State(readCtx, source.Scope())
		err = errors.Join(err, stateErr)
	}()
	snapshot, scanErr := source.Scan(ctx, at, func(next Progress) error {
		p = next
		return c.progress(ctx, id, runID, "scanning", p, false)
	})
	if scanErr != nil {
		// Built-in sources emit sanitized diagnostics; never persist arbitrary
		// adapter/database errors, whose text can contain provider data.
		problem = "provider scan failed"
		var safe *scanFailure
		if errors.As(scanErr, &safe) {
			problem = safe.Error()
		}
		if errors.Is(scanErr, context.DeadlineExceeded) {
			problem = "provider scan timed out"
		}
		if errors.Is(scanErr, context.Canceled) {
			problem = "provider scan cancelled"
		}
		return out, errors.New(problem)
	}
	if err = c.progress(ctx, id, runID, "applying", p, snapshot.Authoritative); err != nil {
		return out, err
	}
	if resolver, ok := source.(RelationshipResolver); ok {
		snapshot, err = resolver.Resolve(ctx, snapshot, c.objects)
		if err != nil {
			problem = "source relationship resolution failed"
			return out, errors.New(problem)
		}
	}
	if _, err = c.objects.Reconcile(ctx, source.Scope(), at, snapshot.Observations, snapshot.Authoritative, "sync:"+source.Name()); err != nil {
		problem = "object reconciliation failed"
		// No raw local/provider definition is included in job diagnostics.
		return out, errors.New(problem)
	}
	success, problem = true, ""
	return out, nil
}

// scanFailure only contains controlled messages from the built-in HTTP sources.
type scanFailure struct{ message string }

func (e *scanFailure) Error() string { return e.message }
func scanError(message string) error { return &scanFailure{message} }

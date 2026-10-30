package agentscollector

import (
	"context"
	"database/sql"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Retention: 7 days of events, 30 days of gaps and ended sessions (design E6, pending confirmation).
const (
	EventRetention   = 7 * 24 * time.Hour
	GapRetention     = 30 * 24 * time.Hour
	SessionRetention = 30 * 24 * time.Hour
	// Minute buckets are kept 48 h; hour buckets 30 d (the downsampled history).
	MinuteSampleRetention = 48 * time.Hour
	HourSampleRetention   = 30 * 24 * time.Hour
	pruneBatch            = 1000
)

type Store struct {
	DB  *localstore.Store
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// State is the cursor and bookkeeping row.
type State struct {
	Env           string
	LastSeq       int64
	LastEventAt   time.Time
	Strategy      string
	RetentionLast time.Time
	Pruned        int64
}

func (s *Store) State(ctx context.Context) (State, error) {
	var st State
	var last, ret int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT env_id,last_seq,last_event_at_ns,strategy,retention_last_ns,retention_pruned FROM agents_collector_state WHERE id=1`).
		Scan(&st.Env, &st.LastSeq, &last, &st.Strategy, &ret, &st.Pruned)
	if last > 0 {
		st.LastEventAt = time.Unix(0, last).UTC()
	}
	if ret > 0 {
		st.RetentionLast = time.Unix(0, ret).UTC()
	}
	return st, err
}

// SetStrategy records which bootstrap path is in use and resets the epoch counter.
func (s *Store) SetStrategy(ctx context.Context, strategy string) error {
	return s.DB.Writer.Submit(ctx, "agents_strategy", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents_collector_state SET strategy=?, started_ns=?, epoch=epoch+1 WHERE id=1`, strategy, s.now().UnixNano())
		return err
	})
}

// Seed moves the cursor forward without a frame (a snapshot's high-water mark).
func (s *Store) Seed(ctx context.Context, env string, seq int64) error {
	return s.DB.Writer.Submit(ctx, "agents_seed", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents_collector_state SET env_id=?, last_seq=MAX(last_seq,?) WHERE id=1`, env, seq)
		return err
	})
}

func b2i(p *bool) any {
	if p == nil {
		return nil
	}
	if *p {
		return 1
	}
	return 0
}

// Apply stores a batch in one writer transaction: the events, the session facts they carry, and the
// cursor. A replayed frame (same event id) changes nothing, so a restart or backfill is idempotent.
func (s *Store) Apply(ctx context.Context, frames []Frame) error {
	if len(frames) == 0 {
		return nil
	}
	return s.DB.Writer.Submit(ctx, "agents_apply", func(ctx context.Context, tx *sql.Tx) error {
		now := s.now().UnixNano()
		var maxSeq, lastAt int64
		for _, f := range frames {
			at := f.At.UnixNano()
			if f.At.IsZero() {
				at = now
				if f.Kind == "session.snapshot" {
					at = 0 // a listing without a timestamp says nothing about when the session was last active
				}
			}
			if f.Seq > maxSeq {
				maxSeq = f.Seq
			}
			if at > lastAt && f.Kind != "session.snapshot" {
				lastAt = at
			}
			if f.SessionID == "" || f.App != "" || f.Gap || f.Synchronized {
				continue // bus events are A4's; gap and sync frames carry no session
			}
			if typ := Type(f.Kind); typ != "" && f.EventID != "" {
				res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agents_events(session_id,tether_seq,tether_event_id,type,at_ns,actor,summary) VALUES(?,?,?,?,?,?,?)`,
					f.SessionID, f.Seq, f.EventID, typ, at, "tether", f.Summary())
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					continue // already seen
				}
			}
			if err := upsertSession(ctx, tx, f, at, now); err != nil {
				return err
			}
			if err := sample(ctx, tx, f, at); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE agents_collector_state SET last_seq=MAX(last_seq,?), last_event_at_ns=MAX(last_event_at_ns,?) WHERE id=1`, maxSeq, lastAt)
		return err
	})
}

// sample counts one event into the minute and hour buckets, and adds a turn's tokens.
func sample(ctx context.Context, tx *sql.Tx, f Frame, at int64) error {
	if f.Kind == "session.snapshot" || (Type(f.Kind) == "" && f.Usage == nil) {
		return nil
	}
	var in, out, cr, cw, calls int64
	if f.Usage != nil {
		in, out, cr, cw = f.Usage[0], f.Usage[1], f.Usage[2], f.Usage[3]
		if _, err := tx.ExecContext(ctx, `UPDATE agents_sessions SET tok_in=COALESCE(tok_in,0)+?, tok_out=COALESCE(tok_out,0)+?, tok_cr=COALESCE(tok_cr,0)+?, tok_cw=COALESCE(tok_cw,0)+? WHERE session_id=?`, in, out, cr, cw, f.SessionID); err != nil {
			return err
		}
	}
	if f.Kind == "tool.call.finished" {
		calls = 1
	}
	act := int64(0)
	if Type(f.Kind) != "" {
		act = 1
	}
	t := time.Unix(0, at).UTC()
	for _, b := range []struct {
		kind string
		ns   int64
	}{{"min", t.Truncate(time.Minute).UnixNano()}, {"hour", t.Truncate(time.Hour).UnixNano()}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents_samples(session_id,bucket_ns,bucket_kind,activity,tok_in,tok_out,tok_cr,tok_cw,tool_calls) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(session_id,bucket_kind,bucket_ns) DO UPDATE SET activity=activity+excluded.activity, tok_in=tok_in+excluded.tok_in, tok_out=tok_out+excluded.tok_out, tok_cr=tok_cr+excluded.tok_cr, tok_cw=tok_cw+excluded.tok_cw, tool_calls=tool_calls+excluded.tool_calls`,
			f.SessionID, b.ns, b.kind, act, in, out, cr, cw, calls); err != nil {
			return err
		}
	}
	return nil
}

func upsertSession(ctx context.Context, tx *sql.Tx, f Frame, at, now int64) error {
	tool := 0
	if Type(f.Kind) == "tool" && f.Kind == "tool.call.finished" {
		tool = 1
	}
	work := f.WorkState
	if f.Kind != "session.work_state_changed" {
		work = ""
	}
	var ended int64
	var reason string
	switch f.SessionState {
	case "ended", "stopped", "exited", "failed", "orphaned":
		ended, reason = at, f.SessionState
		if ended == 0 {
			ended = now
		}
		if f.Reason != "" {
			reason = f.Reason
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO agents_sessions(session_id,session_state,work_state,work_known,pending_approval,pending_question,provider,model,workroot,task_ref,started_ns,last_activity_ns,ended_ns,end_reason,tool_calls,updated_ns)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(session_id) DO UPDATE SET
  session_state=CASE WHEN excluded.session_state<>'' THEN excluded.session_state ELSE session_state END,
  work_state=CASE WHEN excluded.work_state<>'' THEN excluded.work_state ELSE work_state END,
  work_known=CASE WHEN excluded.work_state<>'' THEN 1 ELSE work_known END,
  pending_approval=COALESCE(excluded.pending_approval,pending_approval),
  pending_question=COALESCE(excluded.pending_question,pending_question),
  provider=CASE WHEN excluded.provider<>'' THEN excluded.provider ELSE provider END,
  model=CASE WHEN excluded.model<>'' THEN excluded.model ELSE model END,
  workroot=CASE WHEN excluded.workroot<>'' THEN excluded.workroot ELSE workroot END,
  task_ref=CASE WHEN excluded.task_ref<>'' THEN excluded.task_ref ELSE task_ref END,
  started_ns=CASE WHEN excluded.started_ns=0 THEN started_ns WHEN started_ns=0 THEN excluded.started_ns ELSE MIN(started_ns,excluded.started_ns) END,
  last_activity_ns=MAX(last_activity_ns,excluded.last_activity_ns),
  ended_ns=CASE WHEN excluded.ended_ns>0 THEN excluded.ended_ns ELSE ended_ns END,
  end_reason=CASE WHEN excluded.ended_ns>0 THEN excluded.end_reason ELSE end_reason END,
  tool_calls=tool_calls+excluded.tool_calls,
  updated_ns=excluded.updated_ns`,
		f.SessionID, f.SessionState, work, work != "", b2i(f.PendingApproval), b2i(f.PendingQuestion), f.Provider, f.Model, f.Workroot, f.TaskRef,
		at, at, ended, reason, tool, now)
	return err
}

// Touch records activity for a session without storing a row (session.output is counted this way,
// so a busy session is not marked stale while its output is being discarded).
func (s *Store) Touch(ctx context.Context, sessions map[string]time.Time, seq int64) error {
	if len(sessions) == 0 && seq == 0 {
		return nil
	}
	return s.DB.Writer.Submit(ctx, "agents_touch", func(ctx context.Context, tx *sql.Tx) error {
		now := s.now().UnixNano()
		for id, at := range sessions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO agents_sessions(session_id,started_ns,last_activity_ns,updated_ns) VALUES(?,?,?,?)
 ON CONFLICT(session_id) DO UPDATE SET last_activity_ns=MAX(last_activity_ns,excluded.last_activity_ns), updated_ns=excluded.updated_ns`, id, at.UnixNano(), at.UnixNano(), now); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE agents_collector_state SET last_seq=MAX(last_seq,?) WHERE id=1`, seq)
		return err
	})
}

// Gap is a recorded hole in collection. Nothing silently fills one.
type Gap struct {
	Start, End     time.Time
	Reason         string
	FromSeq, ToSeq int64
	Backfilled     bool
}

func (s *Store) AddGap(ctx context.Context, g Gap) (int64, error) {
	var id int64
	err := s.DB.Writer.Submit(ctx, "agents_gap", func(ctx context.Context, tx *sql.Tx) error {
		// A retried pass must not record the same hole twice.
		if g.FromSeq > 0 {
			if err := tx.QueryRowContext(ctx, `SELECT id FROM agents_gaps WHERE backfilled=0 AND from_seq=? AND reason=?`, g.FromSeq, g.Reason).Scan(&id); err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE agents_gaps SET end_ns=? WHERE id=?`, g.End.UnixNano(), id)
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO agents_gaps(start_ns,end_ns,reason,from_seq,to_seq,backfilled) VALUES(?,?,?,?,?,?)`,
			g.Start.UnixNano(), g.End.UnixNano(), g.Reason, g.FromSeq, g.ToSeq, g.Backfilled)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

func (s *Store) MarkBackfilled(ctx context.Context, id int64) error {
	return s.DB.Writer.Submit(ctx, "agents_gap_backfilled", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents_gaps SET backfilled=1 WHERE id=?`, id)
		return err
	})
}

// OpenGaps counts gaps that were not filled.
func (s *Store) OpenGaps(ctx context.Context) (n int, err error) {
	err = s.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents_gaps WHERE backfilled=0`).Scan(&n)
	return
}

// SessionCount counts sessions that have not ended.
func (s *Store) SessionCount(ctx context.Context) (n int, err error) {
	err = s.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents_sessions WHERE ended_ns=0`).Scan(&n)
	return
}

// Prune deletes expired rows in batches of 1000, one writer transaction each, and records a receipt.
func (s *Store) Prune(ctx context.Context) (int64, error) {
	now := s.now()
	var total int64
	for _, job := range []struct {
		sql string
		cut time.Time
	}{
		{`DELETE FROM agents_events WHERE id IN (SELECT id FROM agents_events WHERE at_ns<? LIMIT ?)`, now.Add(-EventRetention)},
		{`DELETE FROM agents_gaps WHERE id IN (SELECT id FROM agents_gaps WHERE end_ns<? LIMIT ?)`, now.Add(-GapRetention)},
		{`DELETE FROM agents_samples WHERE rowid IN (SELECT rowid FROM agents_samples WHERE bucket_kind='min' AND bucket_ns<? LIMIT ?)`, now.Add(-MinuteSampleRetention)},
		{`DELETE FROM agents_samples WHERE rowid IN (SELECT rowid FROM agents_samples WHERE bucket_kind='hour' AND bucket_ns<? LIMIT ?)`, now.Add(-HourSampleRetention)},
		{`DELETE FROM agents_sessions WHERE session_id IN (SELECT session_id FROM agents_sessions WHERE ended_ns>0 AND ended_ns<? LIMIT ?)`, now.Add(-SessionRetention)},
	} {
		for {
			var n int64
			err := s.DB.Writer.Submit(ctx, "agents_prune", func(ctx context.Context, tx *sql.Tx) error {
				res, err := tx.ExecContext(ctx, job.sql, job.cut.UnixNano(), pruneBatch)
				if err != nil {
					return err
				}
				n, _ = res.RowsAffected()
				return nil
			})
			if err != nil {
				return total, err
			}
			total += n
			if n < pruneBatch {
				break
			}
		}
	}
	err := s.DB.Writer.Submit(ctx, "agents_prune_receipt", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents_collector_state SET retention_last_ns=?, retention_pruned=? WHERE id=1`, now.UnixNano(), total)
		return err
	})
	return total, err
}

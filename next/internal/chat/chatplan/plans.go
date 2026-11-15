package chatplan

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// MaxPlans bounds the plans one thread may hold.
const MaxPlans = 20

// PlansForThread returns every plan the thread holds, in order (position, then creation).
func (s *Store) PlansForThread(ctx context.Context, thread string) ([]Plan, error) {
	return s.plansOf(ctx, s.DB.Reader, thread)
}

func (s *Store) plansOf(ctx context.Context, q querier, thread string) ([]Plan, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+planColumns+` FROM chat_plans WHERE scope='thread' AND thread_ref=? ORDER BY position, created_ns`, thread)
	if err != nil {
		return nil, err
	}
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // before loadSteps: one connection may not serve two open cursors
	for i := range out {
		if err := loadSteps(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []Plan{}
	}
	return out, nil
}

// CreatePlan adds an empty plan after the thread's others.
func (s *Store) CreatePlan(ctx context.Context, thread string, title *string, by string) (Plan, error) {
	if thread == "" || (by != "user" && by != "agent") {
		return Plan{}, ErrInvalid
	}
	t, ok := clean(title, MaxTitle)
	if !ok {
		return Plan{}, ErrInvalid
	}
	now := s.now()
	var out Plan
	err := s.DB.Writer.Submit(ctx, "chat_plan_create", func(ctx context.Context, tx store.Tx) error {
		var n, maxPos int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(position),0) FROM chat_plans WHERE scope='thread' AND thread_ref=?`, thread).Scan(&n, &maxPos); err != nil {
			return err
		}
		if n >= MaxPlans {
			return ErrInvalid
		}
		p := Plan{ID: "pl_" + uuid.NewString(), Scope: "thread", ThreadRef: &thread, Status: "proposed", Revision: 1, Position: maxPos + 1, CreatedBy: by, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(), Steps: []Step{}}
		if t != "" {
			p.Title = &t
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_plans(`+planColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ID, p.Scope, p.ThreadRef, p.ProjectID, p.Title, p.Status, p.Revision, p.TaskRef, p.CreatedBy, now.UnixNano(), now.UnixNano(), p.Position); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

// DeletePlan removes one of the thread's plans. The revision must match, and a plan holding steps linked to Torque
// tasks is refused (ErrLinked): move or unlink them first.
func (s *Store) DeletePlan(ctx context.Context, thread, planID string, expected int64) error {
	return s.DB.Writer.Submit(ctx, "chat_plan_delete", func(ctx context.Context, tx store.Tx) error {
		p, err := s.getArgs(ctx, tx, "id=? AND scope='thread' AND thread_ref=?", planID, thread)
		if err != nil {
			return err
		}
		if p.Revision != expected {
			return ErrConflict
		}
		for _, st := range p.Steps {
			if st.TaskRef != nil {
				return ErrLinked
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plan_steps WHERE plan_id=?`, p.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plans WHERE id=?`, p.ID); err != nil {
			return err
		}
		return renumberPlans(ctx, tx, s, thread)
	})
}

// renumberPlans makes the thread's positions 1..n in their current order.
func renumberPlans(ctx context.Context, tx store.Tx, s *Store, thread string) error {
	plans, err := s.plansOf(ctx, tx, thread)
	if err != nil {
		return err
	}
	for i, p := range plans {
		if p.Position != i+1 {
			if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET position=? WHERE id=?`, i+1, p.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReorderPlans sets the thread's plan order. ids must name every plan the thread holds, once each. Plan revisions do
// not change: the order belongs to the thread.
func (s *Store) ReorderPlans(ctx context.Context, thread string, ids []string) ([]Plan, error) {
	var out []Plan
	err := s.DB.Writer.Submit(ctx, "chat_plan_reorder_plans", func(ctx context.Context, tx store.Tx) error {
		plans, err := s.plansOf(ctx, tx, thread)
		if err != nil {
			return err
		}
		if len(ids) != len(plans) {
			return ErrInvalid
		}
		by := map[string]Plan{}
		for _, p := range plans {
			by[p.ID] = p
		}
		next := make([]Plan, 0, len(ids))
		for i, id := range ids {
			p, ok := by[id]
			if !ok {
				return ErrInvalid
			}
			delete(by, id)
			p.Position = i + 1
			if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET position=? WHERE id=?`, p.Position, p.ID); err != nil {
				return err
			}
			next = append(next, p)
		}
		out = next
		return nil
	})
	return out, err
}

// MoveStep moves a step from one of the thread's plans to another, keeping its id, status and Torque link. Both plan
// revisions must match what the writer saw (ErrConflict otherwise) and both advance by one. pos is the 1-based place
// in the target (0 or past the end appends). Moving within one plan is Reorder's job.
func (s *Store) MoveStep(ctx context.Context, thread, fromID, toID, stepID string, pos int, expFrom, expTo int64) (from, to Plan, st Step, err error) {
	if fromID == toID || pos < 0 {
		return from, to, st, ErrInvalid
	}
	now := s.now()
	err = s.DB.Writer.Submit(ctx, "chat_plan_move_step", func(ctx context.Context, tx store.Tx) error {
		src, err := s.getArgs(ctx, tx, "id=? AND scope='thread' AND thread_ref=?", fromID, thread)
		if err != nil {
			return err
		}
		dst, err := s.getArgs(ctx, tx, "id=? AND scope='thread' AND thread_ref=?", toID, thread)
		if err != nil {
			return err
		}
		if src.Revision != expFrom || dst.Revision != expTo {
			return ErrConflict
		}
		if len(dst.Steps) >= MaxSteps {
			return ErrInvalid
		}
		idx := -1
		for i, x := range src.Steps {
			if x.ID == stepID {
				idx = i
			}
		}
		if idx < 0 {
			return ErrNotFound
		}
		st = src.Steps[idx]
		src.Steps = append(src.Steps[:idx:idx], src.Steps[idx+1:]...)
		at := len(dst.Steps)
		if pos > 0 && pos-1 < at {
			at = pos - 1
		}
		st.UpdatedAt = now.UnixMilli()
		dst.Steps = append(dst.Steps[:at:at], append([]Step{st}, dst.Steps[at:]...)...)
		for _, p := range []*Plan{&src, &dst} {
			p.Revision++
			p.UpdatedAt = now.UnixMilli()
			if err := saveSteps(ctx, tx, p, now); err != nil {
				return err
			}
		}
		from, to = src, dst
		for _, x := range dst.Steps {
			if x.ID == stepID {
				st = x
			}
		}
		return nil
	})
	return from, to, st, err
}

// saveSteps writes the plan's revision and rewrites its steps in order.
func saveSteps(ctx context.Context, tx store.Tx, p *Plan, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET revision=?,updated_ns=? WHERE id=?`, p.Revision, now.UnixNano(), p.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plan_steps WHERE plan_id=?`, p.ID); err != nil {
		return err
	}
	for i := range p.Steps {
		st := &p.Steps[i]
		st.Position = i + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_plan_steps(plan_id,id,position,title,status,acceptance,task_ref,notes,updated_ns) VALUES(?,?,?,?,?,?,?,?,?)`,
			p.ID, st.ID, st.Position, st.Title, st.Status, st.Acceptance, st.TaskRef, st.Notes, st.UpdatedAt*1e6); err != nil {
			return err
		}
	}
	return nil
}

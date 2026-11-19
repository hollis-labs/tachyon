package chatplan

import (
	"context"
	"database/sql"
)

// TaskStatuses reads the current Torque status of the given task ids. A missing id means
// the task is unknown and its step is left alone.
type TaskStatuses func(ctx context.Context, taskRefs []string) (map[string]string, error)

// StepStatusFromTorque maps a Torque task status to a step status; ok is false for an unknown one.
func StepStatusFromTorque(status string) (string, bool) {
	switch status {
	case "backlog", "todo":
		return "todo", true
	case "doing", "review":
		return "doing", true
	case "blocked", "paused":
		return "blocked", true
	case "done":
		return "done", true
	case "abandoned", "cancelled", "archived":
		return "cancelled", true
	}
	return "", false
}

// SyncTaskStatuses copies Torque task status onto the linked steps. It is one-way: nothing
// is ever written to Torque. It returns the ids of the plans it changed (each bumped one
// revision). Nothing calls it on a timer yet (phase 2: a collector over torqueread).
func (s *Store) SyncTaskStatuses(ctx context.Context, src TaskStatuses) ([]string, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT DISTINCT st.task_ref FROM chat_plan_steps st JOIN chat_plans p ON p.id=st.plan_id WHERE st.task_ref IS NOT NULL AND p.status<>'cancelled'`)
	if err != nil {
		return nil, err
	}
	var refs []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	if len(refs) == 0 || src == nil {
		return nil, rows.Err()
	}
	statuses, err := src(ctx, refs)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var changed []string
	err = s.DB.Writer.Submit(ctx, "chat_plan_sync", func(ctx context.Context, tx *sql.Tx) error {
		touched := map[string]bool{}
		for ref, ts := range statuses {
			to, ok := StepStatusFromTorque(ts)
			if !ok {
				continue
			}
			res, err := tx.QueryContext(ctx, `SELECT plan_id,id FROM chat_plan_steps WHERE task_ref=? AND status<>?`, ref, to)
			if err != nil {
				return err
			}
			type key struct{ plan, step string }
			var ks []key
			for res.Next() {
				var k key
				if err := res.Scan(&k.plan, &k.step); err != nil {
					res.Close()
					return err
				}
				ks = append(ks, k)
			}
			res.Close()
			for _, k := range ks {
				if _, err := tx.ExecContext(ctx, `UPDATE chat_plan_steps SET status=?,updated_ns=? WHERE plan_id=? AND id=?`, to, now.UnixNano(), k.plan, k.step); err != nil {
					return err
				}
				touched[k.plan] = true
			}
		}
		for id := range touched {
			if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET revision=revision+1,updated_ns=? WHERE id=?`, now.UnixNano(), id); err != nil {
				return err
			}
			changed = append(changed, id)
		}
		return nil
	})
	return changed, err
}

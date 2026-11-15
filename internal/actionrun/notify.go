package actionrun

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

// The durable run is the outbox. A receiver must deduplicate by run ID because
// the process may stop after sending and before recording its receipt.
var resultDelivery sync.Mutex

func (s *Service) notifyResults(ctx context.Context) error {
	if !resultDelivery.TryLock() {
		return nil
	}
	defer resultDelivery.Unlock()
	rows, err := s.Store.DB.Reader.QueryContext(ctx, "SELECT "+runColumns+" FROM action_runs WHERE notified_ns IS NULL AND state IN (?,?,?,?,?) ORDER BY created_ns", StateSucceeded, StateFailed, StateRefused, StateDenied, StateExpired)
	if err != nil {
		return err
	}
	var pending []Run
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return e
		}
		if r.Requester.Agent != "" {
			pending = append(pending, r)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range pending {
		if err := s.Notify(ctx, r); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.Store.DB.Writer.Submit(ctx, "action_run_notified", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE action_runs SET notified_ns=? WHERE id=? AND notified_ns IS NULL", s.now().UnixNano(), r.ID)
			return err
		}); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

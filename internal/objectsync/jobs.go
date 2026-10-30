package objectsync

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"
	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

const jobType = "object-sync.v1"

// Jobs uses the shared scheduler's reference SQL store in a separate private
// jobs.db. Its internal transactions use a single SQLite writer connection;
// all domain records and scan state still use Tachyon's serialwrite queue.
// Keeping scheduler bookkeeping separate avoids introducing a second writer
// to tachyon.db or reimplementing the library's claim/retry/CAS contracts.
type Jobs struct {
	Store       *sqlstore.Store
	Engine      *scheduler.Engine
	coordinator *Coordinator
	sources     map[string]Source
	db          *sql.DB
	lifecycle   context.Context
	cancel      context.CancelFunc
}

func OpenJobs(ctx context.Context, path string, c *Coordinator, sources []Source) (*Jobs, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = errors.Join(f.Chmod(0600), f.Close())
	if err != nil {
		return nil, err
	}
	opts := sqlitekit.WriterOptions()
	opts.Synchronous = "FULL"
	db, err := sqlitekit.OpenWriter(ctx, path, sqlitekit.OpenOptions{Options: opts})
	if err != nil {
		return nil, err
	}
	// sqlstore owns its own transactions; keep its pool bounded to one writer.
	db.SetMaxOpenConns(1)
	if err = sqlstore.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	store, err := sqlstore.New(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	j := &Jobs{Store: store, coordinator: c, sources: map[string]Source{}, db: db}
	j.lifecycle, j.cancel = context.WithCancel(context.Background())
	for _, s := range sources {
		j.sources[sourceID(s.Scope())] = s
	}
	j.Engine = scheduler.New(store, j, scheduler.WithConcurrency(2), scheduler.WithFireTimeout(runTimeout), scheduler.WithClaimLease(leaseDuration), scheduler.WithObserver(scheduler.NewSlogObserver(slog.Default())))
	return j, nil
}
func (j *Jobs) Close() error { j.cancel(); j.Engine.Stop(); return j.db.Close() }

// Enqueue deliberately runs the sync synchronously: scheduler success means
// the data was applied and the scan completed, not merely put in another queue.
func (j *Jobs) Enqueue(ctx context.Context, job scheduler.Job) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(j.lifecycle, cancel)
	defer stop()
	defer cancel()
	if job.JobType != jobType {
		return errors.New("unknown object sync job type")
	}
	s, ok := j.sources[job.ScheduleID]
	if !ok {
		// An orphan schedule (its source is no longer configured here) can never succeed: skip the fire and
		// disable the schedule so it stops retrying and logging errors every interval.
		if err := j.Store.DisableSchedule(ctx, job.ScheduleID); err != nil {
			slog.Warn("object sync: disabling orphan schedule failed", "schedule", job.ScheduleID, "err", err)
		}
		return scheduler.ErrDuplicateJob
	}
	sch, exists, err := j.Store.GetSchedule(ctx, job.ScheduleID)
	if err != nil {
		return errors.New("sync schedule unavailable")
	}
	if !exists || !sch.Enabled {
		return scheduler.ErrDuplicateJob
	}
	_, err = j.coordinator.Run(ctx, s, job.FireID)
	return err
}

// Enable requires a successful import for this exact provider scope. Calling it
// again preserves the existing next fire; host restarts never reset cadence.
func (j *Jobs) Enable(ctx context.Context, s Source, interval time.Duration) error {
	if interval < time.Minute || interval > 24*time.Hour {
		return errors.New("sync interval must be between 1m and 24h")
	}
	state, err := j.coordinator.State(ctx, s.Scope())
	if err != nil {
		return err
	}
	if state.Phase != "succeeded" || state.LastSuccessAt == "" {
		return errors.New("validate this source with a successful import before enabling its job")
	}
	id := sourceID(s.Scope())
	existing, ok, err := j.Store.GetSchedule(ctx, id)
	if err != nil {
		return err
	}
	if ok {
		if existing.JobType != jobType || existing.Interval != interval {
			return errors.New("existing job has a different type or interval")
		}
		if existing.Enabled {
			return nil
		}
		_, err = j.db.ExecContext(ctx, `UPDATE gosched_schedules SET enabled=1,next_run=? WHERE id=? AND enabled=0`, time.Now().UTC().Add(5*time.Second).Format(stamp), id)
		return err
	}
	return j.Store.CreateSchedule(ctx, scheduler.Schedule{
		ID: id, JobType: jobType, Enabled: true, Interval: interval, NextRun: time.Now().UTC().Add(5 * time.Second),
		Overlap: scheduler.OverlapSkip, Misfire: scheduler.MisfireRunOnce, KeepLastN: 100,
		Retry: scheduler.RetryPolicy{MaxAttempts: 3, Backoff: scheduler.BackoffPolicy{Strategy: scheduler.BackoffExponential, InitialDelay: 30 * time.Second, MaxDelay: 2 * time.Minute}},
	})
}

func (j *Jobs) Disable(ctx context.Context, s Source) error {
	return j.Store.DisableSchedule(ctx, sourceID(s.Scope()))
}

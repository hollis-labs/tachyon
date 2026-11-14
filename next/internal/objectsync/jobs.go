package objectsync

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"
)

// JobType is the scheduler job type of object sync, the old build's name: the migrated schedules
// carry it.
const JobType = "object-sync.v1"

const jobType = JobType

// Jobs runs object-sync fires. The schedules live in the scheduler module's store (libs sqlstore in
// the main database, where migrate-from copied the old jobs.db rows); the scheduler module's engine
// dispatches fires to Enqueue through Tachyon's runner, and only when tachyon.features.object_sync
// is on. Scheduler bookkeeping and the sync's own writes share the one writer connection: the engine
// claims and settles a fire in its own short transactions and holds none while Enqueue runs.
type Jobs struct {
	Store       *sqlstore.Store
	Engine      *scheduler.Engine // the module's engine; tests set their own
	coordinator *Coordinator
	sources     map[string]Source
	lifecycle   context.Context
	cancel      context.CancelFunc
}

// NewJobs is object sync over a scheduler store.
func NewJobs(st *sqlstore.Store, c *Coordinator, sources []Source) *Jobs {
	j := &Jobs{Store: st, coordinator: c, sources: map[string]Source{}}
	j.lifecycle, j.cancel = context.WithCancel(context.Background())
	for _, s := range sources {
		j.sources[sourceID(s.Scope())] = s
	}
	return j
}

// EngineOptions are the old engine's settings, for the scheduler module (scheduler.UseOptionsFunc):
// two concurrent fires, the run timeout and claim lease of a sync, and slog observation.
func EngineOptions() []scheduler.Option {
	return []scheduler.Option{scheduler.WithConcurrency(2), scheduler.WithFireTimeout(runTimeout), scheduler.WithClaimLease(leaseDuration),
		scheduler.WithObserver(scheduler.NewSlogObserver(slog.Default()))}
}

// Close cancels the syncs in flight. The store and the engine are the module's.
func (j *Jobs) Close() error { j.cancel(); return nil }

// SourceID is the schedule id of a source's scope (the old build's derivation).
func SourceID(s Source) string { return sourceID(s.Scope()) }

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
		// The source is not configured in THIS process. jobs.db is shared by every tachyon process on the data
		// dir, so this does not mean the schedule is orphaned: a process started without the service env
		// (TETHER_ADDR unset) sees none of the Tether sources. Disabling here (08d4d79) switched off five
		// schedules on 2026-10-30 and stalled the sync for two weeks. Skip the fire, leave the schedule as it
		// is, and let the status route and the stall alert show a schedule nobody is running.
		slog.Warn("object sync: skipping a fire for a source not configured in this process", "schedule", job.ScheduleID)
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
		_, err = j.Store.DB().ExecContext(ctx, `UPDATE gosched_schedules SET enabled=1,next_run=? WHERE id=? AND enabled=0`, time.Now().UTC().Add(5*time.Second).Format(stamp), id)
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

// Coordinator is the sync coordinator the jobs run.
func (j *Jobs) Coordinator() *Coordinator { return j.coordinator }

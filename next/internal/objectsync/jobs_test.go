package objectsync

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"
	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

type fakeSource struct {
	scan  func(context.Context, time.Time, func(Progress) error) (Snapshot, error)
	calls atomic.Int32
}

func (*fakeSource) Name() string { return "test-projects" }
func (*fakeSource) Scope() objects.SourceScope {
	return objects.SourceScope{Provider: "test", Instance: "test-host", Collection: "projects", Kind: objects.ProjectKind}
}
func (s *fakeSource) Scan(ctx context.Context, at time.Time, p func(Progress) error) (Snapshot, error) {
	s.calls.Add(1)
	return s.scan(ctx, at, p)
}
func newFake() *fakeSource {
	s := &fakeSource{}
	s.scan = func(ctx context.Context, at time.Time, p func(Progress) error) (Snapshot, error) {
		c := collect(s.Scope(), at)
		if err := c.add("a", "", false, objects.Project{Common: objects.Common{Name: "Example"}}); err != nil {
			return Snapshot{}, err
		}
		c.snapshot.Authoritative = true
		return c.snapshot, p(Progress{Pages: 1, Items: 1, Checkpoint: "complete"})
	}
	return s
}

func TestCoordinatorCrossProcessLeaseCancellationAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tachyon.db")
	first, err := store.OpenFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.OpenFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a, b := New(first), New(second)
	s := newFake()
	entered := make(chan struct{})
	s.scan = func(ctx context.Context, _ time.Time, _ func(Progress) error) (Snapshot, error) {
		close(entered)
		<-ctx.Done()
		return Snapshot{}, ctx.Err()
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, ierr := a.Run(runCtx, s, ""); done <- ierr }()
	<-entered
	if _, ierr := b.Run(ctx, s, ""); !errors.Is(ierr, ErrBusy) {
		t.Fatalf("concurrent runner was not fenced: %v", ierr)
	}
	cancel()
	if ierr := <-done; ierr == nil {
		t.Fatal("cancelled scan succeeded")
	}
	state, ierr := b.State(ctx, s.Scope())
	if ierr != nil || state.Phase != "failed" || state.Problem != "provider scan cancelled" {
		t.Fatalf("cancelled state: %+v %v", state, ierr)
	}
	// Simulate a process lost after claiming a source. A new process may recover
	// only once the persisted lease expires, retaining the old interrupted run.
	old := time.Now().Add(-time.Hour).UTC().Format(stamp)
	ierr = first.Writer.Submit(ctx, "test_crashed_sync", func(ctx context.Context, tx store.Tx) error {
		_, jerr := tx.ExecContext(ctx, `UPDATE object_sync_state SET phase='scanning',run_id='crashed',lease_until=? WHERE source_id=?`, old, sourceID(s.Scope()))
		if jerr != nil {
			return jerr
		}
		_, jerr = tx.ExecContext(ctx, `INSERT INTO object_sync_runs(id,source_id,started_at,phase) VALUES('crashed',?,?,'scanning')`, sourceID(s.Scope()), old)
		return jerr
	})
	if ierr != nil {
		t.Fatal(ierr)
	}
	s = newFake()
	mustRun(t, b, s)
	var phase string
	if ierr = second.Reader.QueryRowContext(ctx, `SELECT phase FROM object_sync_runs WHERE id='crashed'`).Scan(&phase); ierr != nil || phase != "interrupted" {
		t.Fatalf("crash recovery history: %s %v", phase, ierr)
	}
}

func TestCoordinatorRejectsInvalidBatchAndDeduplicatesCompletedFire(t *testing.T) {
	_, c := testStore(t)
	s := newFake()
	_, err := c.Run(ctx, s, "fire-one")
	if err != nil {
		t.Fatal(err)
	}
	original := get(t, c, s, "a")
	if _, err = c.Run(ctx, s, "fire-one"); !errors.Is(err, scheduler.ErrDuplicateJob) {
		t.Fatalf("fire replay: %v", err)
	}
	if s.calls.Load() != 1 {
		t.Fatal("completed fire fetched twice")
	}
	s.scan = func(ctx context.Context, at time.Time, p func(Progress) error) (Snapshot, error) {
		return Snapshot{Authoritative: true, Observations: []objects.Observation{
			{Key: objects.SourceKey{SourceScope: s.Scope(), ExternalID: "a"}, ObservedAt: at, Definition: []byte(`{"name":"changed"}`)},
			{Key: objects.SourceKey{SourceScope: s.Scope(), ExternalID: "b"}, ObservedAt: at, Definition: []byte(`{"name":"bad","repository_ids":["missing"]}`)},
		}}, nil
	}
	state, err := c.Run(ctx, s, "fire-two")
	if err == nil || state.Phase != "failed" || state.Problem != "object reconciliation failed" {
		t.Fatalf("invalid batch state: %+v %v", state, err)
	}
	after := get(t, c, s, "a")
	if after.Revision != original.Revision || after.Name != original.Name {
		t.Fatal("invalid batch partly committed")
	}
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time                         { return c.now }
func (*testClock) NewTicker(time.Duration) scheduler.Ticker { panic("tests use synchronous TickNow") }
func testEngine(j *Jobs, clock *testClock) *scheduler.Engine {
	return scheduler.New(j.Store, j, scheduler.WithClock(clock), scheduler.WithConcurrency(1), scheduler.WithFireTimeout(time.Second))
}

func TestJobsValidateThenRecurRetryRestartAndPause(t *testing.T) {
	_, c := testStore(t)
	s := newFake()
	path := filepath.Join(t.TempDir(), "jobs.db")
	jobs, err := OpenJobs(ctx, path, c, []Source{s})
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Enable(ctx, s, 5*time.Minute); err == nil {
		t.Fatal("enabled before validation")
	}
	mustRun(t, c, s)
	if err = jobs.Enable(ctx, s, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	sch, ok, err := jobs.Store.GetSchedule(ctx, sourceID(s.Scope()))
	if err != nil || !ok || !sch.Enabled {
		t.Fatalf("schedule: %+v %v", sch, err)
	}
	if err = jobs.Enable(ctx, s, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	again, _, _ := jobs.Store.GetSchedule(ctx, sch.ID)
	if !again.NextRun.Equal(sch.NextRun) {
		t.Fatal("enable reset cadence")
	}
	clock := &testClock{now: sch.NextRun}
	if err = testEngine(jobs, clock).TickNow(ctx); err != nil {
		t.Fatal(err)
	}
	fireID := scheduler.DeriveFireID(sch.ID, sch.NextRun)
	fire, ok, err := jobs.Store.GetFire(ctx, fireID)
	if err != nil || !ok || fire.Status != scheduler.FireSucceeded {
		t.Fatalf("initial fire: %+v %v", fire, err)
	}
	next, _, _ := jobs.Store.GetSchedule(ctx, sch.ID)
	if !next.NextRun.After(sch.NextRun) {
		t.Fatal("recurring schedule did not advance")
	}
	// Fail the next fire, close the scheduler database, and recover its durable
	// retry in a new Jobs instance. Successful object imports remain readable.
	successfulScan := s.scan
	s.scan = func(context.Context, time.Time, func(Progress) error) (Snapshot, error) {
		return Snapshot{}, &scanFailure{"provider returned HTTP 503"}
	}
	clock.now = next.NextRun
	if err = testEngine(jobs, clock).TickNow(ctx); err != nil {
		t.Fatal(err)
	}
	retryID := scheduler.DeriveFireID(sch.ID, next.NextRun)
	failed, _, _ := jobs.Store.GetFire(ctx, retryID)
	if failed.Status != scheduler.FireRetrying || failed.Attempt != 1 || failed.LastError != "provider returned HTTP 503" {
		t.Fatalf("retry: %+v", failed)
	}
	if err = jobs.Close(); err != nil {
		t.Fatal(err)
	}
	jobs, err = OpenJobs(ctx, path, c, []Source{s})
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	s.scan = successfulScan
	clock.now = failed.NextAttemptAt
	if err = testEngine(jobs, clock).TickNow(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _, _ := jobs.Store.GetFire(ctx, retryID)
	if recovered.Status != scheduler.FireSucceeded || recovered.Attempt != 2 {
		t.Fatalf("recovered: %+v", recovered)
	}
	rec := get(t, c, s, "a")
	if rec.Revision != 1 {
		t.Fatal("retry/restart invented object changes")
	}
	calls := s.calls.Load()
	if err = jobs.Disable(ctx, s); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Hour)
	if err = testEngine(jobs, clock).TickNow(ctx); err != nil {
		t.Fatal(err)
	}
	if s.calls.Load() != calls {
		t.Fatal("paused source still ran")
	}
	if err = jobs.Enable(ctx, s, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	enabled, _, _ := jobs.Store.GetSchedule(ctx, sch.ID)
	if !enabled.Enabled {
		t.Fatal("validated source did not resume")
	}
}

func TestSchedulerCoalescesMissedIntervalsAndFencesPendingPausedFire(t *testing.T) {
	_, c := testStore(t)
	s := newFake()
	j, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "jobs.db"), c, []Source{s})
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	mustRun(t, c, s)
	if err = j.Enable(ctx, s, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	sch, _, _ := j.Store.GetSchedule(ctx, sourceID(s.Scope()))
	clock := &testClock{now: sch.NextRun.Add(24 * time.Hour)}
	calls := s.calls.Load()
	if err = testEngine(j, clock).TickNow(ctx); err != nil {
		t.Fatal(err)
	}
	if s.calls.Load() != calls+1 {
		t.Fatal("downtime replayed missed scans instead of coalescing")
	}
	if err = j.Disable(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err = j.Enqueue(ctx, scheduler.Job{JobType: jobType, ScheduleID: sch.ID, FireID: "pending-before-pause"}); !errors.Is(err, scheduler.ErrDuplicateJob) {
		t.Fatalf("pending paused fire dispatched: %v", err)
	}
}

func TestJobsCloseCancelsActiveProviderRead(t *testing.T) {
	_, c := testStore(t)
	s := newFake()
	j, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "jobs.db"), c, []Source{s})
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, c, s)
	if err = j.Enable(ctx, s, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	sch, _, _ := j.Store.GetSchedule(ctx, sourceID(s.Scope()))
	entered := make(chan struct{})
	s.scan = func(ctx context.Context, _ time.Time, _ func(Progress) error) (Snapshot, error) {
		close(entered)
		<-ctx.Done()
		return Snapshot{}, ctx.Err()
	}
	j.Engine = testEngine(j, &testClock{now: sch.NextRun})
	done := make(chan error, 1)
	go func() { done <- j.Engine.TickNow(ctx) }()
	<-entered
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	state, err := c.State(ctx, s.Scope())
	if err != nil || state.Phase != "failed" || state.Problem != "provider scan cancelled" {
		t.Fatalf("shutdown state: %+v %v", state, err)
	}
}

func TestEnqueueSkipsButKeepsScheduleForSourceNotConfiguredHere(t *testing.T) {
	_, c := testStore(t)
	s := newFake()
	jobs, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "jobs.db"), c, []Source{s})
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	mustRun(t, c, s)
	if err = jobs.Enable(ctx, s, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	id := sourceID(s.Scope())
	delete(jobs.sources, id) // the source is no longer configured in this process
	err = jobs.Enqueue(ctx, scheduler.Job{JobType: jobType, ScheduleID: id, FireID: "f1"})
	if !errors.Is(err, scheduler.ErrDuplicateJob) {
		t.Fatalf("orphan fire must be skipped, got %v", err)
	}
	sch, ok, err := jobs.Store.GetSchedule(ctx, id)
	// jobs.db is shared: another process (the service, with its env) may own this source. Never disable it here.
	if err != nil || !ok || !sch.Enabled {
		t.Fatalf("a source missing from this process must keep its schedule enabled: %+v %v", sch, err)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("source must not be scanned for an orphan: %d", s.calls.Load())
	}
}

// OpenJobs is the old build's arrangement, kept for these tests: the scheduler's sqlstore in its
// own file, with an engine of the module's options. Production uses the scheduler module's store in
// the main database (NewJobs).
func OpenJobs(ctx context.Context, path string, c *Coordinator, sources []Source) (*Jobs, error) {
	opts := sqlitekit.WriterOptions()
	opts.Synchronous = "FULL"
	db, err := sqlitekit.OpenWriter(ctx, path, sqlitekit.OpenOptions{Options: opts})
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = sqlstore.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	st, err := sqlstore.New(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	j := NewJobs(st, c, sources)
	j.Engine = scheduler.New(st, j, EngineOptions()...)
	stop := j.cancel
	j.cancel = func() { stop(); j.Engine.Stop(); _ = db.Close() }
	return j, nil
}

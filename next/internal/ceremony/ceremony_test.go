package ceremony

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func openDB(t *testing.T) *store.Store {
	t.Helper()
	db := storetest.New(t).Open(t)
	return db
}

func exec(t *testing.T, db *store.Store, q string, args ...any) {
	t.Helper()
	if err := db.Writer.Submit(context.Background(), "t", func(ctx context.Context, tx store.Tx) error { _, err := tx.ExecContext(ctx, q, args...); return err }); err != nil {
		t.Fatal(err)
	}
}

var seq int

func taskEvent(t *testing.T, db *store.Store, task, state string, at time.Time) {
	t.Helper()
	seq++
	exec(t, db, `INSERT INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject,refs_json) VALUES(?,?,?,?,?,?)`,
		seq, fmt.Sprintf("e:%d", seq), at.UnixNano(), "task", task+" "+state, `[{"kind":"task","ref":"`+task+`","label":"`+task+`"}]`)
}

func approval(t *testing.T, db *store.Store, id string, created time.Time, decided *time.Time) {
	t.Helper()
	var d any
	if decided != nil {
		d = decided.UnixNano()
	}
	exec(t, db, `INSERT INTO approval_items(id,caller_scope,idempotency_key,state,revision,terminal,created_ns,record) VALUES(?,'test',?,'pending',1,0,?,'{}')`, id, id, created.UnixNano())
	exec(t, db, `INSERT INTO approval_item_index(item_id,revision,state,terminal,status,kind,grp,risk,risk_rank,title,summary,requester_id,requester_label,requester_kind,session_ref,created_ns,decided_ns,interactive,has_secret,command_exec,destructive,effect_ops,search)
VALUES(?,1,'pending',0,'pending','approval','g','low',1,'t','s','r','r','agent','',?,?,0,0,0,0,'[]','')`, id, created.UnixNano(), d)
}

type fakeTorque struct {
	stalled []Task
	total   int
	pending []Checkpoint
	records map[string]CheckpointRecord
	done    int
	err     error
}

func (f fakeTorque) Stalled(context.Context, time.Time, int) ([]Task, int, error) {
	return f.stalled, f.total, f.err
}
func (f fakeTorque) DoneSince(context.Context, time.Time) (int, error) { return f.done, f.err }
func (f fakeTorque) PendingCheckpoints(context.Context) ([]Checkpoint, error) {
	return f.pending, f.err
}

func (f fakeTorque) Checkpoint(_ context.Context, id string) (CheckpointRecord, error) {
	r, ok := f.records[id]
	if !ok {
		return r, fmt.Errorf("torque answered 404")
	}
	return r, nil
}

func checkpointEvent(t *testing.T, db *store.Store, id, state string, at time.Time) {
	t.Helper()
	seq++
	exec(t, db, `INSERT INTO checkpoint_events(correlation_id,state,at_ns,tether_seq) VALUES(?,?,?,?)`, id, state, at.UnixNano(), seq)
}

type fakeAsks int

func (f fakeAsks) AsksSince(context.Context, time.Time) (int, error) { return int(f), nil }

func TestMeterCountsGatesWaitsStallsAndActivity(t *testing.T) {
	db := openDB(t)
	h := time.Hour
	// Reviews: A entered review 2d ago and left after 2h; B entered 10d ago (outside) and left 1d ago (wait 9d);
	// C is still in review.
	taskEvent(t, db, "CW-A", "doing", now.Add(-3*24*h))
	taskEvent(t, db, "CW-A", "review", now.Add(-2*24*h))
	taskEvent(t, db, "CW-A", "done", now.Add(-2*24*h+2*h))
	taskEvent(t, db, "CW-B", "review", now.Add(-10*24*h))
	taskEvent(t, db, "CW-B", "doing", now.Add(-24*h))
	taskEvent(t, db, "CW-C", "review", now.Add(-h))
	// Approvals: one created and decided in 30m, one pending, one created long ago.
	d := now.Add(-5*h + 30*time.Minute)
	approval(t, db, "a1", now.Add(-5*h), &d)
	approval(t, db, "a2", now.Add(-h), nil)
	approval(t, db, "a3", now.Add(-20*24*h), nil)
	// Activity: 3 session events and 1 mail row in the window, 1 session event outside.
	for i, at := range []time.Time{now.Add(-h), now.Add(-2 * h), now.Add(-3 * h), now.Add(-9 * 24 * h)} {
		exec(t, db, `INSERT INTO agents_events(session_id,tether_seq,tether_event_id,type,at_ns,actor,summary) VALUES('s',?,?,'tool',?,'tether','x')`, 100+i, fmt.Sprintf("x:%d", i), at.UnixNano())
	}
	seq++
	exec(t, db, `INSERT INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject) VALUES(?,?,?,'message','@a sent request to @b')`, seq, fmt.Sprintf("e:%d", seq), now.Add(-h).UnixNano())

	// Checkpoints seen on the collector's stream this week; their types and times come from Torque's records.
	// c1 approval emitted 3h ago, responded 2h ago; c2 pr_review emitted 30d ago, canceled 1h ago (no responded_at:
	// the stream's time); c3 approval emitted 1h ago, pending; c4 is a data checkpoint, not a gate.
	checkpointEvent(t, db, "c1", "pending", now.Add(-3*h))
	checkpointEvent(t, db, "c1", "responded", now.Add(-2*h))
	checkpointEvent(t, db, "c2", "canceled", now.Add(-h))
	checkpointEvent(t, db, "c4", "pending", now.Add(-h))
	responded := now.Add(-2 * h)
	m := &Meterer{DB: db, Now: func() time.Time { return now }, Asks: fakeAsks(2),
		Torque: fakeTorque{stalled: []Task{{ID: "CW-N", Status: "doing", UpdatedAt: now.Add(-48 * h)}, {ID: "CW-O", Status: "review", UpdatedAt: now.Add(-72 * h)}}, total: 9,
			pending: []Checkpoint{{ID: "c3", Type: "approval", EmittedAt: now.Add(-h)}, {ID: "p1", Type: "approval", EmittedAt: now.Add(-60 * 24 * h)}}, done: 4,
			records: map[string]CheckpointRecord{
				"c1": {ID: "c1", Type: "approval", Status: "responded", EmittedAt: now.Add(-3 * h), RespondedAt: &responded},
				"c2": {ID: "c2", Type: "pr_review", Status: "canceled", EmittedAt: now.Add(-30 * 24 * h)},
				"c3": {ID: "c3", Type: "approval", Status: "pending", EmittedAt: now.Add(-h)},
				"c4": {ID: "c4", Type: "data", Status: "pending", EmittedAt: now.Add(-h)},
			}}}
	r := m.Read(context.Background())
	if r.Gates != (Gates{Total: 2 + 2 + 2 + 2, Reviews: 2, Approvals: 2, Checkpoints: 2, Asks: 2}) {
		t.Fatalf("gates: %+v (reviews A and C; approvals a1 and a2; checkpoints c1 and c3; 2 asks)", r.Gates)
	}
	// Waits closed in the window: review A 2h, review B 9d, approval a1 30m, checkpoint c1 1h, c2 30d-1h: median 2h.
	if r.WaitSamples != 5 || r.MedianWaitSeconds == nil || *r.MedianWaitSeconds != int64((2*h)/time.Second) {
		t.Fatalf("median wait: %v over %d", r.MedianWaitSeconds, r.WaitSamples)
	}
	if r.StalledTotal != 9 || r.Stalled[0].ID != "CW-O" || r.PendingTotal != 2 || r.PendingCheckpoints[0].ID != "p1" || r.PendingCheckpoints[1].ID != "c3" {
		t.Fatalf("stalled and pending, oldest first: %+v %+v", r.Stalled, r.PendingCheckpoints)
	}
	if r.Activity != 4 || r.Done != 4 || r.Comparison != "pending" || r.Previous != nil {
		t.Fatalf("activity/done/comparison: %+v", r)
	}
	for k, s := range r.Sources {
		if !s.OK {
			t.Errorf("source %s: %+v", k, s)
		}
	}
}

// A week later the meter compares with the snapshot it kept; activity up 25% with done flat is a regression.
func TestMeterComparesWithLastWeeksSnapshot(t *testing.T) {
	db := openDB(t)
	clock := now
	m := &Meterer{DB: db, Now: func() time.Time { return clock }, Torque: fakeTorque{done: 5}}
	for i := 0; i < 8; i++ {
		exec(t, db, `INSERT INTO agents_events(session_id,tether_seq,tether_event_id,type,at_ns,actor,summary) VALUES('s',?,?,'tool',?,'tether','x')`, i, fmt.Sprintf("x:%d", i), clock.Add(-time.Hour).UnixNano())
	}
	if r := m.Read(context.Background()); r.Comparison != "pending" {
		t.Fatalf("first week: %+v", r)
	}
	clock = now.Add(Window)
	for i := 0; i < 10; i++ {
		exec(t, db, `INSERT INTO agents_events(session_id,tether_seq,tether_event_id,type,at_ns,actor,summary) VALUES('s',?,?,'tool',?,'tether','x')`, 100+i, fmt.Sprintf("y:%d", i), clock.Add(-time.Hour).UnixNano())
	}
	r := m.Read(context.Background())
	if r.Previous == nil || r.Previous.Activity != 8 || r.Activity != 10 || r.Comparison != "regression" {
		t.Fatalf("week two: %+v prev %+v", r, r.Previous)
	}
	if Compare(Snapshot{Activity: 8, Done: 5}, Snapshot{Activity: 10, Done: 6}) != "steady" || Compare(Snapshot{Activity: 8, Done: 5}, Snapshot{Activity: 9, Done: 5}) != "steady" ||
		Compare(Snapshot{Activity: 0, Done: 0}, Snapshot{Activity: 50, Done: 0}) != "steady" {
		t.Fatal("progress up, or activity up less than 25%, or no baseline: not a regression")
	}
}

// A failed Torque read is reported and never becomes history.
func TestMeterKeepsNoSnapshotFromAFailedRead(t *testing.T) {
	db := openDB(t)
	m := &Meterer{DB: db, Now: func() time.Time { return now }, Torque: fakeTorque{err: fmt.Errorf("torque unreachable")}}
	r := m.Read(context.Background())
	if r.Sources["done"].OK || !strings.Contains(r.Sources["done"].Error, "unreachable") || r.Sources["asks"].OK || r.Sources["pending_checkpoints"].OK {
		t.Fatalf("%+v", r.Sources)
	}
	var n int
	_ = db.Reader.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM ceremony_daily`).Scan(&n)
	if n != 0 {
		t.Fatal("a partial read was kept as a snapshot")
	}
}

// The Torque client asks for one status at a time, oldest first, with totals, and pages the pending checkpoints.
func TestTorqueHTTP(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen = append(seen, r.URL.Path+"?"+q.Get("status"))
		switch {
		case r.URL.Path == "/api/v1/tasks" && q.Get("status") == "done":
			fmt.Fprint(w, `{"items":[],"meta":{"total":92}}`)
		case r.URL.Path == "/api/v1/tasks":
			if q.Get("updated_before") == "" || q.Get("sort_dir") != "asc" || q.Get("include_total") != "true" {
				t.Errorf("stalled query: %v", q)
			}
			fmt.Fprintf(w, `{"items":[{"id":"CW-%s","title":"t","status":"%s","updated_at":"2026-11-10T00:00:00Z"}],"meta":{"total":3}}`, q.Get("status"), q.Get("status")) //nolint:gosec // a test handler
		case r.URL.Path == "/api/v1/checkpoints/c%2F1" || r.URL.Path == "/api/v1/checkpoints/c/1":
			t.Errorf("an id is escaped into one path segment: %s", r.URL.Path)
		case r.URL.Path == "/api/v1/checkpoints/c9":
			fmt.Fprint(w, `{"correlation_id":"c9","type":"pr_review","status":"responded","emitted_at":"2026-11-14T10:00:00Z","responded_at":"2026-11-14T11:30:00Z"}`)
		case r.URL.Path == "/api/v1/checkpoints/pending" && q.Get("cursor") == "":
			fmt.Fprint(w, `{"items":[{"correlation_id":"c1","task_id":"CW-1","type":"approval","payload_json":"{\"title\":\"Merge?\"}","emitted_at":"2026-09-14T18:39:09Z"}],"meta":{"has_more":true,"next_cursor":"n2"}}`)
		case r.URL.Path == "/api/v1/checkpoints/pending":
			fmt.Fprint(w, `{"items":[{"correlation_id":"c2","task_id":"CW-2","type":"pr_review","payload_json":"{}","emitted_at":"2026-11-01T21:24:24Z"}],"meta":{"has_more":false}}`)
		}
	}))
	defer srv.Close()
	tq := &TorqueHTTP{Base: srv.URL}
	st, total, err := tq.Stalled(context.Background(), now, 20)
	if err != nil || total != 9 || len(st) != 3 {
		t.Fatal(st, total, err)
	}
	if n, ierr := tq.DoneSince(context.Background(), now); ierr != nil || n != 92 {
		t.Fatal(n, ierr)
	}
	rec, err := tq.Checkpoint(context.Background(), "c9")
	if err != nil || rec.Type != "pr_review" || rec.Status != "responded" || rec.RespondedAt == nil || rec.RespondedAt.Sub(rec.EmittedAt) != 90*time.Minute {
		t.Fatal(rec, err)
	}
	cps, err := tq.PendingCheckpoints(context.Background())
	if err != nil || len(cps) != 2 || cps[0].Title != "Merge?" || cps[1].Type != "pr_review" {
		t.Fatal(cps, err)
	}
}

// slowTorque counts checkpoint reads; each fails after a delay, or blocks until the context ends when hang is set.
type slowTorque struct {
	fakeTorque
	hang  bool
	calls atomic.Int64
}

func (f *slowTorque) Checkpoint(ctx context.Context, _ string) (CheckpointRecord, error) {
	f.calls.Add(1)
	if f.hang {
		<-ctx.Done()
		return CheckpointRecord{}, ctx.Err()
	}
	time.Sleep(20 * time.Millisecond)
	return CheckpointRecord{}, fmt.Errorf("torque answered 502")
}

// A failing or hung Torque costs one read and at most the Torque budget, not one client timeout per checkpoint.
func TestMeterStopsAtTheFirstTorqueFailureAndKeepsToItsBudget(t *testing.T) {
	db := openDB(t)
	for i := range 300 {
		checkpointEvent(t, db, fmt.Sprintf("cp-%03d", i), "pending", now.Add(-time.Hour))
	}
	failing := &slowTorque{}
	r := (&Meterer{DB: db, Now: func() time.Time { return now }, Torque: failing}).Read(context.Background())
	if failing.calls.Load() != 1 || r.Sources["checkpoints"].OK || !strings.Contains(r.Sources["checkpoints"].Error, "502") {
		t.Fatalf("failing Torque: %d reads, %+v", failing.calls.Load(), r.Sources["checkpoints"])
	}

	defer func(b time.Duration) { TorqueBudget = b }(TorqueBudget)
	TorqueBudget = 100 * time.Millisecond
	hung := &slowTorque{hang: true}
	start := time.Now()
	r = (&Meterer{DB: db, Now: func() time.Time { return now }, Torque: hung}).Read(context.Background())
	if el := time.Since(start); el > 2*time.Second || hung.calls.Load() != 1 || r.Sources["checkpoints"].OK {
		t.Fatalf("hung Torque: %s, %d reads, %+v", el, hung.calls.Load(), r.Sources)
	}
}

// GET ceremony serves the last read while it is fresh, so a page view does not call Torque again.
func TestLatestReusesAFreshRead(t *testing.T) {
	db := openDB(t)
	checkpointEvent(t, db, "cp-1", "pending", now.Add(-time.Hour))
	clock := now
	tq := &slowTorque{}
	m := &Meterer{DB: db, Now: func() time.Time { return clock }, Torque: tq}
	m.Latest(context.Background(), time.Minute)
	m.Latest(context.Background(), time.Minute)
	if tq.calls.Load() != 1 {
		t.Fatalf("a fresh read was not reused: %d reads", tq.calls.Load())
	}
	clock = now.Add(2 * time.Minute)
	m.Latest(context.Background(), time.Minute)
	if tq.calls.Load() != 2 {
		t.Fatalf("a stale read was served: %d reads", tq.calls.Load())
	}
}

package alertstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

var lifePack = []byte(`{"version":1,"id":"t","rules":[
 {"id":"fold","match":{"kinds":["fold"]},"alert":{"title":"Folds","severity":"notice"},"coalesce":{"key":"k:{{payload.id}}"}},
 {"id":"fold-up","match":{"kinds":["fold.up"]},"alert":{"title":"Folds up","severity":"error"},"coalesce":{"key":"k:{{payload.id}}"}},
 {"id":"esc","match":{"kinds":["esc"]},"alert":{"title":"Escalates","severity":"warning"},"escalate":{"afterSec":600,"to":"error"}}]}`)

type life struct {
	s       *Store
	now     *time.Time
	changes *[]string
}

func newLife(t *testing.T) life {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pack, _ := alertsrules.ParsePack(lifePack)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	var ch []string
	s := &Store{DB: db, Pack: func() alertsrules.Pack { return pack }, Now: func() time.Time { return now },
		OnChange: func(id string, _ int64, kind string) { ch = append(ch, kind) }}
	return life{s, &now, &ch}
}

func fe(kind, id, key string, at int64, subj ...alertsapi.Subject) alertsource.Event {
	return alertsource.Event{Source: "t", SourceEventID: id, Kind: kind, AtMS: at, Payload: map[string]any{"id": key}, Subjects: subj}
}

func (l life) only(t *testing.T) alertsapi.Alert {
	t.Helper()
	as, _, err := l.s.List(context.Background(), ListQuery{View: "history", Limit: 50})
	act, _, err2 := l.s.List(context.Background(), ListQuery{View: "active", Limit: 50})
	if err != nil || err2 != nil {
		t.Fatal(err, err2)
	}
	all := append(act, as...)
	if len(all) != 1 {
		t.Fatalf("want one alert, got %d", len(all))
	}
	return all[0]
}

func TestCoalesceFoldsWithinWindow(t *testing.T) {
	l := newLife(t)
	base := l.now.UnixMilli()
	for i, at := range []int64{base, base + 60_000, base + 100_000} {
		if err := l.s.Ingest(fe("fold", string(rune('a'+i)), "x", at)); err != nil {
			t.Fatal(err)
		}
	}
	a := l.only(t)
	if a.Count != 3 || a.LastAtMS != base+100_000 || a.FirstAtMS != base {
		t.Fatalf("%+v", a)
	}
	if err := l.s.Ingest(fe("fold", "late", "x", base+100_000+300_000)); err != nil { // outside the 120 s window
		t.Fatal(err)
	}
	if err := l.s.Ingest(fe("fold", "other", "y", base+1)); err != nil { // other key
		t.Fatal(err)
	}
	as, _, _ := l.s.List(context.Background(), ListQuery{View: "active", Limit: 50})
	if len(as) != 3 {
		t.Fatalf("want 3 alerts, got %d", len(as))
	}
	// a redelivered event does not count twice
	if err := l.s.Ingest(fe("fold", "late", "x", base+100_000+300_000)); err != nil {
		t.Fatal(err)
	}
	as, _, _ = l.s.List(context.Background(), ListQuery{View: "active", Limit: 50})
	for _, x := range as {
		if x.Count > 3 {
			t.Fatalf("redelivery counted: %+v", x)
		}
	}
}

func TestCoalesceOffAndSeverityRise(t *testing.T) {
	l := newLife(t)
	l.s.Window = func(context.Context) time.Duration { return 0 }
	base := l.now.UnixMilli()
	_ = l.s.Ingest(fe("fold", "a", "x", base))
	_ = l.s.Ingest(fe("fold", "b", "x", base+1))
	if as, _, _ := l.s.List(context.Background(), ListQuery{View: "active", Limit: 50}); len(as) != 2 {
		t.Fatalf("window 0 turns coalescing off: %d", len(as))
	}

	l = newLife(t)
	_ = l.s.Ingest(fe("fold", "a", "x", base))
	a := l.only(t)
	if _, err := l.s.Apply(context.Background(), a.AlertID, Op{Op: "read", ExpectedVersion: &a.Version}); err != nil {
		t.Fatal(err)
	}
	_ = l.s.Ingest(fe("fold", "b", "x", base+1)) // same severity: stays read
	if a = l.only(t); !a.Read || a.Count != 2 {
		t.Fatalf("%+v", a)
	}
	*l.changes = nil
	_ = l.s.Ingest(fe("fold.up", "c", "x", base+2)) // rises to error: unread again, reported as updated
	if a = l.only(t); a.Read || a.Severity != "error" || a.Count != 3 || a.SeverityBase != "notice" {
		t.Fatalf("%+v", a)
	}
	if len(*l.changes) != 1 || (*l.changes)[0] != "updated" {
		t.Fatalf("%v", *l.changes)
	}
}

func TestSnoozeWakesOnTime(t *testing.T) {
	l := newLife(t)
	_ = l.s.Ingest(fe("fold", "a", "x", l.now.UnixMilli()))
	a := l.only(t)
	if _, err := l.s.Apply(context.Background(), a.AlertID, Op{Op: "snooze", Preset: "15m", ExpectedVersion: &a.Version}); err != nil {
		t.Fatal(err)
	}
	if w, _, _ := l.s.Tick(context.Background()); w != 0 {
		t.Fatal("not due yet")
	}
	if due, ok := l.s.NextDue(context.Background()); !ok || !due.Equal(l.now.Add(15*time.Minute)) {
		t.Fatalf("%v %v", due, ok)
	}
	*l.now = l.now.Add(16 * time.Minute)
	*l.changes = nil
	w, _, err := l.s.Tick(context.Background())
	a = l.only(t)
	if err != nil || w != 1 || a.Status != "active" || a.Read || a.SnoozeUntil != nil || len(*l.changes) != 1 || (*l.changes)[0] != "woke" {
		t.Fatalf("%v %d %+v %v", err, w, a, *l.changes)
	}
	if w, _, _ := l.s.Tick(context.Background()); w != 0 {
		t.Fatal("a woken alert is not woken twice")
	}
}

func TestDeferUntilRefChange(t *testing.T) {
	l := newLife(t)
	_ = l.s.Ingest(fe("esc", "a", "x", l.now.UnixMilli()))
	a := l.only(t)
	ref := &alertsapi.UntilRef{Kind: "task", Ref: "CW-1"}
	if _, err := l.s.Apply(context.Background(), a.AlertID, Op{Op: "defer", UntilRefChange: ref, ExpectedVersion: &a.Version}); err != nil {
		t.Fatal(err)
	}
	_ = l.s.Ingest(fe("fold", "b", "y", l.now.UnixMilli(), alertsapi.Subject{Kind: "task", Ref: "CW-2"})) // other subject
	var got alertsapi.Alert
	as, _, _ := l.s.List(context.Background(), ListQuery{View: "history", Limit: 50})
	if len(as) != 1 || as[0].Status != "deferred" {
		t.Fatalf("still deferred: %+v", as)
	}
	_ = l.s.Ingest(fe("fold", "c", "z", l.now.UnixMilli(), alertsapi.Subject{Kind: "task", Ref: "CW-1"}))
	got, _ = l.s.Get(context.Background(), a.AlertID)
	if got.Status != "active" || got.Read || got.Defer != nil {
		t.Fatalf("%+v", got)
	}
}

func TestEscalationRetoastsUnreadOnly(t *testing.T) {
	l := newLife(t)
	base := l.now.UnixMilli()
	_ = l.s.Ingest(fe("esc", "a", "x", base))
	_ = l.s.Ingest(fe("esc", "b", "y", base)) // a second, which is read before its deadline
	as, _, _ := l.s.List(context.Background(), ListQuery{View: "active", Limit: 50})
	if len(as) != 2 {
		t.Fatalf("%d", len(as))
	}
	read := as[0]
	if _, err := l.s.Apply(context.Background(), read.AlertID, Op{Op: "read", ExpectedVersion: &read.Version}); err != nil {
		t.Fatal(err)
	}
	*l.now = l.now.Add(9 * time.Minute)
	if _, e, _ := l.s.Tick(context.Background()); e != 0 {
		t.Fatal("before the deadline")
	}
	*l.now = l.now.Add(2 * time.Minute)
	*l.changes = nil
	_, e, err := l.s.Tick(context.Background())
	if err != nil || e != 1 || len(*l.changes) != 1 || (*l.changes)[0] != "escalated" {
		t.Fatalf("%v %d %v", err, e, *l.changes)
	}
	escalated := 0
	for _, id := range []string{as[0].AlertID, as[1].AlertID} {
		a, _ := l.s.Get(context.Background(), id)
		if a.EscalatedAt != nil {
			escalated++
			if a.Severity != "error" || !a.NeedsYou || a.SeverityBase != "warning" || a.AlertID == read.AlertID {
				t.Fatalf("%+v", a)
			}
		}
	}
	if escalated != 1 {
		t.Fatalf("escalated %d", escalated)
	}
	if _, e, _ := l.s.Tick(context.Background()); e != 0 {
		t.Fatal("escalates once")
	}
}

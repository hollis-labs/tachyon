package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNaniteRunningCancelArchivesProvider(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var stops atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/api/sessions/session%2Ffixture" {
					t.Errorf("unexpected stop route %s %s", r.Method, r.URL.EscapedPath())
					http.NotFound(w, r)
					return
				}
				stops.Add(1)
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"archived": "session/fixture"})
			}))
			defer srv.Close()
			store := testStore(t)
			ctx := context.Background()
			if err := store.Create(ctx, &Launch{ID: "running", Backend: "nanite", State: LaunchStateRunning, SessionID: "session/fixture"}); err != nil {
				t.Fatal(err)
			}
			p := &plugin{adapter: NewNaniteLaunchAdapter(srv.URL, store)}
			env, err := p.HandleVerb(ctx, "launch_cancel", json.RawMessage(`{"launch_id":"running"}`))
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.Get(ctx, "running")
			if err != nil {
				t.Fatal(err)
			}
			if stops.Load() != 1 {
				t.Fatal("provider archive not called")
			}
			if status == http.StatusOK {
				if env.Status != "ok" || stored.State != LaunchStateCancelled || stored.EndedAt == nil {
					t.Fatalf("successful archive: %+v %+v", env, stored)
				}
			} else {
				if env.Status != "error" || stored.State != LaunchStateRunning || stored.EndedAt != nil {
					t.Fatalf("failed archive claimed cancelled: %+v %+v", env, stored)
				}
			}
		})
	}
}

func TestUnsupportedProviderCannotClaimRunningCancellation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Create(ctx, &Launch{ID: "running", Backend: "unsupported", State: LaunchStateRunning, SessionID: "live-session"}); err != nil {
		t.Fatal(err)
	}
	p := &plugin{adapter: &launchLifecycle{store: store, backend: "unsupported"}}
	env, err := p.HandleVerb(ctx, "launch_cancel", json.RawMessage(`{"launch_id":"running"}`))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(ctx, "running")
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != "error" || stored.State != LaunchStateRunning || stored.EndedAt != nil {
		t.Fatalf("unsupported provider cancellation: %+v %+v", env, stored)
	}
}

func TestPreparedCancelExecuteRaceNeverOrphansSession(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	var creates, stops atomic.Int32
	a := &launchLifecycle{store: store, backend: "fixture"}
	a.start = func(context.Context, *Launch) (string, LaunchState, error) {
		creates.Add(1)
		return "fixture-session", LaunchStateRunning, nil
	}
	a.stop = func(context.Context, *Launch) error { stops.Add(1); return nil }
	// Each race has one of three honest outcomes: cancelled intent and no
	// creation, executing/running with cancellation rejected, or a stopped session.
	for i := 0; i < 30; i++ {
		id := string(rune('a' + i))
		if err := store.Create(ctx, &Launch{ID: id, Backend: "fixture", State: LaunchStatePrepared}); err != nil {
			t.Fatal(err)
		}
		beforeCreates, beforeStops := creates.Load(), stops.Load()
		begin := make(chan struct{})
		done := make(chan error, 2)
		go func() { <-begin; _, err := a.Execute(ctx, ExecuteRequest{LaunchID: id}); done <- err }()
		go func() { <-begin; _, err := a.Cancel(ctx, CancelRequest{LaunchID: id}); done <- err }()
		close(begin)
		<-done
		<-done
		l, err := store.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if l.State == LaunchStateCancelled && creates.Load() > beforeCreates && stops.Load() == beforeStops {
			t.Fatalf("claimed cancelled after creation without provider stop: %+v", l)
		}
		if l.State != LaunchStateCancelled && l.State != LaunchStateRunning {
			t.Fatalf("unexpected settled state: %+v", l)
		}
	}
}

func TestTetherExecutingCancelStateUnchanged(t *testing.T) {
	f := &tetherFixture{}
	srv := f.server(t)
	defer srv.Close()
	store := testStore(t)
	ctx := context.Background()
	if err := store.Create(ctx, &Launch{ID: "executing", Backend: "tether", State: LaunchStateExecuting, SessionID: "tether-session"}); err != nil {
		t.Fatal(err)
	}
	a := tetherAdapter(t, srv.URL, store)
	if _, err := a.Cancel(ctx, CancelRequest{LaunchID: "executing"}); err == nil {
		t.Fatal("executing Tether launch claimed cancelled")
	}
	l, err := store.Get(ctx, "executing")
	if err != nil {
		t.Fatal(err)
	}
	if l.State != LaunchStateExecuting || f.stops != 0 {
		t.Fatalf("executing state changed: %+v", l)
	}
}

func TestCompletionWhileStoppingIsNotOverwritten(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Create(ctx, &Launch{ID: "running", State: LaunchStateRunning, SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	a := &launchLifecycle{store: store, backend: "fixture"}
	a.stop = func(ctx context.Context, l *Launch) error {
		_, err := store.Update(ctx, l.ID, func(l *Launch) error { l.State = LaunchStateCompleted; return nil })
		return err
	}
	l, err := a.Cancel(ctx, CancelRequest{LaunchID: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if l.State != LaunchStateCompleted {
		t.Fatalf("completion overwritten: %+v", l)
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestStaleExecutingCancelWithoutSession(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.Create(ctx, &Launch{ID: "stale", State: LaunchStateExecuting}); err != nil {
		t.Fatal(err)
	}
	a := &launchLifecycle{store: store}
	a.stop = func(context.Context, *Launch) error { t.Fatal("stop called without session ID"); return nil }
	l, err := a.Cancel(ctx, CancelRequest{LaunchID: "stale", Reason: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if l.State != LaunchStateCancelled || l.EndedAt == nil || l.Error != "cancelled: provider outcome unknown; a session may exist" {
		t.Fatalf("dishonest resolution: %+v", l)
	}
}

func TestStaleExecutingCancelWithSession(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			ctx := context.Background()
			store := testStore(t)
			if err := store.Create(ctx, &Launch{ID: "stale", State: LaunchStateExecuting, SessionID: "known-session", Error: "previous outcome"}); err != nil {
				t.Fatal(err)
			}
			a := &launchLifecycle{store: store, replaySafe: true}
			entered, release := make(chan struct{}), make(chan struct{})
			a.stop = func(_ context.Context, l *Launch) error {
				if l.SessionID != "known-session" {
					t.Errorf("wrong session: %+v", l)
				}
				close(entered)
				<-release
				if fail {
					return errors.New("provider refused stop")
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { _, err := a.Cancel(ctx, CancelRequest{LaunchID: "stale"}); done <- err }()
			<-entered
			// An idempotent Execute replay must not restart a session being stopped.
			_, replayErr := a.Execute(ctx, ExecuteRequest{LaunchID: "stale"})
			close(release)
			err := <-done
			if replayErr == nil {
				t.Fatal("replay admitted during cancellation")
			}
			l, readErr := store.Get(ctx, "stale")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if fail {
				if err == nil || l.State != LaunchStateExecuting || l.Error != "previous outcome" || l.EndedAt != nil {
					t.Fatalf("failed stop changed checkpoint: %+v %v", l, err)
				}
			} else if err != nil || l.State != LaunchStateCancelled || l.EndedAt == nil {
				t.Fatalf("stop not recorded: %+v %v", l, err)
			}
		})
	}
}

func TestInFlightExecutingCancelRejected(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.Create(ctx, &Launch{ID: "active", State: LaunchStatePrepared}); err != nil {
		t.Fatal(err)
	}
	a := &launchLifecycle{store: store}
	entered, release := make(chan struct{}), make(chan struct{})
	a.start = func(context.Context, *Launch) (string, LaunchState, error) {
		close(entered)
		<-release
		return "session", LaunchStateRunning, nil
	}
	a.stop = func(context.Context, *Launch) error { t.Error("stop during active creation"); return nil }
	done := make(chan error, 1)
	go func() { _, err := a.Execute(ctx, ExecuteRequest{LaunchID: "active"}); done <- err }()
	<-entered
	_, err := a.Cancel(ctx, CancelRequest{LaunchID: "active"})
	l, readErr := store.Get(ctx, "active")
	close(release)
	executeErr := <-done
	if err == nil || readErr != nil || l.State != LaunchStateExecuting || executeErr != nil {
		t.Fatalf("active cancellation: %+v %v %v %v", l, err, readErr, executeErr)
	}
}

func TestInterruptedExecuteCancelAfterStoreReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "launches.db")
	store, err := OpenLaunchStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, &Launch{ID: "interrupted", State: LaunchStatePrepared}); err != nil {
		t.Fatal(err)
	}
	before := &launchLifecycle{store: store, replaySafe: true}
	before.start = func(context.Context, *Launch) (string, LaunchState, error) {
		return "", LaunchStateExecuting, errors.New("ambiguous request")
	}
	l, err := before.Execute(ctx, ExecuteRequest{LaunchID: "interrupted"})
	if err != nil || l.State != LaunchStateExecuting {
		t.Fatalf("expected unresolved checkpoint: %+v %v", l, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenStore(t, path)
	after := &launchLifecycle{store: reopened}
	l, err = after.Cancel(ctx, CancelRequest{LaunchID: "interrupted"})
	if err != nil || l.State != LaunchStateCancelled || l.Error != "cancelled: provider outcome unknown; a session may exist" {
		t.Fatalf("restart resolution: %+v %v", l, err)
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

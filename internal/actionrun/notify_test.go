package actionrun

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestResultsAreDurableAndDeduplicatedAcrossSyncAndRestart(t *testing.T) {
	r := newRig(t, func(*fakeSession) {})
	ctx := context.Background()
	for _, state := range []string{StateSucceeded, StateFailed, StateRefused, StateDenied, StateExpired} {
		id := "run_" + state
		if err := r.svc.Store.Insert(ctx, Run{ID: id, ItemID: "item_" + state, Machine: MachineHub, Action: "tether.catalog.approve", Params: map[string]string{"plan_id": "already-approved"}, Argv: []string{"tether", "catalog", "approve", "already-approved"}, State: StatePending, CreatedAt: time.Now(), Requester: Requester{Agent: "chief", Label: "@chief", Task: "CW-20261115-0125"}}); err != nil {
			t.Fatal(err)
		}
		if err := r.svc.Store.Finish(ctx, id, StatePending, state, nil, "", "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	attempts := 0
	r.svc.Notify = func(context.Context, Run) error { attempts++; return errors.New("delivery unavailable") }
	if err := r.svc.Sync(ctx); err == nil {
		t.Fatal("failed delivery was not retained")
	}
	delivered := map[string]int{}
	r.svc.Notify = func(_ context.Context, run Run) error {
		delivered[run.ID]++
		if run.Requester.Task != "CW-20261115-0125" || run.ExitCode != nil {
			t.Fatalf("lost attribution or fabricated result: %+v", run)
		}
		return nil
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := r.svc.Sync(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	restarted := &Service{Store: r.svc.Store, Manager: r.svc.Manager, Notify: r.svc.Notify}
	if err := restarted.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 5 || attempts != 5 {
		t.Fatalf("deliveries %v attempts %d", delivered, attempts)
	}
	for _, n := range delivered {
		if n != 1 {
			t.Fatalf("duplicate delivery: %v", delivered)
		}
	}
	if r.tr.count() != 0 {
		t.Fatal("completion routing replayed a catalog action")
	}
}

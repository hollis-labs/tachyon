package dataverbs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

func TestNativeGateAuthorizationDeadlineWhileQueued(t *testing.T) {
	f := nativeStack(t)
	original := f.inbox.Native.Authorize
	var expire atomic.Bool
	type heldWrite struct {
		deadline time.Time
		done     <-chan error
	}
	held := make(chan heldWrite, 1)
	f.inbox.Native.Authorize = func(ctx context.Context, p approvals.ProposedResponse) (approvals.ResponseAuthorization, error) {
		a, err := original(ctx, p) // real separately authenticated callback
		if err != nil {
			return a, err
		}
		until := time.Now().Add(100 * time.Millisecond)
		if expire.Load() {
			until = a.ExpiresAt.Add(250 * time.Millisecond)
		}
		entered := make(chan struct{})
		done := make(chan error, 1)
		// This competing transaction must outlive the response's cancellation
		// to prove that no late queued CAS is left behind. Bound its own lifetime.
		holdCtx, stopHold := context.WithDeadline(context.WithoutCancel(ctx), until.Add(time.Second))
		go func() {
			defer stopHold()
			done <- f.inbox.Store.DB.Writer.Submit(holdCtx, "hold_native_response_writer", func(context.Context, store.Tx) error {
				close(entered)
				time.Sleep(time.Until(until))
				return nil
			})
		}()
		<-entered
		held <- heldWrite{deadline: a.ExpiresAt, done: done}
		return a, nil
	}
	for _, name := range []string{"valid_after_queue", "expired_while_queued"} {
		t.Run(name, func(t *testing.T) {
			expired := name == "expired_while_queued"
			expire.Store(expired)
			_, h := f.enqueue(name)
			status, body := f.do(f.owner, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
			write := <-held
			if err := <-write.done; err != nil {
				t.Fatal(err)
			}
			// Drain the actual writer after releasing it; a request returning early
			// on cancellation must not leave a queued terminal write behind it.
			if err := f.inbox.Store.DB.Writer.Submit(context.Background(), "native_response_barrier", func(context.Context, store.Tx) error { return nil }); err != nil {
				t.Fatal(err)
			}
			rec, err := f.inbox.Store.Get(context.Background(), h.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			if expired {
				if time.Now().Before(write.deadline) {
					t.Fatal("writer did not remain held through authorization expiry")
				}
				if status != 408 || rec.Outcome != nil || rec.State.IsTerminal() || rec.Revision != h.Revision {
					t.Fatalf("expired queued authorization: status=%d state=%s revision=%d outcome=%v body=%s", status, rec.State, rec.Revision, rec.Outcome, body)
				}
				// A refused queued choice must not become fresh authority evidence.
				f.want(f.anonymous, 409, "GET", gatePath("gate.assess-response", assessment(h)), nil, f.producer)
				return
			}
			if status != 200 || rec.Outcome == nil || rec.Revision != h.Revision+1 {
				t.Fatalf("valid queued authorization: status=%d state=%s revision=%d body=%s", status, rec.State, rec.Revision, body)
			}
			f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", assessment(h)), nil, f.producer)
		})
	}
}

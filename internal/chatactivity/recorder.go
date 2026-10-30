package chatactivity

import (
	"context"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Panels is the built-in allowlist an agent may open. Plugin panels need an explicit grant (not built).
var Panels = map[string]bool{"plan": true, "topics": true, "bookmarks": true, "work": true, "activity": true, "approvals": true, "files": true, "doc": true}

// PanelsPerMinute bounds agent panel opens per thread.
const PanelsPerMinute = 6

// Recorder writes activity rows and tells the thread's stream. Publish may be nil.
type Recorder struct {
	Store   *Store
	Publish func(ctx context.Context, thread, typ string, fields map[string]any)

	mu     sync.Mutex
	opened map[string][]time.Time
}

// Record appends the row and publishes an `activity` event. A failure to append is returned; a
// failure to publish never is (events are signals, the row is the record).
func (r *Recorder) Record(ctx context.Context, e Entry) (Entry, error) {
	ctx = context.WithoutCancel(ctx)
	e, err := r.Store.Append(ctx, e)
	if err != nil {
		return e, err
	}
	if r.Publish != nil {
		f := map[string]any{"seq": e.Seq, "activity_type": e.Type, "actor": e.Actor, "cite": e.Cite, "source": e.Source}
		if len(e.Payload) > 0 {
			f["payload"] = e.Payload
		}
		r.Publish(ctx, e.ThreadRef, chatapi.EventActivity, f)
	}
	return e, nil
}

// Quiet records an activity row from a service seam where a failure must not break the caller.
func (r *Recorder) Quiet(ctx context.Context, thread, typ, actor, source string, cite []Cite, payload []byte) {
	_, _ = r.Record(ctx, Entry{ThreadRef: thread, Type: typ, Actor: actor, Source: source, Cite: cite, Payload: payload})
}

// OpenPanel signals the GUI to open a panel. It is rate limited per thread; the server never
// remembers it (the UI owns the manual-lock rule) and the GUI must work if the event is dropped.
func (r *Recorder) OpenPanel(ctx context.Context, thread, panel, ref, reason, actor, source string) error {
	if !Panels[panel] || len(ref) > 200 {
		return ErrInvalid
	}
	if !r.Allow("panel:"+thread, PanelsPerMinute) {
		return ErrRateLimited
	}
	reason = clip(reason, 200)
	if r.Publish != nil {
		f := map[string]any{"panel": panel, "reason": reason, "source": actorKind(source)}
		if ref != "" {
			f["ref"] = ref
		}
		r.Publish(context.WithoutCancel(ctx), thread, chatapi.EventPanel, f)
	}
	r.Quiet(ctx, thread, "panel.opened", actor, source, nil, Card("Opened "+panel, reason, nil))
	return nil
}

func actorKind(source string) string {
	if len(source) >= 6 && source[:6] == "agent:" {
		return "agent"
	}
	return "system"
}

// Allow reports whether key may act n times per minute, and counts this attempt when it may.
func (r *Recorder) Allow(key string, n int) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.opened == nil {
		r.opened = map[string][]time.Time{}
	}
	kept := r.opened[key][:0]
	for _, t := range r.opened[key] {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n {
		r.opened[key] = kept
		return false
	}
	r.opened[key] = append(kept, now)
	return true
}

// SetContext stores the runtime's report and publishes a `context` event.
func (r *Recorder) SetContext(ctx context.Context, thread string, used, window int64, model string) {
	ctx = context.WithoutCancel(ctx)
	c, err := r.Store.SaveContext(ctx, thread, used, window, model)
	if err != nil || r.Publish == nil {
		return
	}
	r.Publish(ctx, thread, chatapi.EventContext, map[string]any{"used": c.Used, "window": c.Window, "model": c.Model})
}

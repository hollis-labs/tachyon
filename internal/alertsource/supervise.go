package alertsource

import (
	"context"
	"sync"
	"time"
)

// SourceHealth feeds GET /alerts/sources.
type SourceHealth struct {
	Source      string `json:"source"`
	State       string `json:"state"` // ok | degraded | down
	LastEventMS int64  `json:"last_event_ms"`
	Note        string `json:"note"`
}

// Health is the registry of source states. A nil *Health is a no-op, so adapters need no checks.
type Health struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*SourceHealth
}

func NewHealth() *Health { return &Health{now: time.Now, m: map[string]*SourceHealth{}} }

func (h *Health) get(src string) *SourceHealth {
	if h.m[src] == nil {
		h.m[src] = &SourceHealth{Source: src, State: "down"}
	}
	return h.m[src]
}

func (h *Health) Set(src, state, note string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.get(src)
	s.State, s.Note = state, note
}

// Seen records that an event arrived from src.
func (h *Health) Seen(src string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.get(src).LastEventMS = h.now().UnixMilli()
}

func (h *Health) Snapshot() []SourceHealth {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SourceHealth, 0, len(h.m))
	for _, s := range h.m {
		out = append(out, *s)
	}
	return out
}

// Supervise runs fn until ctx ends, restarting after a failure with exponential backoff (1s doubling to max;
// reset after a run that lasted over a minute). A panic in fn counts as a failure. The source is "down" with
// the error as note while it is failing, so the UI can say "Alerts are not updating".
func Supervise(ctx context.Context, name string, h *Health, max time.Duration, fn func(context.Context) error) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = panicErr{r}
				}
			}()
			return fn(ctx)
		}()
		if ctx.Err() != nil {
			return
		}
		note := "stream ended"
		if err != nil {
			note = err.Error()
		}
		h.Set(name, "down", note)
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > max {
			backoff = max
		}
	}
}

type panicErr struct{ v any }

func (p panicErr) Error() string { return "adapter panicked" }

// Limiter allows burst events per source per minute (design: 200); the rest are dropped and counted by the
// caller through Sink.Suppressed.
type Limiter struct {
	mu     sync.Mutex
	now    func() time.Time
	Burst  int
	window map[string][]time.Time
}

func NewLimiter(burst int) *Limiter {
	return &Limiter{now: time.Now, Burst: burst, window: map[string][]time.Time{}}
}

func (l *Limiter) Allow(src string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.window[src]
	i := 0
	for i < len(w) && now.Sub(w[i]) >= time.Minute {
		i++
	}
	w = w[i:]
	if len(w) >= l.Burst {
		l.window[src] = w
		return false
	}
	l.window[src] = append(w, now)
	return true
}

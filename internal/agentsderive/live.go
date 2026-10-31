package agentsderive

import (
	"context"
	"sync"
	"time"
)

// Live keeps each session's in-memory meter and last tool, reduces every output payload, and saves
// the derived fields through Save (at most once a second per session). It implements the collector's
// OutputSink. Nothing from a payload is kept except what ExtractOutput returns.
type Live struct {
	Save func(ctx context.Context, sessionID string, d Saved) error

	mu       sync.Mutex
	sessions map[string]*liveSession
}

// Saved is the derived snapshot handed to Save.
type Saved struct {
	OneLiner    *OneLiner
	CallsInTurn int
	CallsDelta  int    // tool calls seen since the last save; the store adds them to the session total
	TokDelta    *Usage // tokens of the API calls finished since the last save; nil when none were seen
	Meter       Meter
	At          time.Time
}

type liveSession struct {
	meter   Meter
	last    *OneLiner
	calls   int
	unsaved int
	tokens  *Usage
	savedAt time.Time
}

// maxLive bounds the map: a session idle longest is dropped first when it is exceeded.
const maxLive = 2000

func (l *Live) Output(ctx context.Context, id string, at time.Time, payload []byte) {
	e := ExtractOutput(payload)
	if len(e.Tools) == 0 && e.Usage == nil && e.Window == 0 && e.Compact == nil {
		return
	}
	l.mu.Lock()
	if l.sessions == nil {
		l.sessions = map[string]*liveSession{}
	}
	s := l.sessions[id]
	if s == nil {
		if len(l.sessions) >= maxLive {
			var oldest string
			var ot time.Time
			for k, v := range l.sessions {
				if oldest == "" || v.savedAt.Before(ot) {
					oldest, ot = k, v.savedAt
				}
			}
			delete(l.sessions, oldest)
		}
		s = &liveSession{}
		l.sessions[id] = s
	}
	for i := range e.Tools {
		t := e.Tools[i]
		s.last = &t
		s.calls++
		s.unsaved++
	}
	if e.Billed != nil {
		if s.tokens == nil {
			s.tokens = &Usage{}
		}
		s.tokens.Input += e.Billed.Input
		s.tokens.Output += e.Billed.Output
		s.tokens.CacheRead += e.Billed.CacheRead
		s.tokens.CacheWrite += e.Billed.CacheWrite
	}
	s.meter.Observe(e, at)
	if !s.savedAt.IsZero() && at.Sub(s.savedAt) < time.Second { // throttled; the next output saves the newer figures
		l.mu.Unlock()
		return
	}
	s.savedAt = at
	snap := Saved{CallsInTurn: s.calls, CallsDelta: s.unsaved, Meter: s.meter, At: at}
	snap.TokDelta, s.tokens = s.tokens, nil
	s.unsaved = 0
	snap.Meter.Compactions = append([]Marker(nil), s.meter.Compactions...)
	if s.last != nil {
		o := *s.last
		snap.OneLiner = &o
	}
	l.mu.Unlock()
	if l.Save != nil {
		_ = l.Save(ctx, id, snap)
	}
}

// EndTurn resets the per-turn call counter (the collector calls it on a work_state change to idle).
func (l *Live) EndTurn(id string) {
	l.mu.Lock()
	if s := l.sessions[id]; s != nil {
		s.calls = 0
	}
	l.mu.Unlock()
}

package agentscollector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	queueSize    = 4096
	batchEvery   = time.Second
	batchMax     = 500
	watchdog     = 60 * time.Second
	backfillPage = 200
	backfillMax  = 20 // pages per attempt; the rest stays an open gap
	pruneEvery   = 10 * time.Minute
	offlineGap   = 2 * time.Minute // a cursor older than this at start means Tachyon was away
)

// Collector follows the source and keeps the read model current. Run it once; it survives Tether
// restarts, stream gaps and its own restarts without losing lifecycle events.
type Collector struct {
	Source Source
	Store  *Store
	Now    func() time.Time
	// Backoff bounds (defaults 3 s doubling to 1 min, reset on a successful stream).
	BackoffMin, BackoffMax time.Duration
	Watchdog               time.Duration
	// Sink, if set, receives session.output payloads in memory (A2 derivation). Nil discards them.
	Sink OutputSink
	// TurnEnded, if set, is told when a session returns to idle (resets per-turn counters).
	TurnEnded  func(sessionID string)
	PruneEvery time.Duration

	mu       sync.Mutex
	src      SourceStatus
	outputs  atomic.Int64
	kindMu   sync.Mutex
	kinds    map[string]int64 // frames seen by kind since start (names and counts only)
	dropped  atomic.Int64
	streamed atomic.Bool
	resync   atomic.Bool // a gap frame arrived: the next pass re-seeds from the snapshot instead of resuming the old cursor
	done     chan struct{}
}

// SourceStatus is the Tether source's health for the status route.
type SourceStatus struct {
	State  string // streaming | polling | unreachable | off
	Detail string
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Collector) setSource(state, detail string) {
	c.mu.Lock()
	c.src = SourceStatus{State: state, Detail: detail}
	c.mu.Unlock()
}

// Start runs the collector in the background; Wait returns once it has stopped.
func (c *Collector) Start(ctx context.Context) {
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		c.Run(ctx)
	}()
}

// Wait blocks until a collector started with Start has stopped, or ctx ends.
func (c *Collector) Wait(ctx context.Context) {
	if c.done == nil {
		return
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

// Run blocks until ctx ends. Errors are logged at warn level and retried with backoff; a panic in
// one pass is recovered (without dumping payloads) and treated as a failed pass.
func (c *Collector) Run(ctx context.Context) {
	lo, hi := c.BackoffMin, c.BackoffMax
	if lo <= 0 {
		lo = 3 * time.Second
	}
	if hi <= 0 {
		hi = time.Minute
	}
	wait := lo
	go c.retention(ctx)
	for ctx.Err() == nil {
		started := c.now()
		err := c.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		c.streamed.Store(false)
		c.setSource("unreachable", errText(err))
		slog.Warn("agents collector pass ended", "err", errText(err))
		if c.now().Sub(started) > 2*lo {
			wait = lo // it ran a while: this is a fresh failure, not a crash loop
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, hi)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	if len(err.Error()) > 200 {
		return err.Error()[:200]
	}
	return err.Error()
}

func (c *Collector) retention(ctx context.Context) {
	every := c.PruneEvery
	if every <= 0 {
		every = pruneEvery
	}
	for {
		if _, err := c.Store.Prune(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("agents retention failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// pass is one connection's life: bootstrap, backfill, then stream until it breaks.
func (c *Collector) pass(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("collector panic recovered (%T)", r) // the value may hold payload text: not printed
		}
	}()
	st, err := c.Store.State(ctx)
	if err != nil {
		return err
	}
	cursor := st.LastSeq
	if cursor == 0 || c.resync.Swap(false) {
		if cursor, err = c.bootstrap(ctx); err != nil {
			return err
		}
	} else if !st.LastEventAt.IsZero() && c.now().Sub(st.LastEventAt) > offlineGap {
		// Tachyon (or Tether) was away: record the hole, then try to fill it from history.
		gap := Gap{Start: st.LastEventAt, End: c.now(), Reason: "tachyon_offline", FromSeq: st.LastSeq}
		if cursor, err = c.fill(ctx, gap); err != nil {
			return err
		}
	}
	return c.follow(ctx, cursor)
}

// bootstrap seeds the sessions and the cursor. Strategy A is the environment snapshot; any failure
// falls back to B (list the running sessions, start from the newest event). Which one ran is recorded.
func (c *Collector) bootstrap(ctx context.Context) (int64, error) {
	snap, err := c.Source.Snapshot(ctx)
	if err == nil {
		if err := c.Store.SetStrategy(ctx, "snapshot"); err != nil {
			return 0, err
		}
		if err := c.Store.Apply(ctx, snap.Sessions); err != nil {
			return 0, err
		}
		return snap.HighWater, c.Store.Reseed(ctx, snap.Env, snap.HighWater)
	}
	slog.Info("agents collector: snapshot unavailable, using the session list", "err", errText(err))
	sessions, serr := c.Source.Sessions(ctx)
	if serr != nil {
		return 0, serr
	}
	head, herr := c.Source.Head(ctx)
	if herr != nil {
		return 0, herr
	}
	if err := c.Store.SetStrategy(ctx, "fallback"); err != nil {
		return 0, err
	}
	if err := c.Store.Apply(ctx, sessions); err != nil {
		return 0, err
	}
	return head, c.Store.Reseed(ctx, "", head)
}

// fill records the gap, then pages history newest first down to the gap's start (bounded). A gap
// that cannot be filled completely stays open and is reported; it is never papered over.
func (c *Collector) fill(ctx context.Context, g Gap) (int64, error) {
	id, err := c.Store.AddGap(ctx, g)
	if err != nil {
		return 0, err
	}
	var all []Frame
	var before int64
	complete := false
	for page := 0; page < backfillMax; page++ {
		frames, next, err := c.Source.History(ctx, g.FromSeq, before, backfillPage)
		if err != nil {
			return g.FromSeq, err
		}
		all = append(all, frames...)
		if next == 0 || next <= g.FromSeq || len(frames) == 0 {
			complete = true
			break
		}
		before = next
	}
	// Oldest first so state facts apply in order.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	for i := 0; i < len(all); i += batchMax {
		if err := c.Store.Apply(ctx, all[i:min(i+batchMax, len(all))]); err != nil {
			return 0, err
		}
	}
	if complete {
		if err := c.Store.MarkBackfilled(ctx, id); err != nil {
			return 0, err
		}
		st, err := c.Store.State(ctx)
		return st.LastSeq, err
	}
	// Not complete: the newest page is stored, the middle is the hole. Resume from the newest seq.
	st, err := c.Store.State(ctx)
	return st.LastSeq, err
}

// follow streams live frames. A reader goroutine feeds a bounded queue and a writer batches into
// the store once a second. When the writer lags, session.output is only counted (never queued);
// lifecycle, approval and state events always wait for room.
func (c *Collector) follow(ctx context.Context, after int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wd := c.Watchdog
	if wd <= 0 {
		wd = watchdog
	}
	var lastAlive atomic.Int64
	lastAlive.Store(time.Now().UnixNano())
	alive := func() { lastAlive.Store(time.Now().UnixNano()) }
	go func() {
		t := time.NewTicker(wd / 4)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastAlive.Load())) > wd {
					slog.Warn("agents collector: no frame or keepalive, reconnecting")
					cancel()
					return
				}
			}
		}
	}()

	queue := make(chan Frame, queueSize)
	touch := map[string]time.Time{}
	var tmu sync.Mutex
	var touchSeq int64
	writerDone := make(chan error, 1)
	go func() { writerDone <- c.write(ctx, queue, touch, &tmu, &touchSeq) }()

	c.setSource("streaming", "")
	c.streamed.Store(true)
	err := c.Source.Stream(ctx, after, func(f Frame) error {
		alive()
		c.countKind(frameTag(f))
		switch {
		case f.Output:
			c.outputs.Add(1)
			if c.Sink != nil && f.SessionID != "" && len(f.Payload) > 0 {
				at := f.At
				if at.IsZero() {
					at = c.now()
				}
				c.Sink.Output(ctx, f.SessionID, at, f.Payload)
			}
			f.Payload = nil
			if f.SessionID != "" {
				at := f.At
				if at.IsZero() {
					at = c.now()
				}
				tmu.Lock()
				touch[f.SessionID] = at
				touchSeq = max(touchSeq, f.Seq)
				tmu.Unlock()
			}
			return nil
		case f.Gap:
			now := c.now()
			if _, err := c.Store.AddGap(ctx, Gap{Start: now, End: now, Reason: gapReason(f.Reason), FromSeq: after}); err != nil {
				return err
			}
			c.resync.Store(true)
			return errors.New("stream reported a gap; resyncing")
		case f.Synchronized:
			return nil
		}
		if c.TurnEnded != nil && f.Kind == "session.work_state_changed" && f.WorkState == "idle" && f.SessionID != "" {
			c.TurnEnded(f.SessionID)
		}
		select {
		case queue <- f:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}, alive)
	cancel()
	close(queue)
	if werr := <-writerDone; werr != nil && err == nil {
		err = werr
	}
	return err
}

func gapReason(r string) string {
	switch r {
	case "ahead", "purged", "too_large", "restart", "event_id_invariant", "backfill_in_progress":
		return r
	}
	return "tether_unreachable"
}

func (c *Collector) write(ctx context.Context, queue <-chan Frame, touch map[string]time.Time, tmu *sync.Mutex, touchSeq *int64) error {
	var batch []Frame
	flush := func() error {
		fctx := context.WithoutCancel(ctx) // finish what was read even while shutting down
		fctx, cancel := context.WithTimeout(fctx, 15*time.Second)
		defer cancel()
		tmu.Lock()
		t := make(map[string]time.Time, len(touch))
		for k, v := range touch {
			t[k] = v
			delete(touch, k)
		}
		seq := *touchSeq
		*touchSeq = 0
		tmu.Unlock()
		if err := c.Store.Apply(fctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return c.Store.Touch(fctx, t, seq)
	}
	tick := time.NewTicker(batchEvery)
	defer tick.Stop()
	for {
		select {
		case f, ok := <-queue:
			if !ok {
				return flush()
			}
			batch = append(batch, f)
			if len(batch) >= batchMax {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-tick.C:
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// frameTag names a frame kind with why it may not become a timeline row: an app publisher ("@app") or no session
// ("!sid"). It exists so an empty events table can be told apart from frames that were skipped.
func frameTag(f Frame) string {
	k := f.Kind
	if f.App != "" && !f.Output {
		k += "@" + f.App
	}
	if f.SessionID == "" && !f.Output {
		k += "!sid"
	}
	return k
}

// countKind keeps a bounded tally of frame kinds so an empty timeline can be told apart from an empty stream.
func (c *Collector) countKind(kind string) {
	c.kindMu.Lock()
	defer c.kindMu.Unlock()
	if c.kinds == nil {
		c.kinds = map[string]int64{}
	}
	if _, ok := c.kinds[kind]; ok || len(c.kinds) < 40 {
		c.kinds[kind]++
	}
}

func (c *Collector) kindCounts() map[string]int64 {
	c.kindMu.Lock()
	defer c.kindMu.Unlock()
	out := make(map[string]int64, len(c.kinds))
	for k, v := range c.kinds {
		out[k] = v
	}
	return out
}

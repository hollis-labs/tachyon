// Package ceremony is the ceremony meter (CW-20261114-0023f; design v2: "measure ceremony weekly; alert when
// activity rises and progress doesn't"). Over a rolling 7 days it reports:
//   - gates: tasks entering review, Tachyon approvals created, Torque checkpoints emitted, typed asks to @chrispian;
//   - median wait: how long gates closed in the window waited (review to its next state, approval created to
//     decided, checkpoint pending to responded or canceled);
//   - stalled: Torque tasks in doing, review or blocked with no update for 24h (no-update stands in for
//     "no driver" until Torque has a driver field), and the oldest pending checkpoints;
//   - activity vs progress: agent events collected against tasks closed done, compared with the same 7 days a
//     week ago from a daily snapshot; a regression is activity up 25% or more with done not up.
package ceremony

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	Window       = 7 * 24 * time.Hour
	StalledAfter = 24 * time.Hour
	listLimit    = 20 // stalled tasks and pending checkpoints shown, oldest first
)

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Checkpoint struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	EmittedAt time.Time `json:"emitted_at"`
}

// CheckpointEvent is one Torque checkpoint.state_changed from Tether's bus.
type CheckpointEvent struct {
	ID    string
	State string // pending, responded, canceled, ...
	At    time.Time
}

type Torque interface {
	Stalled(ctx context.Context, before time.Time, limit int) ([]Task, int, error)
	DoneSince(ctx context.Context, since time.Time) (int, error)
	PendingCheckpoints(ctx context.Context) ([]Checkpoint, error)
}

// CheckpointHistory is the recent checkpoint transitions (slow upstream, about 2 minutes on agent-os: callers cache it).
type CheckpointHistory interface {
	CheckpointEvents(ctx context.Context) ([]CheckpointEvent, error)
}

// Asks counts typed asks to the operator created since a time (needs-you's mailbox read).
type Asks interface {
	AsksSince(ctx context.Context, since time.Time) (int, error)
}

type Gates struct {
	Total       int `json:"total"`
	Reviews     int `json:"reviews"`
	Approvals   int `json:"approvals"`
	Checkpoints int `json:"checkpoints"`
	Asks        int `json:"asks"`
}

type Snapshot struct {
	Day      string `json:"day"`
	Activity int    `json:"activity"`
	Done     int    `json:"done"`
	Gates    int    `json:"gates"`
}

type SourceState struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Meter is the read.
type Meter struct {
	CheckedAt          time.Time              `json:"checked_at"`
	WindowDays         int                    `json:"window_days"`
	Gates              Gates                  `json:"gates"`
	MedianWaitSeconds  *int64                 `json:"median_wait_seconds"` // null when no gate closed in the window
	WaitSamples        int                    `json:"wait_samples"`
	Stalled            []Task                 `json:"stalled"`
	StalledTotal       int                    `json:"stalled_total"`
	PendingCheckpoints []Checkpoint           `json:"pending_checkpoints"`
	PendingTotal       int                    `json:"pending_checkpoints_total"`
	Activity           int                    `json:"activity"`
	Done               int                    `json:"done"`
	Previous           *Snapshot              `json:"previous"`   // the same figures a week ago; null until 7 days of snapshots
	Comparison         string                 `json:"comparison"` // pending | steady | regression
	Sources            map[string]SourceState `json:"sources"`
}

type Meterer struct {
	DB      *localstore.Store
	Torque  Torque
	History CheckpointHistory
	Asks    Asks
	Now     func() time.Time
}

func (m *Meterer) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now().UTC()
}

func set(src map[string]SourceState, name string, err error) {
	if err != nil {
		src[name] = SourceState{Error: err.Error()}
	} else {
		src[name] = SourceState{OK: true}
	}
}

// Read computes the meter and records today's snapshot.
func (m *Meterer) Read(ctx context.Context) Meter {
	now := m.now()
	since := now.Add(-Window)
	out := Meter{CheckedAt: now, WindowDays: 7, Stalled: []Task{}, PendingCheckpoints: []Checkpoint{}, Comparison: "pending", Sources: map[string]SourceState{}}
	var waits []time.Duration

	rev, w, err := m.reviews(ctx, since)
	set(out.Sources, "reviews", err)
	out.Gates.Reviews, waits = rev, append(waits, w...)

	ap, w, err := m.approvals(ctx, since)
	set(out.Sources, "approvals", err)
	out.Gates.Approvals, waits = ap, append(waits, w...)

	if m.History != nil {
		evs, err := m.History.CheckpointEvents(ctx)
		set(out.Sources, "checkpoint_history", err)
		n, w := checkpointGates(evs, since)
		out.Gates.Checkpoints, waits = n, append(waits, w...)
	} else {
		set(out.Sources, "checkpoint_history", errUnavailable)
	}
	if m.Asks != nil {
		n, err := m.Asks.AsksSince(ctx, since)
		set(out.Sources, "asks", err)
		out.Gates.Asks = n
	} else {
		set(out.Sources, "asks", errUnavailable)
	}
	out.Gates.Total = out.Gates.Reviews + out.Gates.Approvals + out.Gates.Checkpoints + out.Gates.Asks
	if med, ok := median(waits); ok {
		s := int64(med / time.Second)
		out.MedianWaitSeconds = &s
	}
	out.WaitSamples = len(waits)

	if m.Torque != nil {
		st, total, err := m.Torque.Stalled(ctx, now.Add(-StalledAfter), listLimit)
		set(out.Sources, "stalled", err)
		sort.Slice(st, func(i, j int) bool { return st[i].UpdatedAt.Before(st[j].UpdatedAt) })
		if len(st) > listLimit {
			st = st[:listLimit]
		}
		if st != nil {
			out.Stalled = st
		}
		out.StalledTotal = total
		cps, err := m.Torque.PendingCheckpoints(ctx)
		set(out.Sources, "pending_checkpoints", err)
		out.PendingTotal = len(cps)
		sort.Slice(cps, func(i, j int) bool { return cps[i].EmittedAt.Before(cps[j].EmittedAt) })
		if len(cps) > listLimit {
			cps = cps[:listLimit]
		}
		if cps != nil {
			out.PendingCheckpoints = cps
		}
		done, err := m.Torque.DoneSince(ctx, since)
		set(out.Sources, "done", err)
		out.Done = done
	} else {
		for _, k := range []string{"stalled", "pending_checkpoints", "done"} {
			set(out.Sources, k, errUnavailable)
		}
	}

	act, err := m.activity(ctx, since)
	set(out.Sources, "activity", err)
	out.Activity = act

	// Snapshot today only when every figure it keeps was read, so a failed read never becomes history.
	whole := out.Sources["activity"].OK && out.Sources["done"].OK && out.Sources["reviews"].OK && out.Sources["approvals"].OK
	if whole {
		_ = m.snapshot(ctx, now, Snapshot{Activity: out.Activity, Done: out.Done, Gates: out.Gates.Total})
	}
	if prev, ok := m.previous(ctx, now); ok {
		out.Previous = &prev
		out.Comparison = Compare(prev, Snapshot{Activity: out.Activity, Done: out.Done})
	}
	return out
}

// Compare: a regression is activity up 25% or more while done did not go up.
func Compare(prev, cur Snapshot) string {
	if prev.Activity > 0 && cur.Activity*4 >= prev.Activity*5 && cur.Done <= prev.Done {
		return "regression"
	}
	return "steady"
}

type errString string

func (e errString) Error() string { return string(e) }

const errUnavailable = errString("not configured")

func median(ds []time.Duration) (time.Duration, bool) {
	if len(ds) == 0 {
		return 0, false
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2], true
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2, true
}

// reviews counts tasks entering review in the window, and the wait of each review left in the window (its next
// state event), from the collector's task events (kept 7 days).
func (m *Meterer) reviews(ctx context.Context, since time.Time) (int, []time.Duration, error) {
	if m.DB == nil {
		return 0, nil, errUnavailable
	}
	rows, err := m.DB.Reader.QueryContext(ctx, `SELECT at_ns, subject, refs_json FROM bus_events WHERE type='task' ORDER BY at_ns, tether_seq`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	type ev struct {
		at    time.Time
		state string
	}
	byTask := map[string][]ev{}
	for rows.Next() {
		var ns int64
		var subject, refs string
		if err := rows.Scan(&ns, &subject, &refs); err != nil {
			return 0, nil, err
		}
		var rs []struct{ Kind, Ref string }
		_ = json.Unmarshal([]byte(refs), &rs)
		task := ""
		for _, r := range rs {
			if r.Kind == "task" {
				task = r.Ref
			}
		}
		if task == "" {
			continue
		}
		state := strings.TrimSpace(strings.TrimPrefix(subject, task))
		byTask[task] = append(byTask[task], ev{time.Unix(0, ns).UTC(), state})
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	n := 0
	var waits []time.Duration
	for _, evs := range byTask {
		for i, e := range evs {
			if e.state != "review" {
				continue
			}
			if !e.at.Before(since) {
				n++
			}
			for _, next := range evs[i+1:] {
				if next.state != "review" {
					if !next.at.Before(since) {
						waits = append(waits, next.at.Sub(e.at))
					}
					break
				}
			}
		}
	}
	return n, waits, nil
}

// approvals counts Tachyon approvals created in the window and the wait of those decided in it.
func (m *Meterer) approvals(ctx context.Context, since time.Time) (int, []time.Duration, error) {
	if m.DB == nil {
		return 0, nil, errUnavailable
	}
	var n int
	if err := m.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_item_index WHERE created_ns>=?`, since.UnixNano()).Scan(&n); err != nil {
		return 0, nil, err
	}
	rows, err := m.DB.Reader.QueryContext(ctx, `SELECT created_ns, decided_ns FROM approval_item_index WHERE decided_ns IS NOT NULL AND decided_ns>=?`, since.UnixNano())
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var waits []time.Duration
	for rows.Next() {
		var c, d int64
		if rows.Scan(&c, &d) == nil && d >= c {
			waits = append(waits, time.Duration(d-c))
		}
	}
	return n, waits, rows.Err()
}

// checkpointGates counts checkpoints emitted (to pending) in the window, and the wait of each that left pending
// (responded or canceled) in it.
func checkpointGates(evs []CheckpointEvent, since time.Time) (int, []time.Duration) {
	sort.Slice(evs, func(i, j int) bool { return evs[i].At.Before(evs[j].At) })
	opened := map[string]time.Time{}
	n := 0
	var waits []time.Duration
	for _, e := range evs {
		switch e.State {
		case "pending":
			opened[e.ID] = e.At
			if !e.At.Before(since) {
				n++
			}
		default:
			if at, ok := opened[e.ID]; ok {
				if !e.At.Before(since) {
					waits = append(waits, e.At.Sub(at))
				}
				delete(opened, e.ID)
			}
		}
	}
	return n, waits
}

// activity is the agent events collected in the window: session events (turns, tool calls, messages) and mail.
func (m *Meterer) activity(ctx context.Context, since time.Time) (int, error) {
	if m.DB == nil {
		return 0, errUnavailable
	}
	var a, b int
	if err := m.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents_events WHERE at_ns>=?`, since.UnixNano()).Scan(&a); err != nil {
		return 0, err
	}
	if err := m.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM bus_events WHERE type='message' AND at_ns>=?`, since.UnixNano()).Scan(&b); err != nil {
		return 0, err
	}
	return a + b, nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

func (m *Meterer) snapshot(ctx context.Context, now time.Time, s Snapshot) error {
	return m.DB.Writer.Submit(ctx, "ceremony_daily", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ceremony_daily(day,activity,done,gates,computed_ns) VALUES(?,?,?,?,?)
ON CONFLICT(day) DO UPDATE SET activity=excluded.activity,done=excluded.done,gates=excluded.gates,computed_ns=excluded.computed_ns`,
			day(now), s.Activity, s.Done, s.Gates, now.UnixNano())
		return err
	})
}

func (m *Meterer) previous(ctx context.Context, now time.Time) (Snapshot, bool) {
	if m.DB == nil {
		return Snapshot{}, false
	}
	s := Snapshot{Day: day(now.Add(-Window))}
	err := m.DB.Reader.QueryRowContext(ctx, `SELECT activity, done, gates FROM ceremony_daily WHERE day=?`, s.Day).Scan(&s.Activity, &s.Done, &s.Gates)
	return s, err == nil
}

// CachedHistory serves a slow CheckpointHistory from a cache refreshed in the background.
type CachedHistory struct {
	Source CheckpointHistory
	Every  time.Duration

	mu   sync.Mutex
	evs  []CheckpointEvent
	err  error
	have bool
}

// Run refreshes until ctx ends.
func (c *CachedHistory) Run(ctx context.Context) {
	every := c.Every
	if every <= 0 {
		every = 30 * time.Minute
	}
	for {
		rctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
		evs, err := c.Source.CheckpointEvents(rctx)
		cancel()
		c.mu.Lock()
		if err == nil {
			c.evs, c.have = evs, true
		}
		c.err = err
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (c *CachedHistory) CheckpointEvents(context.Context) ([]CheckpointEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.have {
		if c.err != nil {
			return nil, c.err
		}
		return nil, errString("checkpoint history not loaded yet")
	}
	return append([]CheckpointEvent(nil), c.evs...), nil
}

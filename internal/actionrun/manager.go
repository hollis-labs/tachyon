package actionrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/user"
	"sync"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// Frame is one event on a live run.
type Frame struct {
	Output []byte // terminal output
	Exit   *int   // set once, when the process ends
}

// live is a started process and its subscribers.
type live struct {
	mu          sync.Mutex
	sess        Session
	replay      ring
	subs        map[chan Frame]struct{}
	done        bool
	exit        int
	trans       transcript
	cancel      context.CancelFunc // ends the run context: kills the process tree
	interactive bool
	reason      string      // why the manager ended the run, if it did
	idle        *time.Timer // pending kill after the last viewer left
}

// Defaults for the lifetime and resource policy.
const (
	DefaultMaxRuntime    = 10 * time.Minute
	DefaultMaxConcurrent = 4
	// initialAttach is how long a started interactive run may wait for its first viewer.
	initialAttach = 15 * time.Second
)

// ErrBusy is returned by Start when the concurrency bound is reached; the run
// stays approved and is retried.
var ErrBusy = errors.New("too many action runs are active; it will start when one finishes")

// Manager owns started runs. Starting is the only place a command executes,
// and it re-derives the command from the registry before doing so.
type Manager struct {
	Store      *Store
	Items      ItemReader
	Transports map[string]Transport
	Clock      func() time.Time
	// Base is the parent of every run context (shutdown ends the runs); nil is Background.
	Base context.Context
	// MaxRuntime ends any run still going after this long (default 10m).
	MaxRuntime time.Duration
	// MaxConcurrent bounds simultaneously active runs (default 4).
	MaxConcurrent int
	// DetachGrace is how long an interactive run survives with no terminal
	// attached before it is killed. Zero (the default) kills it as soon as the
	// last terminal closes, so a dropped connection never leaves a live pty.
	DetachGrace time.Duration
	// Labels, when set, replaces the Cerberus label lookup (tests).
	Labels func(ctx context.Context, machine, resource string) (Labels, error)

	mu       sync.Mutex
	runs     map[string]*live
	starting int
}

// ItemReader looks up the approval item behind a run.
type ItemReader interface {
	Get(ctx context.Context, itemID string) (hitl.Record, error)
}

func (m *Manager) now() time.Time {
	if m.Clock != nil {
		return m.Clock().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) lookup(id string) *live {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[id]
}

// Recover fails runs that were mid-flight when the process stopped; their
// terminals cannot be reattached after a restart.
func (m *Manager) Recover(ctx context.Context) error {
	open, err := m.Store.Open(ctx)
	if err != nil {
		return err
	}
	for _, r := range open {
		if r.State == StateRunning || r.State == StateApproved {
			from := r.State
			if err := m.Store.Finish(ctx, r.ID, from, StateFailed, nil, r.Output, "interrupted by a Tachyon restart", m.now()); err != nil {
				return err
			}
			slog.Warn("action run interrupted by restart", "run_id", r.ID, "action", r.Action, "machine", r.Machine)
		}
	}
	return nil
}

// approved reports whether the item behind a run was approved, and returns its
// snapshot's op_digest.
func approvedDigest(rec hitl.Record) (approved bool, digest string) {
	res, ok := rec.Outcome.(hitl.Resolved)
	if !ok {
		return false, ""
	}
	approved = res.Resolution.Response.Decision == "approved"
	var req hitl.EnqueueRequest
	if err := req.UnmarshalJSON(rec.Request); err == nil {
		digest = extraString(req, "op_digest")
	}
	return approved, digest
}

// Start runs an approved action. It refuses unless the approval item is
// approved and the digest recomputed from the stored request equals both the
// stored and the approved digest. cols/rows size the terminal.
func (m *Manager) Start(ctx context.Context, runID string, cols, rows uint16) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, err := m.Store.Get(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.State != StateWaitingInput && run.State != StateApproved {
		return run, fmt.Errorf("run is %s; it starts only from an approved, not yet started state", run.State)
	}
	rec, err := m.Items.Get(ctx, run.ItemID)
	if err != nil {
		return run, err
	}
	ok, approvedDigestValue := approvedDigest(rec)
	if !ok {
		return run, errors.New("approval is not an approved decision")
	}
	spec, _, rerr := Resolve(run.Machine, run.Action, run.Params)
	switch {
	case rerr != nil:
		return m.refuse(ctx, run, "the registry no longer allows this request: "+rerr.Error())
	case spec.Digest() != run.OpDigest || spec.Digest() != approvedDigestValue:
		return m.refuse(ctx, run, "op_digest mismatch: the request that runs is not the request that was approved")
	case !equalArgv(spec.Argv, run.Argv):
		return m.refuse(ctx, run, "stored argv differs from the registry's argv")
	}
	// Approval can wait; the target may have been relabelled since the request.
	// Re-read its labels now and fail closed if they are not dev or unreadable.
	if act, _ := ActionByID(run.Action); act.LabelParam != "" {
		labels, err := m.labelsFor(ctx, run.Machine, run.Params[act.LabelParam])
		if err != nil {
			return m.refuse(ctx, run, "could not re-check the target's Cerberus labels: "+Redact(err.Error()))
		}
		if !labels.Dev() {
			return m.refuse(ctx, run, fmt.Sprintf("the target is no longer a dev resource (%s)", labels))
		}
	}
	transport, ok := m.Transports[run.Machine]
	if !ok {
		return m.refuse(ctx, run, "no transport is configured for machine "+run.Machine)
	}
	if !m.reserve() {
		return run, ErrBusy
	}
	reserved := true
	defer func() {
		if reserved {
			m.release()
		}
	}()
	won, err := m.Store.Transition(ctx, run.ID, run.State, StateRunning, nil)
	if err != nil || !won {
		if err == nil {
			return m.Store.Get(ctx, runID) // another caller started it
		}
		return run, err
	}
	startedAt := m.now()
	// The process lives on the run context, never on the caller's request.
	base := m.Base
	if base == nil {
		base = context.Background()
	}
	maxRuntime := m.MaxRuntime
	if maxRuntime <= 0 {
		maxRuntime = DefaultMaxRuntime
	}
	runCtx, cancel := context.WithTimeout(base, maxRuntime)
	sess, err := transport.Start(runCtx, spec.Argv, cols, rows)
	if err != nil {
		cancel()
		_ = m.Store.Finish(ctx, run.ID, StateRunning, StateFailed, nil, "", "could not start: "+Redact(err.Error()), m.now())
		return m.Store.Get(ctx, runID)
	}
	runUser := ""
	if u, err := user.Current(); err == nil && run.Machine == MachineAgentOS {
		runUser = u.Username
	} else if run.Machine != MachineAgentOS {
		runUser = "ssh:" + run.Machine
	}
	_ = m.Store.Started(ctx, run.ID, runUser, startedAt)
	act, _ := ActionByID(run.Action)
	l := &live{sess: sess, subs: map[chan Frame]struct{}{}, cancel: cancel, interactive: act.Interactive}
	m.mu.Lock()
	if m.runs == nil {
		m.runs = map[string]*live{}
	}
	m.runs[run.ID] = l
	m.starting--
	reserved = false
	m.mu.Unlock()
	if l.interactive {
		l.mu.Lock()
		l.arm(m.DetachGrace+initialAttach, "no terminal attached")
		l.mu.Unlock()
	}
	slog.Info("action run started", "run_id", run.ID, "item_id", run.ItemID, "op_digest", run.OpDigest, "machine", run.Machine, "argv", spec.Argv, "user", runUser)
	go m.pump(run, l, runCtx)
	return m.Store.Get(ctx, runID)
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Manager) refuse(ctx context.Context, run Run, why string) (Run, error) {
	_ = m.Store.Finish(ctx, run.ID, run.State, StateRefused, nil, "", why, m.now())
	slog.Warn("action run refused", "run_id", run.ID, "item_id", run.ItemID, "action", run.Action, "machine", run.Machine, "why", why)
	out, err := m.Store.Get(ctx, run.ID)
	if err == nil {
		return out, fmt.Errorf("refused: %s", why)
	}
	return run, err
}

// pump copies output to subscribers and the transcript, then records the exit.
func (m *Manager) pump(run Run, l *live, runCtx context.Context) {
	buf := make([]byte, 4096)
	for {
		n, err := l.sess.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			l.trans.output(chunk)
			l.mu.Lock()
			l.replay.add(chunk)
			for ch := range l.subs {
				select {
				case ch <- Frame{Output: chunk}:
				default: // a slow viewer loses frames, never blocks the run
				}
			}
			l.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code, werr := l.sess.Wait()
	_ = l.sess.Close()
	l.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, errText := StateSucceeded, ""
	l.mu.Lock()
	reason := l.reason
	if l.idle != nil {
		l.idle.Stop()
	}
	l.mu.Unlock()
	switch {
	case reason != "":
		state, errText = StateFailed, "ended by Tachyon: "+reason
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		state, errText = StateFailed, "ended by Tachyon: exceeded the maximum run time"
	case werr != nil:
		state, errText = StateFailed, Redact(werr.Error())
	case code != 0:
		state = StateFailed
	}
	exit := code
	if err := m.Store.Finish(ctx, run.ID, StateRunning, state, &exit, l.trans.text(), errText, m.now()); err != nil {
		slog.Error("action run result not recorded", "run_id", run.ID, "err", err)
	}
	slog.Info("action run finished", "run_id", run.ID, "item_id", run.ItemID, "op_digest", run.OpDigest, "machine", run.Machine, "exit_code", code, "state", state)
	l.mu.Lock()
	l.done, l.exit = true, code
	for ch := range l.subs {
		select {
		case ch <- Frame{Exit: &exit}:
		default:
		}
		close(ch)
	}
	l.subs = map[chan Frame]struct{}{}
	l.mu.Unlock()
	m.mu.Lock()
	delete(m.runs, run.ID)
	m.mu.Unlock()
}

// Attach subscribes to a live run: it returns bounded replay of recent output
// and a channel of frames that closes when the run ends. A finished run
// returns its recorded output and a closed channel.
func (m *Manager) Attach(ctx context.Context, runID string) (replay []byte, frames <-chan Frame, cancel func(), err error) {
	l := m.lookup(runID)
	if l == nil {
		run, err := m.Store.Get(ctx, runID)
		if err != nil {
			return nil, nil, nil, err
		}
		ch := make(chan Frame, 1)
		if run.Terminal && run.ExitCode != nil {
			ch <- Frame{Exit: run.ExitCode}
		}
		close(ch)
		return []byte(run.Output), ch, func() {}, nil
	}
	ch := make(chan Frame, 256)
	l.mu.Lock()
	defer l.mu.Unlock()
	replay = append([]byte(nil), l.replay.buf...)
	if l.done {
		close(ch)
		return replay, ch, func() {}, nil
	}
	l.subs[ch] = struct{}{}
	if l.idle != nil {
		l.idle.Stop() // a terminal is attached
		l.idle = nil
	}
	return replay, ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.subs[ch]; ok {
			delete(l.subs, ch)
			close(ch)
		}
		// The last terminal of an interactive run left: end the run after the
		// grace period (immediately by default). Unattended runs are unaffected.
		if l.interactive && !l.done && len(l.subs) == 0 {
			l.arm(m.DetachGrace, "terminal closed")
		}
	}, nil
}

// arm schedules the run's end unless a terminal attaches first. l.mu is held.
func (l *live) arm(after time.Duration, reason string) {
	if l.idle != nil {
		l.idle.Stop()
	}
	end := func() {
		l.mu.Lock()
		if l.done || len(l.subs) > 0 {
			l.mu.Unlock()
			return
		}
		l.reason = reason
		l.mu.Unlock()
		l.cancel()
		_ = l.sess.Close()
	}
	if after <= 0 {
		l.idle = nil
		go end()
		return
	}
	l.idle = time.AfterFunc(after, end)
}

func (m *Manager) reserve() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := m.MaxConcurrent
	if limit <= 0 {
		limit = DefaultMaxConcurrent
	}
	if len(m.runs)+m.starting >= limit {
		return false
	}
	m.starting++
	return true
}

func (m *Manager) release() {
	m.mu.Lock()
	m.starting--
	m.mu.Unlock()
}

// Input sends the owner's keystrokes to the process. It is not logged or
// stored; only its echo is kept out of the transcript.
func (m *Manager) Input(runID string, p []byte) error {
	l := m.lookup(runID)
	if l == nil {
		return errors.New("run is not live")
	}
	l.trans.typed(p)
	_, err := l.sess.Write(p)
	if errors.Is(err, io.EOF) {
		return errors.New("run has ended")
	}
	return err
}

// Resize forwards the browser terminal size.
func (m *Manager) Resize(runID string, cols, rows uint16) error {
	l := m.lookup(runID)
	if l == nil {
		return errors.New("run is not live")
	}
	if cols == 0 || rows == 0 || cols > 500 || rows > 500 {
		return errors.New("invalid terminal size")
	}
	return l.sess.Resize(cols, rows)
}

// Live reports whether a process is attached to the run.
func (m *Manager) Live(runID string) bool {
	l := m.lookup(runID)
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.done && l.reason == "" // a run Tachyon has decided to end is no longer live
}

// labelsFor reads the target resource's Cerberus labels on its machine.
func (m *Manager) labelsFor(ctx context.Context, machine, resource string) (Labels, error) {
	if m.Labels != nil {
		return m.Labels(ctx, machine, resource)
	}
	t, ok := m.Transports[machine]
	if !ok {
		return Labels{}, fmt.Errorf("machine %s has no transport configured", machine)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := t.Output(ctx, []string{"cerberus", "resource", "list", "-o", "json"})
	if err != nil {
		return Labels{}, fmt.Errorf("could not read Cerberus labels on %s: %w", machine, err)
	}
	var list []struct {
		ID    string `json:"id"`
		Env   string `json:"env"`
		Owner string `json:"owner"`
		Admin string `json:"admin"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return Labels{}, fmt.Errorf("could not parse Cerberus resource list on %s: %w", machine, err)
	}
	for _, r := range list {
		if r.ID == resource {
			return Labels{Env: r.Env, Owner: r.Owner, Admin: r.Admin}, nil
		}
	}
	return Labels{}, fmt.Errorf("Cerberus on %s has no resource %q", machine, resource)
}

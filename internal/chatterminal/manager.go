package chatterminal

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/actionrun"
)

const (
	chunkMax      = 4 << 10
	ringMax       = 1 << 20  // live output kept per run
	tailMax       = 64 << 10 // what survives the run
	replayMax     = 256 << 10
	flushIdle     = 150 * time.Millisecond
	maxRunTime    = 10 * time.Minute
	maxLiveRuns   = 4
	maxSessions   = 4
	sessionIdle   = 30 * time.Minute
	proposalTTL   = 15 * time.Minute
	operatorActor = "operator"
)

// Manager runs agent commands and interactive panes. It starts only what the store says is approved.
type Manager struct {
	Store  *Store
	Config *Config
	Start  Starter // StartPTY unless a test replaces it
	SSHBin string  // "ssh" unless a test replaces it
	// Publish emits a thread event (terminal.run). May be nil.
	Publish func(ctx context.Context, thread, typ string, fields map[string]any)
	// Ended is called once per run when it reaches a terminal state. May be nil.
	Ended func(ctx context.Context, r Run)
	Base  context.Context

	mu       sync.Mutex
	runs     map[string]*liveRun
	sessions map[string]*liveSession
}

type chunk struct {
	Seq   int64  `json:"seq"`
	Chunk string `json:"chunk"`
}

type liveRun struct {
	id      string
	thread  string
	sess    Session
	started time.Time
	cancel  context.CancelFunc

	mu       sync.Mutex
	chunks   []chunk
	bytes    int
	dropped  bool
	seq      int64
	acc      []byte
	timer    *time.Timer
	stopping bool
	timedOut bool
}

func (m *Manager) base() context.Context {
	if m.Base != nil {
		return m.Base
	}
	return context.Background()
}

func (m *Manager) starter() Starter {
	if m.Start != nil {
		return m.Start
	}
	return StartPTY
}

func (m *Manager) bin() string {
	if m.SSHBin != "" {
		return m.SSHBin
	}
	return "ssh"
}

func (m *Manager) publish(thread, typ string, f map[string]any) {
	if m.Publish != nil {
		m.Publish(m.base(), thread, typ, f)
	}
}

// StartRun launches an approved run. It re-derives the digest from the stored fields and refuses on a
// mismatch, so the bytes that run are the bytes that were approved.
func (m *Manager) StartRun(ctx context.Context, run Run) {
	fail := func(msg string) {
		r, _ := m.Store.fail(m.base(), run.RunID, msg)
		m.ended(r)
	}
	if run.State != StateRunning {
		return
	}
	if Digest(run.MachineID, run.SSHUser, run.Command) != run.OpDigest {
		fail("The command no longer matches what was approved.")
		return
	}
	mach, err := m.Config.Machine(run.MachineID)
	if err != nil {
		fail("This machine is no longer in the list.")
		return
	}
	if mach.SSHUser != run.SSHUser {
		fail("The ssh user for this machine changed since approval.")
		return
	}
	m.mu.Lock()
	if m.runs == nil {
		m.runs = map[string]*liveRun{}
	}
	if len(m.runs) >= maxLiveRuns {
		m.mu.Unlock()
		fail("Four runs are already in progress. Try again when one ends.")
		return
	}
	lr := &liveRun{id: run.RunID, thread: run.ThreadRef, started: time.Now()}
	m.runs[run.RunID] = lr
	m.mu.Unlock()

	rctx, cancel := context.WithTimeout(m.base(), maxRunTime)
	lr.cancel = cancel
	sess, err := m.starter()(rctx, sshArgv(m.bin(), mach, run.Command, true), sshEnv(m.Config.AuthSock()), 120, 30)
	if err != nil {
		cancel()
		m.drop(run.RunID)
		fail("Could not start ssh: " + err.Error())
		return
	}
	lr.mu.Lock()
	lr.sess = sess
	lr.mu.Unlock()
	go m.pump(rctx, lr, sess)
}

func (m *Manager) drop(id string) {
	m.mu.Lock()
	delete(m.runs, id)
	m.mu.Unlock()
}

func (m *Manager) ended(r Run) {
	if r.RunID != "" && m.Ended != nil {
		m.Ended(m.base(), r)
	}
}

func (m *Manager) pump(ctx context.Context, lr *liveRun, sess Session) {
	buf := make([]byte, chunkMax)
	for {
		n, err := sess.Read(buf)
		if n > 0 {
			lr.feed(m, buf[:n])
		}
		if err != nil {
			break
		}
	}
	code, werr := sess.Wait()
	_ = sess.Close()
	lr.flush(m)
	lr.mu.Lock()
	stopping := lr.stopping
	timedOut := ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)
	tail, trunc := lr.tailLocked()
	last := lr.seq
	lr.mu.Unlock()
	lr.cancel()

	state, errText := StateFinished, ""
	switch {
	case stopping:
		state = StateStopped
	case timedOut:
		state, errText = StateFailed, "Timed out after 10 minutes."
	case werr != nil:
		state, errText = StateFailed, "ssh did not exit cleanly."
	case code != 0:
		state = StateFailed
	}
	var exit *int
	if werr == nil {
		exit = &code
	}
	r, err := m.Store.Finish(m.base(), lr.id, state, exit, tail, trunc, errText, lr.started)
	m.drop(lr.id)
	if err != nil {
		return
	}
	f := map[string]any{"run_id": r.RunID, "state": r.State, "seq": last, "closed": r.ClosedAt != nil}
	if r.ExitCode != nil {
		f["exit_code"] = *r.ExitCode
	}
	if r.DurationMS != nil {
		f["duration_ms"] = *r.DurationMS
	}
	m.publish(lr.thread, "terminal.run", f)
	m.ended(r)
}

// feed takes raw pty bytes: complete lines (or 4 KiB) go out at once, a partial line after a short idle so
// prompts still show. Redaction runs on whole lines, so a secret is not split across two chunks.
func (lr *liveRun) feed(m *Manager, p []byte) {
	lr.mu.Lock()
	lr.acc = append(lr.acc, p...)
	var out [][]byte
	for len(lr.acc) > 0 {
		i := bytes.LastIndexByte(lr.acc, '\n')
		cut := 0
		switch {
		case len(lr.acc) >= chunkMax:
			cut = chunkMax
			if i >= 0 && i+1 <= chunkMax {
				cut = i + 1
			} else {
				for cut > 0 && cut < len(lr.acc) && !utf8.RuneStart(lr.acc[cut]) {
					cut--
				}
			}
		case i >= 0:
			cut = i + 1
		}
		if cut == 0 {
			break
		}
		out = append(out, append([]byte(nil), lr.acc[:cut]...))
		lr.acc = lr.acc[cut:]
	}
	if len(lr.acc) > 0 {
		if lr.timer == nil {
			lr.timer = time.AfterFunc(flushIdle, func() { lr.flush(m) })
		} else {
			lr.timer.Reset(flushIdle)
		}
	}
	lr.mu.Unlock()
	for _, b := range out {
		lr.emit(m, b)
	}
}

func (lr *liveRun) flush(m *Manager) {
	lr.mu.Lock()
	b := lr.acc
	lr.acc = nil
	lr.mu.Unlock()
	if len(b) > 0 {
		lr.emit(m, b)
	}
}

func (lr *liveRun) emit(m *Manager, raw []byte) {
	text := actionrun.Redact(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	text = strings.ReplaceAll(text, "\r", "")
	if text == "" {
		return
	}
	lr.mu.Lock()
	lr.seq++
	seq := lr.seq
	lr.chunks = append(lr.chunks, chunk{Seq: seq, Chunk: text})
	lr.bytes += len(text)
	for lr.bytes > ringMax && len(lr.chunks) > 1 {
		lr.bytes -= len(lr.chunks[0].Chunk)
		lr.chunks = lr.chunks[1:]
		lr.dropped = true
	}
	lr.mu.Unlock()
	m.publish(lr.thread, "terminal.run", map[string]any{"run_id": lr.id, "state": StateRunning, "seq": seq, "chunk": text})
}

func (lr *liveRun) tailLocked() (string, bool) {
	var sb strings.Builder
	for _, c := range lr.chunks {
		sb.WriteString(c.Chunk)
	}
	s, trunc := sb.String(), lr.dropped
	if len(s) > tailMax {
		s = s[len(s)-tailMax:]
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:]
		}
		trunc = true
	}
	return s, trunc
}

// Output is a run's output after seq. A live run answers from its ring; a finished one from the stored tail.
type Output struct {
	RunID     string  `json:"run_id"`
	State     string  `json:"state"`
	Chunks    []chunk `json:"chunks"`
	LastSeq   int64   `json:"last_seq"`
	Tail      *string `json:"tail"`
	Truncated bool    `json:"truncated"`
}

func (m *Manager) Output(ctx context.Context, id string, after int64, limit int) (Output, error) {
	run, err := m.Store.Run(ctx, id)
	if err != nil {
		return Output{}, err
	}
	out := Output{RunID: id, State: run.State, Chunks: []chunk{}, Truncated: run.Truncated}
	m.mu.Lock()
	lr := m.runs[id]
	m.mu.Unlock()
	if lr != nil && run.State == StateRunning {
		if limit <= 0 || limit > 500 {
			limit = 500
		}
		lr.mu.Lock()
		out.LastSeq = lr.seq
		out.Truncated = lr.dropped
		for _, c := range lr.chunks {
			if c.Seq > after && len(out.Chunks) < limit {
				out.Chunks = append(out.Chunks, c)
			}
		}
		lr.mu.Unlock()
		return out, nil
	}
	out.Tail = &run.Output
	return out, nil
}

// LiveOutput fills a run record's output with the live ring tail while it runs.
func (m *Manager) LiveOutput(r *Run) {
	if r.State != StateRunning {
		return
	}
	m.mu.Lock()
	lr := m.runs[r.RunID]
	m.mu.Unlock()
	if lr == nil {
		return
	}
	lr.mu.Lock()
	r.Output, r.Truncated = lr.tailLocked()
	lr.mu.Unlock()
}

// Stop asks a running run to end: SIGTERM to the ssh process group, SIGKILL after stopGrace. It is
// idempotent. The state flips to stopped when ssh exits; it never claims the remote command died.
func (m *Manager) Stop(ctx context.Context, id string) error {
	run, err := m.Store.Run(ctx, id)
	if err != nil {
		return err
	}
	if run.State == StatePending {
		return ErrConflict
	}
	if run.State != StateRunning {
		return nil
	}
	m.mu.Lock()
	lr := m.runs[id]
	m.mu.Unlock()
	if lr == nil {
		return nil
	}
	lr.mu.Lock()
	first := !lr.stopping
	lr.stopping = true
	sess := lr.sess
	lr.mu.Unlock()
	if !first || sess == nil {
		return nil
	}
	_ = m.Store.SetStopped(ctx, id, operatorActor)
	sess.Interrupt()
	time.AfterFunc(stopGrace, func() { _ = sess.Close() })
	return nil
}

// Live reports the number of runs in progress.
func (m *Manager) Live() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runs)
}

// fail ends a run that never started (or could not).
func (s *Store) fail(ctx context.Context, id, msg string) (Run, error) {
	now := s.now()
	err := s.DB.Writer.Submit(ctx, "terminal_fail", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE terminal_runs SET state='failed', error=?, ended_ns=?, duration_ms=0 WHERE run_id=? AND state='running'`, msg, now.UnixNano(), id)
		return err
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, id)
}

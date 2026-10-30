package chatterminal

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// liveSession is one interactive ssh pane. Output goes to every attached socket and to a replay ring;
// nothing is persisted but byte counts. The process starts on the first attach, at the browser's size.
type liveSession struct {
	id      string
	machine Machine

	mu      sync.Mutex
	sess    Session
	ring    []byte
	subs    map[chan []byte]struct{}
	started bool
	ended   bool
	exit    int
	done    chan struct{}
	idle    *time.Timer
	in, out atomic.Int64
}

func (m *Manager) liveSession(id string) *liveSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// OpenSession records a new pane for a listed machine. The ssh process starts when the socket attaches.
func (m *Manager) OpenSession(ctx context.Context, machineID, thread string) (SessionRow, error) {
	mach, err := m.Config.Machine(machineID)
	if err != nil {
		return SessionRow{}, err
	}
	row, err := m.Store.CreateSession(ctx, thread, mach.ID, mach.SSHUser, operatorActor, maxSessions)
	if err != nil {
		return SessionRow{}, err
	}
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = map[string]*liveSession{}
	}
	m.sessions[row.ID] = &liveSession{id: row.ID, machine: mach, subs: map[chan []byte]struct{}{}, done: make(chan struct{})}
	m.mu.Unlock()
	return row, nil
}

// CloseSession ends a pane. Idempotent.
func (m *Manager) CloseSession(ctx context.Context, id string) error {
	if _, err := m.Store.Session(ctx, id); err != nil {
		return err
	}
	if ls := m.liveSession(id); ls != nil {
		ls.mu.Lock()
		sess := ls.sess
		ls.mu.Unlock()
		if sess != nil {
			_ = sess.Close()
		}
		if sess == nil {
			m.endSession(ls, -1)
		}
	}
	return m.Store.EndSession(ctx, id, 0, 0)
}

// Attachment is one websocket's view of a session.
type Attachment struct {
	Replay []byte
	Frames <-chan []byte
	ch     chan []byte
	ls     *liveSession
}

// Attach starts ssh if this is the first attach, then returns the replay and a live frame channel.
func (m *Manager) Attach(ctx context.Context, id string, cols, rows uint16) (*Attachment, error) {
	ls := m.liveSession(id)
	if ls == nil {
		return nil, ErrNotFound
	}
	ls.mu.Lock()
	if ls.ended {
		ls.mu.Unlock()
		return nil, ErrConflict
	}
	if !ls.started {
		sess, err := m.starter()(m.base(), sshArgv(m.bin(), ls.machine, "", false), sshEnv(m.Config.AuthSock()), cols, rows)
		if err != nil {
			ls.mu.Unlock()
			m.endSession(ls, -1)
			return nil, err
		}
		ls.sess, ls.started = sess, true
		ls.idle = time.AfterFunc(sessionIdle, func() { _ = sess.Close() })
		go m.pumpSession(ls, sess)
	}
	ch := make(chan []byte, 64)
	ls.subs[ch] = struct{}{}
	replay := append([]byte(nil), ls.ring...)
	ls.mu.Unlock()
	return &Attachment{Replay: replay, Frames: ch, ch: ch, ls: ls}, nil
}

// Detach drops the socket; the session keeps running for a later attach.
func (a *Attachment) Detach() {
	a.ls.mu.Lock()
	if _, ok := a.ls.subs[a.ch]; ok {
		delete(a.ls.subs, a.ch)
		close(a.ch)
	}
	a.ls.mu.Unlock()
}

// Done closes when the process has ended; ExitCode is valid after.
func (a *Attachment) Done() <-chan struct{} { return a.ls.done }
func (a *Attachment) ExitCode() int         { return a.ls.exit }

func (a *Attachment) Input(p []byte) error {
	a.ls.mu.Lock()
	sess := a.ls.sess
	if a.ls.idle != nil {
		a.ls.idle.Reset(sessionIdle)
	}
	a.ls.mu.Unlock()
	if sess == nil {
		return errors.New("not started")
	}
	a.ls.in.Add(int64(len(p)))
	_, err := sess.Write(p)
	return err
}

func (a *Attachment) Resize(cols, rows uint16) {
	a.ls.mu.Lock()
	sess := a.ls.sess
	a.ls.mu.Unlock()
	if sess != nil && cols > 0 && rows > 0 && cols <= 500 && rows <= 500 {
		_ = sess.Resize(cols, rows)
	}
}

func (m *Manager) pumpSession(ls *liveSession, sess Session) {
	buf := make([]byte, 8<<10)
	for {
		n, err := sess.Read(buf)
		if n > 0 {
			b := append([]byte(nil), buf[:n]...)
			ls.out.Add(int64(n))
			ls.mu.Lock()
			ls.ring = append(ls.ring, b...)
			if len(ls.ring) > replayMax {
				ls.ring = append([]byte(nil), ls.ring[len(ls.ring)-replayMax:]...)
			}
			if ls.idle != nil {
				ls.idle.Reset(sessionIdle)
			}
			for c := range ls.subs {
				select {
				case c <- b:
				default: // a socket that cannot keep up is dropped; it reattaches and replays
					delete(ls.subs, c)
					close(c)
				}
			}
			ls.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code, _ := sess.Wait()
	_ = sess.Close()
	m.endSession(ls, code)
}

func (m *Manager) endSession(ls *liveSession, code int) {
	ls.mu.Lock()
	if ls.ended {
		ls.mu.Unlock()
		return
	}
	ls.ended, ls.exit = true, code
	if ls.idle != nil {
		ls.idle.Stop()
	}
	for c := range ls.subs {
		delete(ls.subs, c)
		close(c)
	}
	close(ls.done)
	ls.mu.Unlock()
	_ = m.Store.EndSession(m.base(), ls.id, ls.in.Load(), ls.out.Load())
	m.mu.Lock()
	delete(m.sessions, ls.id)
	m.mu.Unlock()
}

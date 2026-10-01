package plugins

import (
	"context"
	"sync"
	"time"
)

// service_health has three serial 30s HTTP calls; work_assign has two.
// Other finite provider budgets are <=30s (Tether launch <=20s HTTP plus
// SQLite checkpoints, SCM <=33s). 120s leaves headroom above those sequences.
// Agent Ops HTTP and filesystem work have no provider ceiling: this is their
// outer transport limit. Init/load/discovery use it too; unload gets 5s.
const pluginCallTimeout = 120 * time.Second

// Current unload hooks release local resources only; never wait indefinitely.
const pluginUnloadTimeout = 5 * time.Second

func (m *Manager) lockLifecycle(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, pluginCallTimeout)
	defer cancel()
	return m.lifecycleMu.LockContext(bounded)
}

// contextMutex is a zero-value serial lock with cancellable acquisition.
// Waiting never spawns a goroutine that could acquire a lock after returning.
type contextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *contextMutex) LockContext(ctx context.Context) error {
	m.once.Do(func() { m.token = make(chan struct{}, 1); m.token <- struct{}{} })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}
func (m *contextMutex) Lock() { _ = m.LockContext(context.Background()) }
func (m *contextMutex) Unlock() {
	select {
	case m.token <- struct{}{}:
	default:
		panic("plugins: unlock of unlocked contextMutex")
	}
}

// interruptProcess must not acquire callMu or any manager lock. The watchdog
// captures a process pointer, never an ID lookup that could find a replacement.
func interruptProcess(proc *pluginProcess) { interruptProcessForReason(proc, "host_stop") }

func interruptProcessForReason(proc *pluginProcess, reason string) {
	proc.interruptOnce.Do(func() {
		proc.deathReason = reason // Publish before dead so retirement can read it safely.
		proc.dead.Store(true)
		if proc.cmd != nil && proc.cmd.Process != nil {
			_ = proc.cmd.Process.Kill()
		}
		if proc.stdin != nil {
			_ = proc.stdin.Close()
		}
		if proc.stdout != nil {
			_ = proc.stdout.Close()
		}
	})
}

// retiredPlugin keeps only recovery metadata, never pipes or capability claims.
// lifetime comes from LoadPlugin's process owner, never from CallPlugin's request.
type retiredPlugin struct {
	id, name, binaryPath string
	lifetime             context.Context
	reason               string
	retiredAt            time.Time
}

// Retirement runs outside the call lock and asynchronously to avoid delaying
// HTTP failures behind an unrelated load/restart. Dead processes reject calls
// immediately while lifecycle serialization finishes removing registrations.
func (m *Manager) retireProcess(proc *pluginProcess) {
	proc.retireOnce.Do(func() {
		go func() {
			// Waiting here can delay reaping the already killed child while a hung
			// load owns lifecycleMu. Its handshakes are bounded; the delay is harmless.
			m.lifecycleMu.Lock()
			defer m.lifecycleMu.Unlock()
			stopProcess(proc)
			m.mu.Lock()
			current := m.plugins[proc.id] == proc
			if current {
				m.retired[proc.id] = retiredPlugin{id: proc.id, name: proc.name, binaryPath: proc.binaryPath, lifetime: proc.lifetime, reason: proc.deathReason, retiredAt: time.Now().UTC()}
			}
			m.mu.Unlock()
			if current {
				m.detachProcess(proc)
				m.lifecycleRecord(proc, proc.id, "plugin_failure", "error", proc.deathReason)
				m.lifecycleRecord(proc, proc.id, "plugin_retire", "error", proc.deathReason)
				m.logger.Warn("plugin transport interrupted; plugin unloaded", "id", proc.id, "completion", "unknown")
			}
		}()
	})
}

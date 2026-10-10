package plugins

import (
	"context"
	"errors"
	"fmt"
)

var ErrPluginNotFound = errors.New("plugin not found")
var ErrRestartBusy = errors.New("plugin is busy; restart not attempted")

// RestartPlugin stops only the named subprocess, then uses the normal load
// path to re-read settings and validate registration. The original lifetime
// context is retained: an HTTP request finishing must not kill the new plugin.
// A failed respawn leaves no registered process, module or navigation claims.
func (m *Manager) RestartPlugin(id string) error { return m.restartPlugin(context.Background(), id) }

func (m *Manager) restartPlugin(ctx context.Context, id string) error {
	if err := m.lockLifecycle(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrRestartBusy, err)
	}
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	proc := m.plugins[id]
	retired, recoverable := m.retired[id]
	m.mu.RUnlock()
	if proc == nil && !recoverable {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	path, lifetime := retired.binaryPath, retired.lifetime
	if proc != nil {
		path, lifetime = proc.binaryPath, proc.lifetime
	}
	if lifetime == nil {
		lifetime = context.Background()
	}
	m.lifecycleRecord(proc, id, "plugin_restart", "started", "")
	m.unloadPlugin(lifetime, id)
	if err := m.loadPlugin(lifetime, path, id); err != nil {
		m.lifecycleRecord(nil, id, "plugin_restart", "error", "restart_failed")
		m.logger.Error("plugin restart failed; plugin remains unloaded", "id", id, "error", err)
		return fmt.Errorf("restart plugin %q: %w", id, err)
	}
	m.logger.Info("plugin restarted", "id", id)
	m.mu.RLock()
	replacement := m.plugins[id]
	m.mu.RUnlock()
	m.lifecycleRecord(replacement, id, "plugin_restart", "ok", "")
	return nil
}

// stopProcess forcibly tears down captured transport ownership. It never
// waits for callMu before closing pipes that may be blocking its owner.
func stopProcess(proc *pluginProcess) {
	interruptProcess(proc)
	proc.callMu.Lock()
	proc.stopped = true
	proc.callMu.Unlock()
	proc.reapOnce.Do(func() {
		if proc.cmd != nil && proc.cmd.Process != nil {
			_ = proc.cmd.Wait()
		}
	})
}

func (m *Manager) unloadPlugin(ctx context.Context, id string) {
	m.mu.RLock()
	proc := m.plugins[id]
	m.mu.RUnlock()
	if proc == nil {
		return
	}
	// Unload is best effort, including acquiring the wire lock. A plugin with
	// blocked stdin or an in-flight stalled call cannot wedge lifecycle changes.
	bounded, cancel := context.WithTimeout(ctx, pluginUnloadTimeout)
	defer cancel()
	_, err := callProcess(bounded, proc, "plugin/unload", nil)
	stopProcess(proc)
	m.detachProcess(proc)
	status, reason := "ok", ""
	if err != nil {
		status, reason = "error", "unload_failed"
	}
	m.lifecycleRecord(proc, id, "plugin_unload", status, reason)
	if err != nil {
		m.logger.Warn("plugin unload failed; process force-stopped and reaped", "stage", "plugin_unload", "id", proc.id, "timed_out", errors.Is(err, context.DeadlineExceeded), "error", err)
	} else {
		m.logger.Info("plugin unloaded", "stage", "plugin_unload", "id", proc.id, "graceful", true)
	}
}

// detachProcess requires lifecycleMu. Identity protects replacements from
// delayed retirement of an earlier process with the same plugin ID.
func (m *Manager) detachProcess(proc *pluginProcess) {
	id := proc.id
	m.mu.Lock()
	if m.plugins[id] != proc {
		m.mu.Unlock()
		return
	}
	delete(m.plugins, id)
	m.registryRevision++
	for module, owner := range m.modules {
		if owner == id {
			delete(m.modules, module)
		}
	}
	order := m.loadOrder[:0]
	for _, loaded := range m.loadOrder {
		if loaded != id {
			order = append(order, loaded)
		}
	}
	m.loadOrder = order
	// Diagnose the revision-local projection after removing this identity.
	m.logNavProjectionLocked()
	m.mu.Unlock()
}

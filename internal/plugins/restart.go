package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

var ErrPluginNotFound = errors.New("plugin not found")

// RestartPlugin stops only the named subprocess, then uses the normal load
// path to re-read settings and validate registration. The original lifetime
// context is retained: an HTTP request finishing must not kill the new plugin.
// A failed respawn leaves no registered process, module or navigation claims.
func (m *Manager) RestartPlugin(id string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	proc := m.plugins[id]
	m.mu.RUnlock()
	if proc == nil {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	path, lifetime := proc.binaryPath, proc.lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	m.unloadPlugin(id)
	if err := m.loadPlugin(lifetime, path, id); err != nil {
		m.logger.Error("plugin restart failed; plugin remains unloaded", "id", id, "error", err)
		return fmt.Errorf("restart plugin %q: %w", id, err)
	}
	m.logger.Info("plugin restarted", "id", id)
	return nil
}

func stopProcess(proc *pluginProcess) {
	proc.callMu.Lock()
	defer proc.callMu.Unlock()
	stopProcessLocked(proc)
}

// The caller holds callMu for the full teardown so pending calls to the old
// process cannot write after a new process takes ownership of the plugin ID.
func stopProcessLocked(proc *pluginProcess) {
	if proc.stopped {
		return
	}
	proc.stopped = true
	if proc.stdin != nil {
		_ = json.NewEncoder(proc.stdin).Encode(subprocess.RPCRequest{JSONRPC: "2.0", ID: 999, Method: "plugin/unload", Params: subprocess.LoadParams{}})
		_ = proc.stdin.Close()
	}
	if proc.stdout != nil {
		_ = proc.stdout.Close()
	}
	if proc.cmd != nil && proc.cmd.Process != nil {
		_ = proc.cmd.Process.Kill()
		_ = proc.cmd.Wait()
	}
}

func (m *Manager) unloadPlugin(id string) {
	m.mu.RLock()
	proc := m.plugins[id]
	m.mu.RUnlock()
	if proc == nil {
		return
	}
	proc.callMu.Lock()
	defer proc.callMu.Unlock()
	m.mu.Lock()
	delete(m.plugins, id)
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
	// Re-elect first-loaded surviving declarations when the removed process
	// owned shared nav IDs. Respawn is a new load at the end of loadOrder.
	m.navGroups = map[string]string{}
	m.navItems = map[string]string{}
	for _, loaded := range m.loadOrder {
		caps := m.plugins[loaded].capabilities
		if caps == nil || caps.Nav == nil {
			continue
		}
		for _, group := range caps.Nav.Groups {
			if _, claimed := m.navGroups[group.ID]; !claimed {
				m.navGroups[group.ID] = loaded
			}
		}
		for _, item := range caps.Nav.Items {
			if _, claimed := m.navItems[item.ID]; !claimed {
				m.navItems[item.ID] = loaded
			}
		}
	}
	m.mu.Unlock()
	stopProcessLocked(proc)
}

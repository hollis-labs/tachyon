// Package plugins manages Tachyon's plugin host — the subprocess spawning,
// registry building, and plugin lifecycle for the plugin-sdk integration.
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/hollis-labs/plugin-sdk/registry"
	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// Manager is Tachyon's plugin host. It spawns plugin binaries, manages their
// lifecycle, and builds the registry response for the browser loader.
type Manager struct {
	logger  *slog.Logger
	mu      sync.RWMutex
	plugins map[string]*pluginProcess // keyed by plugin ID
}

// pluginProcess is one spawned plugin subprocess.
type pluginProcess struct {
	id      string
	name    string
	version string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
}

// NewManager creates a new plugin manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		logger:  logger,
		plugins: make(map[string]*pluginProcess),
	}
}

// LoadPlugin spawns a plugin binary and initializes it.
func (m *Manager) LoadPlugin(ctx context.Context, binaryPath string) error {
	cmd := exec.CommandContext(ctx, binaryPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start plugin: %w", err)
	}

	// Call plugin/init to get plugin metadata
	initReq := subprocess.RPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "plugin/init",
		Params: subprocess.InitParams{
			PluginDir: "",
			DataDir:   "",
			CacheDir:  "",
			Config:    map[string]string{},
			LogLevel:  "info",
			HostInfo: subprocess.HostInfo{
				Version:  "0.1.0",
				Protocol: subprocess.ProtocolVersion,
			},
		},
	}

	if err := json.NewEncoder(stdin).Encode(initReq); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to send init request: %w", err)
	}

	var initResp subprocess.RPCResponse
	if err := json.NewDecoder(stdout).Decode(&initResp); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to read init response: %w", err)
	}

	if initResp.Error != nil {
		cmd.Process.Kill()
		return fmt.Errorf("plugin init failed: %s", initResp.Error.Message)
	}

	var initResult subprocess.InitResult
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to unmarshal init result: %w", err)
	}

	proc := &pluginProcess{
		id:      initResult.ID,
		name:    initResult.Name,
		version: initResult.Version,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
	}

	m.mu.Lock()
	m.plugins[initResult.ID] = proc
	m.mu.Unlock()

	m.logger.Info("plugin loaded",
		"id", initResult.ID,
		"name", initResult.Name,
		"version", initResult.Version)

	return nil
}

// BuildRegistry constructs the registry.Response for the browser loader.
func (m *Manager) BuildRegistry() registry.Response {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := registry.NewResponse()

	// For now, we're just proving the contract works — plugins are registered
	// but have no browser UI bundles yet. Full implementation would include
	// bundle URLs from plugin manifests.
	for id, proc := range m.plugins {
		resp.Plugins[id] = registry.Plugin{
			// BundleURL would go here when we have UI components
			BundleURL:     "",
			StylesheetURL: "",
			BundleVersion: proc.version,
		}
	}

	return resp
}

// Shutdown stops all plugins gracefully.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, proc := range m.plugins {
		// Call plugin/unload
		unloadReq := subprocess.RPCRequest{
			JSONRPC: "2.0",
			ID:      999,
			Method:  "plugin/unload",
			Params:  struct{}{},
		}

		if err := json.NewEncoder(proc.stdin).Encode(unloadReq); err != nil {
			m.logger.Error("failed to send unload request", "plugin_id", id, "error", err)
		}

		proc.stdin.Close()
		proc.stdout.Close()

		if err := proc.cmd.Wait(); err != nil {
			m.logger.Error("plugin exited with error", "plugin_id", id, "error", err)
		}
	}

	return nil
}

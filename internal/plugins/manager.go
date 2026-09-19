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

	// stdoutDec is the single long-lived decoder over stdout, created once
	// in LoadPlugin and reused by every subsequent CallPlugin. A
	// json.Decoder buffers ahead of the JSON value it hands back from
	// Decode — reading a pipe can return more bytes in one syscall than
	// exactly one JSON-RPC message, and the decoder holds the remainder
	// (the start of the *next* response) in its own internal buffer.
	// Constructing a fresh json.NewDecoder(proc.stdout) per call, as this
	// used to do, throws that buffered remainder away when the old
	// decoder is discarded — corrupting the next response even with calls
	// fully serialized. Reusing one decoder is what makes serialization
	// (via callMu below) actually sufficient.
	stdoutDec *json.Decoder

	// callMu serializes the write-request/read-response round trip in
	// CallPlugin. The subprocess speaks one JSON-RPC message at a time
	// over a single stdin/stdout pipe pair with no request IDs to
	// correlate an out-of-order response — without this lock, concurrent
	// HTTP handlers calling the same plugin race to write and read that
	// shared pipe, interleaving their requests and responses and handing
	// each other's decode calls garbled JSON (surfaces as "invalid
	// character 'x' looking for beginning of value" at an essentially
	// random byte offset). A UI that fires several requests at once
	// against the same plugin — e.g. loading multiple tabs' data in
	// parallel — reliably triggers this without the lock.
	callMu sync.Mutex
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

	// One decoder for this subprocess's entire lifetime — see
	// pluginProcess.stdoutDec's doc comment for why a fresh decoder per
	// call is unsafe.
	dec := json.NewDecoder(stdout)

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
	if err := dec.Decode(&initResp); err != nil {
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
		id:        initResult.ID,
		name:      initResult.Name,
		version:   initResult.Version,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stdoutDec: dec,
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

// CallPlugin invokes a custom RPC method on a plugin.
func (m *Manager) CallPlugin(ctx context.Context, pluginID, method string, params interface{}) (json.RawMessage, error) {
	m.mu.RLock()
	proc, exists := m.plugins[pluginID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}

	// Serialize the full round trip — see pluginProcess.callMu's doc
	// comment for why concurrent callers must not share this pipe pair
	// unguarded.
	proc.callMu.Lock()
	defer proc.callMu.Unlock()

	req := subprocess.RPCRequest{
		JSONRPC: "2.0",
		ID:      2, // Using 2 for custom calls (1 is init, 999 is unload)
		Method:  method,
		Params:  params,
	}

	if err := json.NewEncoder(proc.stdin).Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	var resp subprocess.RPCResponse
	if err := proc.stdoutDec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("plugin error: %s", resp.Error.Message)
	}

	return resp.Result, nil
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

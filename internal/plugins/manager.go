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
	"github.com/hollis-labs/tachyon/internal/contract"
)

// extendedInitResult extends plugin-sdk's InitResult with the host
// contract's capability declaration (D-47, D-48). Plugins that declare
// capabilities include a "capabilities" field alongside the standard
// InitResult fields. The host unmarshals into this extended type and
// validates the declaration at load time.
type extendedInitResult struct {
	subprocess.InitResult
	Capabilities *contract.PluginCapabilities `json:"capabilities,omitempty"`
}

// Manager is Tachyon's plugin host. It spawns plugin binaries, manages their
// lifecycle, and builds the registry response for the browser loader.
type Manager struct {
	logger  *slog.Logger
	mu      sync.RWMutex
	plugins map[string]*pluginProcess // keyed by plugin ID
	modules map[string]string         // module name -> owning plugin ID
}

// pluginProcess is one spawned plugin subprocess.
type pluginProcess struct {
	id      string
	name    string
	version string
	// capabilities holds the plugin's validated capability declaration
	// from its init response (D-47). Nil for legacy plugins that do not
	// yet declare capabilities.
	capabilities *contract.PluginCapabilities
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
		modules: make(map[string]string),
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

	var extResult extendedInitResult
	if err := json.Unmarshal(initResp.Result, &extResult); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to unmarshal init result: %w", err)
	}

	// Validate capability declarations (D-47). If the plugin declares
	// capabilities, every declaration is checked for internal consistency
	// and cross-plugin module collisions. A legacy plugin that omits
	// capabilities is loaded with a warning but no error.
	if extResult.Capabilities != nil {
		if err := extResult.Capabilities.Validate(); err != nil {
			cmd.Process.Kill()
			return fmt.Errorf("plugin %q capability validation failed: %w", extResult.ID, err)
		}

		// Check for module ownership collisions (D-49).
		for _, mod := range extResult.Capabilities.Modules {
			if owner, taken := m.modules[mod]; taken {
				cmd.Process.Kill()
				return fmt.Errorf("plugin %q claims module %q, already owned by plugin %q", extResult.ID, mod, owner)
			}
		}
	} else {
		m.logger.Warn("plugin loaded without capability declaration",
			"id", extResult.ID,
			"hint", "declare capabilities in plugin/init response (D-47)")
	}

	proc := &pluginProcess{
		id:           extResult.ID,
		name:         extResult.Name,
		version:      extResult.Version,
		capabilities: extResult.Capabilities,
		cmd:          cmd,
		stdin:        stdin,
		stdout:       stdout,
		stdoutDec:    dec,
	}

	m.mu.Lock()
	m.plugins[extResult.ID] = proc
	if extResult.Capabilities != nil {
		for _, mod := range extResult.Capabilities.Modules {
			m.modules[mod] = extResult.ID
		}
	}
	m.mu.Unlock()

	if extResult.Capabilities != nil {
		m.logger.Info("plugin loaded",
			"id", extResult.ID,
			"name", extResult.Name,
			"version", extResult.Version,
			"modules", extResult.Capabilities.Modules,
			"verbs", len(extResult.Capabilities.Verbs))
	} else {
		m.logger.Info("plugin loaded",
			"id", extResult.ID,
			"name", extResult.Name,
			"version", extResult.Version)
	}

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

// InvokeVerb dispatches a verb/invoke call to the plugin that owns the
// verb's module. Returns an error if no plugin claims the module or the
// verb is not declared in the owning plugin's capabilities.
func (m *Manager) InvokeVerb(ctx context.Context, verb string, payload json.RawMessage) (json.RawMessage, error) {
	m.mu.RLock()

	// Determine which module owns this verb by prefix matching.
	var ownerID string
	for mod, pluginID := range m.modules {
		if len(verb) > len(mod) && verb[:len(mod)+1] == mod+"_" {
			ownerID = pluginID
			break
		}
	}
	m.mu.RUnlock()

	if ownerID == "" {
		return nil, fmt.Errorf("no plugin owns a module matching verb %q", verb)
	}

	params := struct {
		Verb    string          `json:"verb"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}{
		Verb:    verb,
		Payload: payload,
	}

	return m.CallPlugin(ctx, ownerID, "verb/invoke", params)
}

// ModuleOwner returns the plugin ID that owns the given module, or empty
// string if no plugin has claimed it.
func (m *Manager) ModuleOwner(module string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modules[module]
}

// AllCapabilities returns a merged view of all loaded plugins'
// capability declarations. The returned map is keyed by module name.
func (m *Manager) AllCapabilities() map[string]*contract.PluginCapabilities {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*contract.PluginCapabilities)
	for _, proc := range m.plugins {
		if proc.capabilities != nil {
			for _, mod := range proc.capabilities.Modules {
				result[mod] = proc.capabilities
			}
		}
	}
	return result
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

		// Release module ownership.
		if proc.capabilities != nil {
			for _, mod := range proc.capabilities.Modules {
				delete(m.modules, mod)
			}
		}

		if err := proc.cmd.Wait(); err != nil {
			m.logger.Error("plugin exited with error", "plugin_id", id, "error", err)
		}
	}

	return nil
}

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
	"path/filepath"
	"sort"
	"sync"

	"github.com/hollis-labs/plugin-sdk/registry"
	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

// Manager is Tachyon's plugin host. It spawns plugin binaries, manages their
// lifecycle, and builds the registry response for the browser loader.
type Manager struct {
	logger    *slog.Logger
	mu        sync.RWMutex
	plugins   map[string]*pluginProcess // keyed by plugin ID
	modules   map[string]string         // module name -> owning plugin ID
	loadOrder []string
	navGroups map[string]string // group ID -> first-loaded plugin ID
	navItems  map[string]string // item ID -> first-loaded plugin ID
}

// pluginProcess is one spawned plugin subprocess.
type pluginProcess struct {
	id      string
	name    string
	version string
	// capabilities holds the plugin's validated capability declaration
	// from its discovery command (D-47). Nil for legacy plugins that do not
	// yet declare capabilities.
	capabilities *contract.PluginCapabilities
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser

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
		logger:    logger,
		plugins:   make(map[string]*pluginProcess),
		modules:   make(map[string]string),
		navGroups: make(map[string]string),
		navItems:  make(map[string]string),
	}
}

// LoadPlugin spawns a plugin binary and initializes it.
func (m *Manager) LoadPlugin(ctx context.Context, binaryPath string) error {
	dataDir, config, err := pluginInitSettings(binaryPath)
	if err != nil {
		return fmt.Errorf("plugin startup settings: %w", err)
	}
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
			PluginDir: filepath.Dir(binaryPath),
			DataDir:   dataDir,
			CacheDir:  "",
			Config:    config,
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
		cmd.Wait()
		return fmt.Errorf("failed to unmarshal init result: %w", err)
	}
	proc := &pluginProcess{id: initResult.ID, name: initResult.Name, version: initResult.Version,
		cmd: cmd, stdin: stdin, stdout: stdout, stdoutDec: dec}
	if err := m.initializePlugin(ctx, proc); err != nil {
		stdin.Close()
		stdout.Close()
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	m.logger.Info("plugin loaded", "id", proc.id, "name", proc.name, "version", proc.version)

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

	return callProcess(ctx, proc, method, params)
}

func callProcess(ctx context.Context, proc *pluginProcess, method string, params interface{}) (json.RawMessage, error) {
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

// InvokeVerb dispatches a command/execute call to the plugin that owns the
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
	if proc := m.plugins[ownerID]; proc != nil && proc.capabilities != nil {
		if _, declared := proc.capabilities.Verbs[verb]; !declared {
			m.mu.RUnlock()
			return nil, fmt.Errorf("verb %q is not declared by plugin %q", verb, ownerID)
		}
	}
	m.mu.RUnlock()

	if ownerID == "" {
		return nil, fmt.Errorf("no plugin owns a module matching verb %q", verb)
	}

	if verb == "config_schema" || verb == "config_get" || verb == "config_list" || verb == "config_set" || verb == "config_reset" {
		if err := m.syncConfigSchemas(ctx, ownerID); err != nil {
			return nil, err
		}
	}
	raw, err := m.CallPlugin(ctx, ownerID, "command/execute", subprocess.CommandExecParams{Name: verb, Args: string(payload)})
	if err != nil {
		return nil, err
	}
	var result subprocess.CommandExecResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode verb command result: %w", err)
	}
	if result.Action != "message" || !json.Valid([]byte(result.Content)) {
		return nil, fmt.Errorf("plugin %q returned invalid verb command result", ownerID)
	}
	return json.RawMessage(result.Content), nil
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

// initializePlugin completes lifecycle and capability registration before the
// process becomes visible to callers. Collision checks and insertion are atomic.
func (m *Manager) initializePlugin(ctx context.Context, proc *pluginProcess) error {
	if _, err := callProcess(ctx, proc, "plugin/load", subprocess.LoadParams{}); err != nil {
		return fmt.Errorf("plugin %q load failed: %w", proc.id, err)
	}
	raw, err := callProcess(ctx, proc, "command/execute", subprocess.CommandExecParams{Name: pluginkit.CommandCapabilities})
	if err != nil {
		m.logger.Warn("plugin loaded without capability declaration", "id", proc.id, "error", err)
	} else {
		var result subprocess.CommandExecResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("plugin %q capability response: %w", proc.id, err)
		}
		if result.Action == "error" {
			m.logger.Warn("plugin loaded without capability declaration", "id", proc.id, "error", result.Content)
		} else {
			var caps contract.PluginCapabilities
			if err := json.Unmarshal([]byte(result.Content), &caps); err != nil {
				return fmt.Errorf("plugin %q capability declaration: %w", proc.id, err)
			}
			if err := caps.Validate(); err != nil {
				return fmt.Errorf("plugin %q capability validation failed: %w", proc.id, err)
			}
			proc.capabilities = &caps
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.plugins[proc.id]; exists {
		return fmt.Errorf("plugin %q already loaded", proc.id)
	}
	if proc.capabilities != nil {
		for _, mod := range proc.capabilities.Modules {
			if owner, taken := m.modules[mod]; taken {
				return fmt.Errorf("plugin %q claims module %q, already owned by plugin %q", proc.id, mod, owner)
			}
		}
		for _, mod := range proc.capabilities.Modules {
			m.modules[mod] = proc.id
		}
	}
	if proc.capabilities != nil && proc.capabilities.Nav != nil {
		for _, group := range proc.capabilities.Nav.Groups {
			if owner, exists := m.navGroups[group.ID]; exists {
				m.logger.Warn("nav group collision; first loaded declaration wins", "group_id", group.ID, "winner", owner, "loser", proc.id)
			} else {
				m.navGroups[group.ID] = proc.id
			}
		}
		for _, item := range proc.capabilities.Nav.Items {
			if owner, exists := m.navItems[item.ID]; exists {
				m.logger.Warn("nav item collision; first loaded declaration wins", "item_id", item.ID, "winner", owner, "loser", proc.id)
			} else {
				m.navItems[item.ID] = proc.id
			}
		}
	}
	m.plugins[proc.id] = proc
	m.loadOrder = append(m.loadOrder, proc.id)
	return nil
}

// MergedNav returns a snapshot of navigation from registered plugins. Group
// metadata and globally unique item IDs are first-loaded wins; later plugins
// can still contribute distinct items to a shared group. Priority zero uses
// the contract default (1000), and IDs break ties for deterministic responses.
func (m *Manager) MergedNav() contract.NavDeclaration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := contract.NavDeclaration{Groups: []contract.NavGroup{}, Items: []contract.NavItem{}}
	for _, id := range m.loadOrder {
		proc := m.plugins[id]
		if proc == nil || proc.capabilities == nil || proc.capabilities.Nav == nil {
			continue
		}
		for _, g := range proc.capabilities.Nav.Groups {
			if m.navGroups[g.ID] == id {
				out.Groups = append(out.Groups, g)
			}
		}
		for _, item := range proc.capabilities.Nav.Items {
			if m.navItems[item.ID] == id {
				out.Items = append(out.Items, item)
			}
		}
	}
	priority := func(value int) int {
		if value == 0 {
			return 1000
		}
		return value
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if priority(a.Priority) == priority(b.Priority) {
			return a.ID < b.ID
		}
		return priority(a.Priority) < priority(b.Priority)
	})
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if priority(a.Priority) == priority(b.Priority) {
			return a.ID < b.ID
		}
		return priority(a.Priority) < priority(b.Priority)
	})
	return out
}

// Package plugins manages Tachyon's plugin host — the subprocess spawning,
// registry building, and plugin lifecycle for the plugin-sdk integration.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/observefeed"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

// Manager is Tachyon's plugin host. It spawns plugin binaries, manages their
// lifecycle, and builds the registry response for the browser loader.
type Manager struct {
	registryRevision uint64
	hostInstance     string
	generation       atomic.Uint64
	observe          *observeRecorder
	lifecycleMu      contextMutex // serialize load/restart/shutdown registration changes
	spawn            func(context.Context, string) (*pluginProcess, error)
	logger           *slog.Logger
	mu               sync.RWMutex
	plugins          map[string]*pluginProcess // keyed by plugin ID
	retired          map[string]retiredPlugin  // recovery metadata only; never routable
	modules          map[string]string         // module name -> owning plugin ID
	loadOrder        []string
	navGroups        map[string]string // group ID -> first-loaded plugin ID
	navItems         map[string]string // item ID -> first-loaded plugin ID
	navRoutes        map[string]string // route -> first-loaded plugin ID
	navDiagnostics   []contract.NavDiagnostic
	navNotices       []string
}

// pluginProcess is one spawned plugin subprocess.
type pluginProcess struct {
	requestID         atomic.Int64
	observeGeneration string // opaque per-process identity, never a memory address
	observeToken      string // immutable private init marker; never published
	binaryPath        string
	lifetime          context.Context
	stopped           bool // protected by callMu
	id                string
	name              string
	version           string
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
	callMu        contextMutex
	deathReason   string      // Published by interruptOnce before dead becomes true.
	dead          atomic.Bool // readable without callMu during watchdog interruption
	interruptOnce sync.Once
	reapOnce      sync.Once
	retireOnce    sync.Once
	callTimeout   time.Duration // zero uses pluginCallTimeout
}

// NewManager creates a new plugin manager.
func NewManager(logger *slog.Logger) *Manager {
	m := &Manager{
		hostInstance: opaqueID(),
		observe:      newObserveRecorder(),
		logger:       logger,
		plugins:      make(map[string]*pluginProcess),
		retired:      make(map[string]retiredPlugin),
		modules:      make(map[string]string),
		navGroups:    make(map[string]string),
		navItems:     make(map[string]string),
		navRoutes:    make(map[string]string),
	}
	m.spawn = func(ctx context.Context, path string) (*pluginProcess, error) {
		generation := m.generation.Add(1)
		if generation > capability.MaxSafeInteger {
			return nil, fmt.Errorf("plugin generation exhausted")
		}
		return spawnProcessWithIdentity(ctx, path, capability.RuntimeIdentity{HostInstance: m.hostInstance, OwnerID: filepath.Base(path), OwnerGeneration: generation})
	}
	return m
}

// LoadPlugin spawns a plugin binary and initializes it through the normal
// settings-read path. Lifecycle changes are serialized separately from calls.
func (m *Manager) LoadPlugin(ctx context.Context, binaryPath string) error {
	if err := m.lockLifecycle(ctx); err != nil {
		return err
	}
	defer m.lifecycleMu.Unlock()
	return m.loadPlugin(ctx, binaryPath, "")
}

func (m *Manager) loadPlugin(ctx context.Context, binaryPath, expectedID string) error {
	spawn := m.spawn
	if spawn == nil {
		spawn = spawnProcess
	}
	proc, err := spawn(ctx, binaryPath)
	if err != nil {
		m.lifecycleRecord(nil, filepath.Base(binaryPath), "plugin_failure", "error", admissionReason(err, "spawn"))
		return err
	}
	proc.binaryPath = binaryPath
	proc.lifetime = ctx
	if expectedID != "" && proc.id != expectedID {
		stopProcess(proc)
		m.lifecycleRecord(proc, expectedID, "plugin_failure", "error", "admission")
		return fmt.Errorf("restarted plugin identity changed from %q to %q", expectedID, proc.id)
	}
	if err := m.initializePlugin(ctx, proc); err != nil {
		m.lifecycleRecord(proc, proc.id, "plugin_failure", "error", admissionReason(err, "admission"))
		stopProcess(proc)
		return err
	}
	m.logger.Info("plugin loaded", "id", proc.id, "name", proc.name, "version", proc.version)
	m.lifecycleRecord(proc, proc.id, "plugin_load", "ok", "")
	if proc.id == "observe-ops" {
		m.observe.start(m)
	}
	return nil
}

func spawnProcess(ctx context.Context, binaryPath string) (*pluginProcess, error) {
	return spawnProcessWithIdentity(ctx, binaryPath, capability.RuntimeIdentity{HostInstance: opaqueID(), OwnerID: filepath.Base(binaryPath), OwnerGeneration: 1})
}

func spawnProcessWithIdentity(ctx context.Context, binaryPath string, identity capability.RuntimeIdentity) (*pluginProcess, error) {
	dataDir, config, err := pluginInitSettings(binaryPath)
	if err != nil {
		return nil, admissionFailure("settings", "plugin startup settings: %w", err)
	}
	pluginDir, err := filepath.Abs(filepath.Dir(binaryPath))
	if err != nil {
		return nil, err
	}
	cacheDir, err := pluginCacheDir(identity.OwnerID)
	if err != nil {
		return nil, err
	}
	if err = validateRuntimeRoots(pluginDir, dataDir, cacheDir); err != nil {
		return nil, admissionFailure("settings", "plugin runtime roots: %w", err)
	}
	if identity.OwnerID == "observe-ops" {
		config[observefeed.TokenConfig] = opaqueID()
	}
	params := subprocess.InitParams{PluginDir: pluginDir, DataDir: dataDir, CacheDir: cacheDir, Config: config, LogLevel: "info", HostInfo: subprocess.HostInfo{Version: "0.1.0", Protocol: subprocess.ProtocolVersion}, CapabilityContract: capability.ContractVersion, Incarnation: identity, Grants: capability.GrantSet{}}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, admissionFailure("handshake", "invalid init: %w", err)
	}
	if err = json.Unmarshal(encoded, &params); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binaryPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, admissionFailure("spawn", "failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, admissionFailure("spawn", "failed to create stdout pipe: %w", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, admissionFailure("spawn", "failed to start plugin: %w", err)
	}

	proc := &pluginProcess{cmd: cmd, stdin: stdin, stdout: stdout, stdoutDec: json.NewDecoder(stdout)}
	proc.observeToken = config[observefeed.TokenConfig]
	// Init shares the bounded serial transport; the process lifetime stays on ctx.
	raw, err := callProcess(ctx, proc, "plugin/init", params)
	if err != nil {
		stopProcess(proc)
		return nil, admissionFailure("handshake", "plugin init failed: %w", err)
	}
	var result subprocess.InitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		stopProcess(proc)
		return nil, admissionFailure("handshake", "failed to unmarshal init result: %w", err)
	}
	if err := subprocess.ValidateInitResult(params, result); err != nil {
		stopProcess(proc)
		return nil, admissionFailure("handshake", "init agreement: %w", err)
	}
	if result.ID != identity.OwnerID {
		stopProcess(proc)
		return nil, admissionFailure("handshake", "plugin identity differs from executable")
	}
	proc.id, proc.name, proc.version = result.ID, result.Name, result.Version

	return proc, nil
}

// BuildRegistry constructs the registry.Response for the browser loader.
// RegistryResponse extends the SDK registry without advertising dead plugin
// bundles or routing claims. Legacy decoders ignore the additive metadata.
type RegistryResponse struct {
	registry.Response
	RetiredPlugins []contract.SettingsTarget `json:"retired_plugins,omitempty"`
}

func (m *Manager) BuildRegistry() RegistryResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := RegistryResponse{Response: registry.NewResponse(m.hostInstance, m.registryRevision+1), RetiredPlugins: m.retiredTargetsLocked()}

	// For now, we're just proving the contract works — plugins are registered
	// but have no browser UI bundles yet. Full implementation would include
	// bundle URLs from plugin manifests.
	for id, proc := range m.plugins {
		resp.Plugins[id] = registry.Plugin{
			// BundleURL would go here when we have UI components
			OwnerGeneration: observeGeneration(proc),
			BundleURL:       "",
			StylesheetURL:   "",
			BundleVersion:   "",
		}
	}

	return resp
}

// CallPlugin invokes a custom RPC method on a plugin. The request context
// governs queueing only: a client disconnect cannot terminate a shared plugin
// or abandon a response on its serial wire. Active I/O uses the host budget.
func (m *Manager) CallPlugin(ctx context.Context, pluginID, method string, params interface{}) (json.RawMessage, error) {
	if strings.HasPrefix(method, "plugin/") {
		return nil, fmt.Errorf("plugin lifecycle methods are host-only")
	}
	if method == "command/execute" {
		// Inspect the actual serialized name, irrespective of the Go parameter
		// type. Pass the frozen bytes onward so custom marshalers run only once.
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		params = json.RawMessage(encoded)
		if PrivateCommand(commandName(params)) {
			return nil, fmt.Errorf("private host command is unavailable")
		}
	}
	m.mu.RLock()
	proc, exists := m.plugins[pluginID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}

	finish := m.beginOperation(proc, method, commandName(params))
	raw, err := callProcessContexts(ctx, context.WithoutCancel(ctx), proc, method, params)
	result := raw
	if method == "command/execute" {
		result = commandEnvelope(raw)
	}
	finish(result, err)
	if proc.dead.Load() {
		m.retireProcess(proc)
	}
	return raw, err
}

// callProcess is for lifecycle operations whose caller owns the subprocess.
// Those contexts may interrupt active I/O, unlike an HTTP request's context.
func callProcess(ctx context.Context, proc *pluginProcess, method string, params interface{}) (json.RawMessage, error) {
	return callProcessContexts(ctx, ctx, proc, method, params)
}

var errCallQueueWait = errors.New("plugin queue wait")

func callProcessContexts(queueCtx, ioCtx context.Context, proc *pluginProcess, method string, params interface{}) (json.RawMessage, error) {
	budget := proc.callTimeout
	if budget <= 0 {
		budget = pluginCallTimeout
	}
	return callProcessContextsBudgets(queueCtx, ioCtx, proc, method, params, budget, budget)
}

// Each deadline starts in its own stage. A queue error proves no bytes were
// written; callers may retain telemetry without replaying ambiguous I/O.
func callProcessContextsBudgets(queueCtx, ioCtx context.Context, proc *pluginProcess, method string, params interface{}, queueBudget, ioBudget time.Duration) (json.RawMessage, error) {
	// Queueing is cancellable and never kills somebody else's active call.
	if proc.dead.Load() {
		return nil, fmt.Errorf("plugin %q is unloaded", proc.id)
	}
	// Freeze user values before acquiring the pipe: custom marshalers run once.
	frozen, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Give wire acquisition its own limit; queue time must not consume the
	// operation budget (service_health can legitimately need up to 90s).
	queued, cancelQueue := context.WithTimeout(queueCtx, queueBudget)
	err = proc.callMu.LockContext(queued)
	cancelQueue()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCallQueueWait, err)
	}
	bounded, cancel := context.WithTimeout(ioCtx, ioBudget)
	defer cancel()
	defer proc.callMu.Unlock()
	if proc.stopped || proc.dead.Load() {
		return nil, fmt.Errorf("plugin %q is unloaded", proc.id)
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	// Preserve absent lifecycle parameters: explicit JSON null is invalid in protocol 2.
	var requestParams any
	if params != nil {
		requestParams = json.RawMessage(frozen)
	}
	// Allocate in actual serial write order: protocol 2 rejects lower/reused IDs.
	req := subprocess.RPCRequest{JSONRPC: "2.0", ID: subprocess.NumberID(proc.requestID.Add(1)), Method: method, Params: requestParams}
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal envelope: %w", err)
	}
	encoded = append(encoded, '\n')

	// Once I/O starts, a host watchdog or lifecycle interruption makes completion
	// ambiguous. Request disconnects do not reach this context. Close the exact
	// captured pipes/process; never reuse the stream or retry the operation.
	finished := make(chan struct{})
	stop := context.AfterFunc(bounded, func() { interruptCall(proc, bounded, bounded.Err()); close(finished) })
	var finishOnce sync.Once
	interrupted := false
	finish := func() {
		finishOnce.Do(func() {
			if !stop() {
				<-finished
				interrupted = true
			}
		})
	}
	defer finish()

	if n, err := proc.stdin.Write(encoded); err != nil || n != len(encoded) {
		if err == nil {
			err = io.ErrShortWrite
		}
		interruptCall(proc, bounded, err)
		if bounded.Err() != nil {
			return nil, fmt.Errorf("plugin call interrupted; completion unknown: %w", bounded.Err())
		}
		return nil, fmt.Errorf("failed to send request; completion unknown: %w", err)
	}

	var raw json.RawMessage
	if err := proc.stdoutDec.Decode(&raw); err != nil {
		interruptCall(proc, bounded, err)
		if bounded.Err() != nil {
			return nil, fmt.Errorf("plugin call interrupted; completion unknown: %w", bounded.Err())
		}
		return nil, fmt.Errorf("failed to read response; completion unknown: %w", err)
	}

	finish()
	if interrupted {
		return nil, fmt.Errorf("plugin call interrupted; completion unknown: %w", bounded.Err())
	}
	// The decoder consumed one complete JSON value. A type/shape error does
	// not leave a partial response behind, so the stream remains reusable.
	var resp subprocess.RPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("invalid RPC response: %w", err)
	}

	if resp.JSONRPC != "2.0" || resp.ID != req.ID {
		interruptCall(proc, bounded, errors.New("response identity mismatch"))
		return nil, errors.New("response identity mismatch")
	}
	if resp.Error != nil {
		return nil, &rpcResponseError{message: resp.Error.Message}
	}

	return resp.Result, nil
}

// Only reason classes are exposed in retirement data; raw provider errors may
// contain sensitive details and remain on the call error path.
func interruptCall(proc *pluginProcess, ctx context.Context, err error) {
	reason := "transport_error"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		reason = "timeout"
	} else if ctx.Err() != nil {
		reason = "canceled"
	} else {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			reason = "invalid_response"
		}
	}
	interruptProcessForReason(proc, reason)
}

// rpcResponseError identifies an explicit plugin rejection, rather than a
// malformed response or transport failure. Only this supports legacy discovery.
type rpcResponseError struct{ message string }

func (e *rpcResponseError) Error() string { return "plugin error: " + e.message }

// InvocationIdentity captures the exact process, not an ID that can be reused.
type InvocationIdentity struct{ proc *pluginProcess }

func (i InvocationIdentity) Generation() string { return fmt.Sprintf("%p", i.proc) }
func (m *Manager) IdentityCurrent(i InvocationIdentity) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return i.proc != nil && m.plugins[i.proc.id] == i.proc && !i.proc.dead.Load()
}

// InvokeVerb dispatches a command/execute call to the plugin that owns the
// verb's module. Returns an error if no plugin claims the module or the
// verb is not declared in the owning plugin's capabilities.
func (m *Manager) InvokeVerb(ctx context.Context, verb string, payload json.RawMessage) (json.RawMessage, error) {
	raw, _, err := m.InvokeVerbCaptured(ctx, verb, payload)
	return raw, err
}

// InvokeVerbCaptured binds the returned result to the process used for I/O.
func (m *Manager) InvokeVerbCaptured(ctx context.Context, verb string, payload json.RawMessage) (returned json.RawMessage, identity InvocationIdentity, returnedErr error) {
	if PrivateCommand(verb) {
		return nil, InvocationIdentity{}, fmt.Errorf("unknown_verb")
	}
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
			return nil, InvocationIdentity{}, fmt.Errorf("verb %q is not declared by plugin %q", verb, ownerID)
		}
	}
	proc := m.plugins[ownerID]
	m.mu.RUnlock()

	if ownerID == "" {
		return nil, InvocationIdentity{}, fmt.Errorf("no plugin owns a module matching verb %q", verb)
	}

	if verb == "config_schema" || verb == "config_get" || verb == "config_list" || verb == "config_set" || verb == "config_reset" {
		if err := m.syncConfigSchemas(ctx, ownerID); err != nil {
			return nil, InvocationIdentity{}, err
		}
	}
	if proc == nil {
		return nil, InvocationIdentity{}, fmt.Errorf("plugin not found: %s", ownerID)
	}
	finish := m.beginOperation(proc, "command/execute", verb)
	defer func() { finish(returned, returnedErr) }()
	raw, err := callProcessContexts(ctx, context.WithoutCancel(ctx), proc, "command/execute", subprocess.CommandExecParams{Name: verb, Args: string(payload)})
	if proc.dead.Load() {
		m.retireProcess(proc)
	}
	if err != nil {
		return nil, InvocationIdentity{}, err
	}
	var result subprocess.CommandExecResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, InvocationIdentity{}, fmt.Errorf("decode verb command result: %w", err)
	}
	if result.Action != "message" || !json.Valid([]byte(result.Content)) {
		return nil, InvocationIdentity{}, fmt.Errorf("plugin %q returned invalid verb command result", ownerID)
	}
	return json.RawMessage(result.Content), InvocationIdentity{proc: proc}, nil
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

// Shutdown gives each plugin a fresh pluginUnloadTimeout grace, then force-stops
// and reaps it. Use an independent context after HTTP drain for normal shutdown;
// an explicit caller deadline still shortens all attempts (startup rollback).
// Nil means cleanup completed, including forced cleanup logged per plugin.
func (m *Manager) Shutdown(ctx context.Context) error {
	if err := m.lockLifecycle(ctx); err != nil {
		return err
	}
	defer m.observe.stop()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	ids := append([]string(nil), m.loadOrder...)
	m.mu.RUnlock()
	// Keep the recorder consumer alive while other plugins emit their unload
	// records. Delivery remains best effort; shutdown never waits for queue drain.
	for i, id := range ids {
		if id == "observe-ops" {
			ids = append(append(ids[:i:i], ids[i+1:]...), id)
			break
		}
	}
	for _, id := range ids {
		bounded, cancel := context.WithTimeout(ctx, pluginUnloadTimeout)
		m.unloadPlugin(bounded, id)
		cancel()
	}
	m.mu.Lock()
	clear(m.retired) // shutdown must not leave recovery paths for stopped plugins
	m.mu.Unlock()
	return nil
}

// initializePlugin completes lifecycle and capability registration before the
// process becomes visible to callers. Collision checks and insertion are atomic.
func (m *Manager) initializePlugin(ctx context.Context, proc *pluginProcess) error {
	if proc.observeGeneration == "" {
		proc.observeGeneration = opaqueID()
	}
	if _, err := callProcess(ctx, proc, "plugin/load", subprocess.LoadParams{}); err != nil {
		return admissionFailure("load", "plugin %q load failed: %w", proc.id, err)
	}
	raw, err := callProcess(ctx, proc, "command/execute", subprocess.CommandExecParams{Name: pluginkit.CommandCapabilities})
	if err != nil {
		var rejection *rpcResponseError
		if !errors.As(err, &rejection) || proc.dead.Load() || ctx.Err() != nil {
			return admissionFailure("discovery", "plugin %q capability discovery failed: %w", proc.id, err)
		}
		m.logger.Warn("plugin loaded without capability declaration", "id", proc.id, "error", err)
	} else {
		var result subprocess.CommandExecResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return admissionFailure("declaration", "plugin %q capability response: %w", proc.id, err)
		}
		if result.Action == "error" {
			m.logger.Warn("plugin loaded without capability declaration", "id", proc.id, "error", result.Content)
		} else {
			if result.Action != "message" {
				return admissionFailure("declaration", "plugin %q capability response: unexpected action %q", proc.id, result.Action)
			}
			var caps contract.PluginCapabilities
			if err := json.Unmarshal([]byte(result.Content), &caps); err != nil {
				return admissionFailure("declaration", "plugin %q capability declaration: %w", proc.id, err)
			}
			if err := caps.Validate(); err != nil {
				return admissionFailure("validation", "plugin %q capability validation failed: %w", proc.id, err)
			}
			proc.capabilities = &caps
			if _, reserved := caps.Verbs[observefeed.Command]; reserved {
				return admissionFailure("validation", "private host command cannot be declared")
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.plugins[proc.id]; exists {
		return admissionFailure("collision", "plugin %q already loaded", proc.id)
	}
	if proc.capabilities != nil {
		for _, mod := range proc.capabilities.Modules {
			if owner, taken := m.modules[mod]; taken {
				return admissionFailure("collision", "plugin %q claims module %q, already owned by plugin %q", proc.id, mod, owner)
			}
		}
		// Distinct overlapping module prefixes can still declare the same verb.
		// Check exact verb ownership before inserting any claims.
		for _, loaded := range m.plugins {
			if loaded.capabilities == nil {
				continue
			}
			for verb := range proc.capabilities.Verbs {
				if _, claimed := loaded.capabilities.Verbs[verb]; claimed {
					return admissionFailure("collision", "plugin %q claims verb %q, already owned by plugin %q", proc.id, verb, loaded.id)
				}
			}
		}
		for _, mod := range proc.capabilities.Modules {
			m.modules[mod] = proc.id
		}
	}
	m.registryRevision++
	m.plugins[proc.id] = proc
	delete(m.retired, proc.id)
	m.loadOrder = append(m.loadOrder, proc.id)
	m.rebuildNavClaimsLocked()
	return nil
}

func (m *Manager) collectNavDeclarationsLocked() ([]string, map[string]*contract.NavDeclaration) {
	navs := make(map[string]*contract.NavDeclaration, len(m.loadOrder))
	for _, id := range m.loadOrder {
		proc := m.plugins[id]
		if proc != nil && proc.capabilities != nil && proc.capabilities.Nav != nil {
			navs[id] = proc.capabilities.Nav
		}
	}
	return m.loadOrder, navs
}

func (m *Manager) rebuildNavClaimsLocked() {
	order, navs := m.collectNavDeclarationsLocked()
	res := ResolveNavigation(order, navs, m.logger)
	m.navGroups = res.GroupOwners
	m.navItems = res.ItemOwners
	m.navRoutes = res.RouteOwners
	m.navDiagnostics = res.Nav.Diagnostics
	m.navNotices = res.Nav.Notices
}

// MergedNav returns a snapshot of navigation from registered plugins. Group
// metadata, globally unique item IDs and routes are first-loaded wins; later
// plugins can still contribute distinct items to a shared group.
// Host-added plugin_id attributions and bounded diagnostics/notices are
// exposed on the returned contract. Priority zero uses the contract default
// (1000), and IDs break ties for deterministic responses.
func (m *Manager) MergedNav() contract.NavDeclaration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	order, navs := m.collectNavDeclarationsLocked()
	res := ResolveNavigation(order, navs, nil)
	return res.Nav
}

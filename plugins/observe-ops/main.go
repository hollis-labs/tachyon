// observe-ops is a subprocess plugin for Tachyon that provides read-only
// observability: activity feeds, structured logs, telemetry metrics,
// lifecycle events, aggregate health status, and subscription handles.
//
// It claims the "observe" module namespace and implements verb dispatch
// via the pluginkit shim (internal/pluginkit), which carries the ADR 001
// verb contract over the plugin-sdk v0.5.0 command/execute method.
//
// The plugin self-feeds: every verb invocation that flows through
// Command() is automatically instrumented with an activity entry, a
// lifecycle event, a latency metric, and (on error) a log line.
// Cross-plugin, Tether, and Nanite data is not wired yet.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

//go:embed capabilities.json
var capabilitiesJSON []byte

type plugin struct {
	adapter ObserveAdapter
	local   *LocalAdapter // concrete type for Record* calls
	caps    contract.PluginCapabilities
}

// --- pluginkit.VerbPlugin implementation ---

// Capabilities returns the embedded capability declaration for this plugin.
func (p *plugin) Capabilities() contract.PluginCapabilities {
	return p.caps
}

// HandleVerb dispatches a verb invocation to the appropriate adapter
// method. This is the pluginkit.VerbPlugin interface method.
func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	return p.instrumentedHandleVerb(ctx, verb, payload)
}

// instrumentedHandleVerb wraps handleVerb with self-feeding instrumentation:
// an activity entry and event for every call, a latency_ms metric, and a
// log line on errors.
func (p *plugin) instrumentedHandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	start := time.Now()
	env, err := p.handleVerb(ctx, verb, payload)
	elapsed := time.Since(start)

	// Record activity entry for every verb invocation.
	p.local.RecordActivity(ActivityEntry{
		Kind:    "verb",
		Source:  "observe-ops",
		Actor:   "observe-ops",
		Summary: fmt.Sprintf("verb %s invoked", verb),
		Detail:  map[string]any{"verb": verb, "status": string(env.Status)},
	})

	// Record lifecycle event.
	p.local.RecordEvent(Event{
		Kind:   "verb_invoked",
		Source: "observe-ops",
		Payload: map[string]any{
			"verb":       verb,
			"status":     string(env.Status),
			"latency_ms": elapsed.Milliseconds(),
		},
	})

	// Record latency metric.
	p.local.RecordMetric(MetricPoint{
		Name:  "latency_ms",
		Value: float64(elapsed) / float64(time.Millisecond),
		Unit:  "ms",
		Tags:  map[string]string{"verb": verb},
	})

	// Log errors.
	if env.Status == contract.StatusError && env.Error != nil {
		p.local.RecordLog(LogEntry{
			Level:   "error",
			Source:  "observe-ops",
			Message: fmt.Sprintf("verb %s failed: %s", verb, env.Error.Message),
			Fields:  map[string]any{"verb": verb, "code": env.Error.Code},
		})
	}

	return env, err
}

// --- subprocess.Plugin lifecycle ---

func (p *plugin) Init(_ context.Context, _ subprocess.InitParams) (subprocess.InitResult, error) {
	// Parse embedded capabilities.
	if err := json.Unmarshal(capabilitiesJSON, &p.caps); err != nil {
		return subprocess.InitResult{}, fmt.Errorf("parse embedded capabilities: %w", err)
	}
	if err := p.caps.Validate(); err != nil {
		return subprocess.InitResult{}, fmt.Errorf("validate capabilities: %w", err)
	}

	// Initialize the local adapter (MVP).
	la := NewLocalAdapter(1000)
	p.local = la
	p.adapter = la

	// Self-feed: record plugin initialization.
	p.local.RecordEvent(Event{
		Kind:    "plugin_init",
		Source:  "observe-ops",
		Payload: map[string]any{"version": "0.1.0"},
	})
	p.local.RecordLog(LogEntry{
		Level:   "info",
		Source:  "observe-ops",
		Message: "observe-ops plugin initialized",
	})

	return subprocess.InitResult{
		ID:          "observe-ops",
		Name:        "Observe Ops",
		Version:     "0.1.0",
		Description: "Read-only observability for Tachyon — activity feeds, logs, metrics, events, status",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *plugin) Load(_ context.Context) (subprocess.LoadResult, error) {
	// Self-feed: record plugin load.
	p.local.RecordEvent(Event{
		Kind:   "plugin_load",
		Source: "observe-ops",
	})
	p.local.RecordLog(LogEntry{
		Level:   "info",
		Source:  "observe-ops",
		Message: "observe-ops plugin loaded and ready",
	})

	return subprocess.LoadResult{}, nil
}

func (p *plugin) Unload(_ context.Context) error {
	return nil
}

// --- subprocess.CommandHandler ---

// Command dispatches command/execute calls. pluginkit.Dispatch handles
// verb invocations (including plugin_capabilities); unhandled commands
// fall through to legacy dispatch.
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	result, handled, err := pluginkit.Dispatch(ctx, p, req)
	if handled || err != nil {
		return result, err
	}
	// No legacy commands in observe-ops.
	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

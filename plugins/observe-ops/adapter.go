// Package main implements the observe-ops plugin for Tachyon.
//
// observe-ops provides read-only observability across agents and sessions:
// activity feeds, log retrieval, telemetry metrics, event streams, aggregate
// health status, and real-time subscription handles. It claims the "observe"
// module namespace and all verbs are classified as reads — this plugin
// never writes state.
//
// Local telemetry is supplemented by on-demand dependency probes and Nanite
// session snapshots. External history and streams remain deferred.
package main

import (
	"context"
	"encoding/json"
	"github.com/hollis-labs/tachyon/internal/observefeed"
	"time"
)

// ActivityEntry is a single item in the activity feed — a timestamped
// record of something that happened (verb invocation, lifecycle event,
// state transition).
type ActivityEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`    // "verb", "lifecycle", "state_change"
	Source    string    `json:"source"`  // plugin ID or subsystem
	Actor     string    `json:"actor"`   // who/what triggered the action
	Summary   string    `json:"summary"` // human-readable one-liner
	Detail    any       `json:"detail,omitempty"`
}

// LogEntry is a structured log line from an agent, session, or plugin.
type LogEntry struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`  // "debug", "info", "warn", "error"
	Source    string         `json:"source"` // plugin ID, agent ID, or subsystem
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// MetricPoint is a single telemetry measurement.
type MetricPoint struct {
	Name      string            `json:"name"` // e.g. "tokens_used", "latency_ms", "cost_usd"
	Value     float64           `json:"value"`
	Unit      string            `json:"unit,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Tags      map[string]string `json:"tags,omitempty"` // e.g. {"agent_id": "x", "verb": "y"}
}

// Event is a structured lifecycle event (creation, termination, error,
// state transition).
type Event struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Kind      string         `json:"kind"` // "agent_created", "session_ended", "error", "transition"
	Source    string         `json:"source"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// StatusSummary is aggregate health/status data for the dashboard.
type HostFeedStatus struct {
	Epoch        string               `json:"epoch"`
	Counters     observefeed.Counters `json:"counters"`
	LastSequence uint64               `json:"last_sequence"`
}

type StatusSummary struct {
	HostFeed          *HostFeedStatus    `json:"host_feed,omitempty"`
	Dependencies      []DependencyStatus `json:"dependencies,omitempty"`
	SessionCountKnown bool               `json:"session_count_known"`
	ActiveAgents      int                `json:"active_agents"`
	ActiveSessions    int                `json:"active_sessions"`
	ErrorCount        int                `json:"error_count"`
	HealthStatus      string             `json:"health_status"` // "healthy", "degraded", "unhealthy"
	UptimeSeconds     int64              `json:"uptime_seconds"`
	LastUpdated       time.Time          `json:"last_updated"`
}

// SubscriptionHandle describes stateless snapshot polling, not a registered
// subscription. Payload is sent as the JSON body to the same-origin endpoint.
type SubscriptionHandle struct {
	Channel         string          `json:"channel"`
	Endpoint        string          `json:"endpoint"`
	Method          string          `json:"method"`
	Payload         json.RawMessage `json:"payload"`
	Transport       string          `json:"transport"`
	Supported       bool            `json:"supported"`
	Mode            string          `json:"mode"`
	PollIntervalMS  int             `json:"poll_interval_ms"`
	MaxLimit        int             `json:"max_limit"`
	CursorSupported bool            `json:"cursor_supported"`
	Cursor          any             `json:"cursor"`
	DurableReplay   bool            `json:"durable_replay"`
}

// ActivityFilter constrains the activity feed query.
type ActivityFilter struct {
	Source  string `json:"source,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	SinceID string `json:"since_id,omitempty"`
}

// LogFilter constrains the log retrieval query.
type LogFilter struct {
	Source string `json:"source,omitempty"`
	Level  string `json:"level,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Since  string `json:"since,omitempty"` // RFC3339 timestamp
	Until  string `json:"until,omitempty"` // RFC3339 timestamp
	Search string `json:"search,omitempty"`
}

// MetricFilter constrains the metrics query.
type MetricFilter struct {
	Names []string          `json:"names,omitempty"`
	Tags  map[string]string `json:"tags,omitempty"`
	Since string            `json:"since,omitempty"`
	Until string            `json:"until,omitempty"`
	Limit int               `json:"limit,omitempty"`
}

// EventFilter constrains the event stream query.
type EventFilter struct {
	Kind    string `json:"kind,omitempty"`
	Source  string `json:"source,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	SinceID string `json:"since_id,omitempty"`
}

// SubscribeRequest requests a polling descriptor. Filter is a JSON object
// encoded as a string, using the selected channel's read-filter fields.
type SubscribeRequest struct {
	Channel string `json:"channel"` // "activity", "logs", "events"
	Filter  string `json:"filter,omitempty"`
}

// ObserveAdapter is the provider-neutral interface for observability data.
// Implementations aggregate from different sources (local event log,
// Tether sessions, Flux activity, OTel metrics).
type ObserveAdapter interface {
	// ListActivity returns the recent activity feed, filtered by the
	// given criteria. Entries are returned newest-first.
	ListActivity(ctx context.Context, filter ActivityFilter) ([]ActivityEntry, error)

	// ListLogs returns structured log entries matching the filter.
	ListLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error)

	// ListMetrics returns telemetry metric points matching the filter.
	ListMetrics(ctx context.Context, filter MetricFilter) ([]MetricPoint, error)

	// ListEvents returns structured lifecycle events matching the filter.
	ListEvents(ctx context.Context, filter EventFilter) ([]Event, error)

	// Status returns aggregate health/status data for the dashboard.
	Status(ctx context.Context) (*StatusSummary, error)

	// Subscribe describes stateless snapshot polling for a supported channel.
	Subscribe(ctx context.Context, req SubscribeRequest) (*SubscriptionHandle, error)
}

// Package agentsapi is the contract for Tachyon's Agents monitor backend: the
// route list, the Session shape and the stream envelope, plus a fixture-backed
// stub the Agents GUI builds against (TACHYON_AGENTS_STUBS=1). Real slices
// replace the stub route by route; the shapes in openapi.yaml are the final ones.
package agentsapi

// Prefix is where agents routes mount. It must stay inside the operator session
// cookie's path (approvals.CookiePath).
const Prefix = "/api/plugins/console/v1/agents"

// Session states. stale carries a StaleReason; waiting keeps needs_you even
// when the session is also stale.
const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateIdle    = "idle"
	StateStale   = "stale"
	StateUnknown = "unknown"
)

// Stale reasons.
const (
	StaleNoEvents      = "no_events"      // no event for stale_after (default 15 min)
	StaleNotAlive      = "not_alive"      // Tether says the session is not alive
	StaleWorkerOffline = "worker_offline" // the hosting worker is offline
)

// Stream event types on GET Prefix/stream. Flat JSON {type, at, ...}.
const (
	EventSession   = "session"   // Session partial upsert
	EventSummary   = "summary"   // per-state counts
	EventCollector = "collector" // collector status
	EventBus       = "bus"       // BusEvent
	EventControl   = "control"   // control request state change
	EventGap       = "gap"       // collection gap marker
)

// Control kinds and states.
const (
	ControlNudge   = "nudge"
	ControlRestart = "restart"
)

// IDed reports whether a stream event type is replayable: it carries an SSE
// `id:` and is replayed after Last-Event-ID. The rest are live snapshots; on a
// gap or reconnect the client refetches GET Prefix/sessions.
func IDed(t string) bool {
	switch t {
	case EventBus, EventControl, EventGap:
		return true
	}
	return false
}

// MaxNudgeText bounds a nudge body (characters).
const MaxNudgeText = 4000

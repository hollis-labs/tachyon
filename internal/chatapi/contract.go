// Package chatapi is the contract for Tachyon's chat backend: the route list,
// the thread_ref scheme and the event envelope, plus a fixture-backed stub the
// chat GUI builds against (TACHYON_CHAT_STUBS=1). Real slices replace the stub
// route by route; the shapes in openapi.yaml are the final ones.
package chatapi

import (
	"errors"
	"strings"
)

// Prefix is where chat routes mount. It must stay inside the operator session
// cookie's path (approvals.CookiePath).
const Prefix = "/api/plugins/console/v1/chat"

// TerminalPrefix is the quick-terminal route family. Every method, GET included,
// needs operator assurance because it is an unrestricted shell.
const TerminalPrefix = "/api/plugins/console/v1/terminal"

// Thread sources. A thread_ref is "<source>:<id>".
const (
	SourceApp    = "app"    // app_agent_threads.id
	SourceTether = "tether" // chatbridge grouping key: session_id, else local_session_id
)

var ErrBadRef = errors.New("chatapi: thread_ref must be app:<id> or tether:<session_id>")

// ThreadRef is the Tachyon-owned stable scope key for all thread data. It is
// not the runtime session id, which may change on resume, compact or fork.
type ThreadRef struct{ Source, ID string }

func ParseRef(s string) (ThreadRef, error) {
	src, id, ok := strings.Cut(s, ":")
	if !ok || id == "" || (src != SourceApp && src != SourceTether) || strings.ContainsAny(id, "/\\ \t\r\n") {
		return ThreadRef{}, ErrBadRef
	}
	return ThreadRef{Source: src, ID: id}, nil
}

func (r ThreadRef) String() string { return r.Source + ":" + r.ID }

// Event types carried on the thread SSE stream. Every event is a flat JSON
// object {type, thread_ref, at, ...}; see openapi.yaml#/components/schemas/Event.
const (
	EventMessage   = "message"
	EventDelta     = "delta"
	EventTool      = "tool"
	EventEnd       = "end"
	EventDelivery  = "delivery"
	EventCard      = "card"
	EventPlan      = "plan"
	EventActivity  = "activity"
	EventPanel     = "panel"
	EventTopic     = "topic"
	EventBookmark  = "bookmark"
	EventWorkspace = "workspace"
	EventContext   = "context"
	EventThread    = "thread"
	EventTerminal  = "terminal.run"
)

// IDed reports whether an event type is activity-class: it carries an SSE
// `id:` (chat_activity.seq) and is replayed after Last-Event-ID. The rest are
// live signals; a reconnecting client refetches state instead.
func IDed(t string) bool {
	switch t {
	case EventMessage, EventDelivery, EventActivity, EventPlan, EventCard, EventTopic, EventBookmark, EventWorkspace, EventTerminal:
		return true
	}
	return false
}

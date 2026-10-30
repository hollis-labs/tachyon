// Package assurance is the one interface the approvals code asks "how sure are we
// who this is, and is that enough for this action?". Today its only implementation
// wraps the Tangent session bridge (approvals.TangentProvider). Batch, defer,
// secret and access code call Provider and Required, never the bridge, so a later
// login-validated confirmation plugs in here and nowhere else.
package assurance

import (
	"net/http"
	"time"
)

// Level is an ordered strength of identity assurance. The names are the wire
// spellings the bridge already uses.
type Level int

const (
	LevelNone           Level = iota // no usable session
	LevelBrowserSession              // an operator browser session (cookie)
	LevelOperatorToken               // elevated: the operator key was entered recently
)

func (l Level) String() string {
	switch l {
	case LevelBrowserSession:
		return "browser-session"
	case LevelOperatorToken:
		return "operator-token"
	}
	return "none"
}

// ParseLevel maps the bridge's assurance spelling to a Level; anything unknown is none.
func ParseLevel(s string) Level {
	switch s {
	case "browser-session":
		return LevelBrowserSession
	case "operator-token":
		return LevelOperatorToken
	}
	return LevelNone
}

// Action names something a session may try to do to the queue.
type Action string

const (
	ActionRead          Action = "read"           // inbox, items, history, audit, events, access
	ActionPresent       Action = "present"        // mark an item presented
	ActionDefer         Action = "defer"          // snooze or un-snooze; decides nothing
	ActionResolveLow    Action = "resolve_low"    // class low
	ActionResolveMedium Action = "resolve_medium" // class medium
	ActionResolveHigh   Action = "resolve_high"   // class high or cerberus source (typed phrase on top)
	ActionDestructive   Action = "resolve_destructive"
	ActionBatch         Action = "batch_resolve" // the whole batch, even for low items
	ActionSecretSubmit  Action = "secret_submit"
	ActionActionRunRead Action = "action_run_read" // command and output of a registered action run
)

// Requirements is the table of the least Level each action needs. Unknown
// actions need the strongest level (fail closed). Changing a row here changes
// behaviour everywhere; the contract's /access required_for is derived from it.
var Requirements = map[Action]Level{
	ActionRead:          LevelBrowserSession,
	ActionPresent:       LevelBrowserSession,
	ActionDefer:         LevelBrowserSession,
	ActionResolveLow:    LevelBrowserSession,
	ActionResolveMedium: LevelOperatorToken,
	ActionResolveHigh:   LevelOperatorToken,
	ActionDestructive:   LevelOperatorToken,
	ActionBatch:         LevelOperatorToken,
	ActionSecretSubmit:  LevelOperatorToken,
	ActionActionRunRead: LevelOperatorToken,
}

// Required returns the Level an action needs.
func Required(a Action) Level {
	if l, ok := Requirements[a]; ok {
		return l
	}
	return LevelOperatorToken
}

// Satisfies reports whether have is enough for the action.
func Satisfies(have Level, a Action) bool { return have >= Required(a) }

// Status is what the provider knows about the caller's session.
type Status struct {
	Level Level
	// ExpiresAt is when elevation ends; zero when unknown or not elevated.
	ExpiresAt time.Time
	// TTLPolicy is operator-facing copy for how long elevation lasts ("30m"); may be empty.
	TTLPolicy string
	// CanElevate says whether elevating would raise the level.
	CanElevate bool
	// Ref is a stable opaque reference to the session (never the cookie value).
	Ref string
}

// Elevated reports whether the level is operator-token.
func (s Status) Elevated() bool { return s.Level >= LevelOperatorToken }

// Error is a refusal the HTTP layer writes as {code,message} with Status.
type Error struct {
	Status  int
	Code    string
	Message string
	// Body, when set, is an upstream error body to pass through unchanged.
	Body string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Provider is the single swap point for session assurance.
type Provider interface {
	// Status resolves the caller's session. No session is a 401 session_required Error.
	Status(r *http.Request) (Status, *Error)
	// Require is Status plus the table check: a 403 operator_access_required Error when the level is too low.
	Require(r *http.Request, a Action) (Status, *Error)
	// Elevate and DeElevate run the provider's own flow and write its response (including cookies).
	Elevate(w http.ResponseWriter, r *http.Request)
	DeElevate(w http.ResponseWriter, r *http.Request)
}

// RequireOn is Require for any Provider that only implements Status.
func RequireOn(p Provider, r *http.Request, a Action) (Status, *Error) {
	st, err := p.Status(r)
	if err != nil {
		return st, err
	}
	if !Satisfies(st.Level, a) {
		return st, &Error{Status: http.StatusForbidden, Code: "operator_access_required", Message: "Operator access is needed for this action."}
	}
	return st, nil
}

// Access is the normalized GET /access body. The UI never sees provider shapes.
type Access struct {
	State      string      `json:"state"` // none | elevated
	ExpiresInS int         `json:"expires_in_s"`
	CanElevate bool        `json:"can_elevate"`
	TTLPolicy  string      `json:"ttl_policy"`
	Required   RequiredFor `json:"required_for"`
}

// RequiredFor says which things need elevation, derived from Requirements.
type RequiredFor struct {
	Batch       bool `json:"batch"`
	Medium      bool `json:"medium"`
	High        bool `json:"high"`
	Destructive bool `json:"destructive"`
	Secrets     bool `json:"secrets"`
}

// View builds the /access body at now.
func View(st Status, now time.Time) Access {
	a := Access{State: "none", CanElevate: st.CanElevate, TTLPolicy: st.TTLPolicy}
	if st.Elevated() {
		a.State = "elevated"
		if !st.ExpiresAt.IsZero() && st.ExpiresAt.After(now) {
			a.ExpiresInS = int(st.ExpiresAt.Sub(now).Seconds())
		}
	}
	need := func(x Action) bool { return Required(x) >= LevelOperatorToken }
	a.Required = RequiredFor{Batch: need(ActionBatch), Medium: need(ActionResolveMedium), High: need(ActionResolveHigh), Destructive: need(ActionDestructive), Secrets: need(ActionSecretSubmit)}
	return a
}

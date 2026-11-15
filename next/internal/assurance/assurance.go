// Package assurance is the approvals assurance check on Substrate identity (port plan section 4).
// It replaces the old Tangent session bridge and step-up elevation: there is no elevation step and
// no session of Tachyon's own. The level comes from the verified principal alone:
//
//   - browser-session: a signed-in person (principal kind "user", from a session, or a token
//     minted from one);
//   - operator-token: a signed-in person who also holds tachyon.operator (the owner does,
//     through "*");
//   - none: anything else, notably an API key or a token minted from a key, whatever its scopes
//     ("*" included), and an unauthenticated request.
//
// The action table is the old one (internal/approvals/assurance), unchanged; an unknown action
// needs operator-token (fail closed). The confirm phrase for high, cerberus and destructive items
// stays where it was, in the approvals code: it is checked after this.
package assurance

import (
	"encoding/json"
	"net/http"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// Level is an assurance level, ordered.
type Level int

// The levels.
const (
	None Level = iota
	BrowserSession
	OperatorToken
)

func (l Level) String() string {
	switch l {
	case BrowserSession:
		return "browser-session"
	case OperatorToken:
		return "operator-token"
	case None:
		return "none"
	}
	return "none"
}

// OperatorScope is the scope that makes a signed-in person an operator. It is never a route scope
// and never granted to a key.
const OperatorScope = "tachyon.operator"

// Action is an action the assurance table knows.
type Action string

// The table: the old build's, plus planner.write and chat.write (operator, as before).
var table = map[Action]Level{
	"read":                    BrowserSession,
	"present":                 BrowserSession,
	"defer":                   BrowserSession,
	"resolve_low":             BrowserSession,
	"account.write":           BrowserSession,
	"transcripts.read":        BrowserSession,
	"settings.write":          BrowserSession,
	"resolve_medium":          OperatorToken,
	"resolve_high":            OperatorToken,
	"resolve_destructive":     OperatorToken,
	"batch_resolve":           OperatorToken,
	"secret_submit":           OperatorToken,
	"action_run_read":         OperatorToken,
	"settings.write_operator": OperatorToken,
	// Planner writes: the old build wrapped them in RequireOperator, outside this table.
	"planner.write": OperatorToken,
	// Chat writes and the workspace file content read: RequireOperator / RequireOperatorReads.
	"chat.write": OperatorToken,
	// Agents writes (settings, nudge, restart, skip, launches): RequireOperator.
	"agents.control": OperatorToken,
}

// Actions is every action in the table.
func Actions() []Action {
	out := make([]Action, 0, len(table))
	for a := range table {
		out = append(out, a)
	}
	return out
}

// Required is the level an action needs; an unknown action needs operator-token.
func Required(a Action) Level {
	if l, ok := table[a]; ok {
		return l
	}
	return OperatorToken
}

// Of is the level of a principal.
func Of(p libauth.Principal) Level {
	if !p.Verified || p.Kind != "user" || !strings.HasPrefix(p.ID, "user:") {
		return None
	}
	if p.HasScope(OperatorScope) {
		return OperatorToken
	}
	return BrowserSession
}

// Error is a refusal, in the flat {code, message} shape.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  Action `json:"action,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Require returns nil when the request's principal reaches the level the action needs. An
// unauthenticated request is 401; a key, or a person below the level, is 403 operator_required.
func Require(r *http.Request, a Action) *Error {
	p, ok := libauth.FromContext(r.Context())
	if !ok || !p.Verified {
		return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Sign in first.", Action: a}
	}
	if Of(p) < Required(a) {
		return &Error{Status: http.StatusForbidden, Code: "operator_required",
			Message: "This action needs a signed-in person holding " + OperatorScope + "; an API key never qualifies.", Action: a}
	}
	return nil
}

// Middleware serves next only when Require passes for the action.
func Middleware(a Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := Require(r, a); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(err.Status)
			_ = json.NewEncoder(w).Encode(err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

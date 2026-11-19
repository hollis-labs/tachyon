package approvals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
)

// The approval inbox on Substrate identity (port plan section 4). The old build asked the Tangent
// session bridge who the caller was and how strongly they had signed in, through a cookie only its
// /session and /elevate routes minted. Here the caller is the verified principal the auth guard put
// on the request, and the strength is assurance.Of: a signed-in person is a browser session; one
// holding tachyon.operator is an operator token. There is no session, elevation, access or step-up
// route of Tachyon's own.

// Prefix is the inbox's path prefix.
const Prefix = "/api/plugins/console/v1/approval-inbox"

var itemPath = regexp.MustCompile(`^/items/[a-zA-Z0-9_-]+$`)

// The old assurance actions the inbox checks (internal/assurance holds the table).
const (
	actionResolveMedium assurance.Action = "resolve_medium"
	actionResolveHigh   assurance.Action = "resolve_high"
	actionDestructive   assurance.Action = "resolve_destructive"
	actionBatch         assurance.Action = "batch_resolve"
	actionSecretSubmit  assurance.Action = "secret_submit"
	actionRunRead       assurance.Action = "action_run_read"
)

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

// participant is the caller as the inbox records it. Only a signed-in person answers: a request
// without a verified principal is 401 session_required, and an API key (or a token minted from one)
// is 403 requester_cannot_approve, as a bearer requester was in the old build.
func participant(w http.ResponseWriter, r *http.Request) (hitl.Participant, bool) {
	p, ok := libauth.FromContext(r.Context())
	if !ok || !p.Verified {
		fail(w, http.StatusUnauthorized, "session_required", "Sign in to use the approval inbox.")
		return hitl.Participant{}, false
	}
	level := assurance.Of(p)
	if level == assurance.None {
		fail(w, http.StatusForbidden, "requester_cannot_approve", "Use a signed-in browser session for inbox access.")
		return hitl.Participant{}, false
	}
	return hitl.Participant{PrincipalRef: p.ID, Authority: "tachyon/identity", Assurance: level.String(),
		Responder: &hitl.Responder{Kind: "human-session", Ref: p.ID}}, true
}

// require answers the refusal itself and reports false unless the caller reaches the action's level
// (403 operator_required below it).
func require(w http.ResponseWriter, r *http.Request, a assurance.Action) bool {
	if err := assurance.Require(r, a); err != nil {
		fail(w, err.Status, err.Code, err.Message)
		return false
	}
	return true
}

func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: invalid JSON command", hitl.ErrInvalidRequest)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("%w: exactly one JSON command is required", hitl.ErrInvalidRequest)
	}
	return nil
}

func commandBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return nil, fmt.Errorf("%w: use application/json", hitl.ErrInvalidRequest)
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid or oversized command", hitl.ErrInvalidRequest)
	}
	return raw, nil
}

func writeValue(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// writeHITLError answers a hitl error in the contract's flat shape: the hitl code and message at the
// top, and the rest of the hitl body (contract version, revision kind, expected and actual revision,
// current state, terminal outcome) under details.
func writeHITLError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNativeUnconfirmed):
		fail(w, 503, "unavailable", "Native response needs reconciliation; no approval evidence is available.")
		return
	case errors.Is(err, hitl.ErrInvalidRequest):
		status = 400
	case errors.Is(err, hitl.ErrNotFound):
		status = 404
	case errors.Is(err, hitl.ErrUnauthorized):
		status = 403
	case errors.Is(err, hitl.ErrStaleRevision), errors.Is(err, hitl.ErrIdempotencyConflict), errors.Is(err, hitl.ErrTerminalConflict):
		status = 409
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = 408
	}
	if status == 500 {
		fail(w, status, "unavailable", "Approval storage is unavailable; fetch the retained item before retrying.")
		return
	}
	var body map[string]any
	if json.Unmarshal(hitl.EncodeError(err), &body) != nil {
		fail(w, status, "invalid", err.Error())
		return
	}
	out := map[string]any{"code": body["code"], "message": body["message"]}
	delete(body, "code")
	delete(body, "message")
	if len(body) > 0 {
		out["details"] = body
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

// legacyOperatorRef is the old build's operator session ref: "browser-session:" and the SHA-256 of its
// built-in operator cookie value, as the old inbox computed it. It maps to the owner.
var legacyOperatorRef = func() string {
	sum := sha256.Sum256([]byte("dev-posture"))
	return "browser-session:" + hex.EncodeToString(sum[:])
}()

// selfRefs are the actor refs history and audit label "You" for this caller: the caller's own, and,
// for the owner (a signed-in operator), the old build's operator session ref (read time only; stored
// rows keep their refs). Other old browser-session refs stay "Operator session", as the old build
// labeled every session but the caller's.
func selfRefs(p hitl.Participant) []string {
	refs := []string{p.PrincipalRef}
	if p.Assurance == assurance.OperatorToken.String() {
		refs = append(refs, legacyOperatorRef)
	}
	return refs
}

// refSuffix is the short, stable part of a principal ref that keys one presentation event per caller
// and item. The old refs were "browser-session:<sha-256>" and their last 12 characters served; a
// principal id can be shorter, so this hashes it.
func refSuffix(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(sum[:6])
}

// flag is a bool as the 0 or 1 an integer flag column holds. SQLite stored a bound Go bool that way;
// Postgres refuses a bool for an integer column.
func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}

// likeEscaper escapes the LIKE wildcards and the escape character itself.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// contains is the LIKE pattern (with ESCAPE '\') matching any text that holds q. The old build used
// SQLite's instr; Postgres has none, and both dialects run this form.
func contains(q string) string { return "%" + likeEscaper.Replace(q) + "%" }

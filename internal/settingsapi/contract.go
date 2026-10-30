// Package settingsapi is the contract for Tachyon's Settings backend: the admin manifest (general settings with
// provenance), the Account/whoami profile, and the assurance extension the sign-in and step-up dialog is built
// against. A fixture-free stub (TACHYON_SETTINGS_STUBS=1) lets the GUI build against all of it. Design:
// design_tachyon_settings_account_assurance_backend_20261030.
//
// Settings reuse the libs admin contract v1 (ui-go envelopes/admin and adminhttp) instead of a Tachyon protocol, so
// the wire shapes below are the library's. This package adds what the library leaves to the host: the group
// declarations, the account and the assurance extension.
package settingsapi

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

// Root is the console API root. It must stay inside the operator session cookie's path.
const Root = "/api/plugins/console/v1"

const (
	// AdminPrefix is where adminhttp serves the manifest and the per-group routes (BasePath is Root).
	AdminPrefix = Root + "/admin"
	// AccountPath is the whoami read; ProfilePath is the profile write.
	AccountPath = Root + "/account"
	ProfilePath = AccountPath + "/profile"
	// StubAccessPrefix is where the stub serves the EXTENDED access contract. The real routes extend the
	// existing approval-inbox /access (ST4) and add /access/challenge and /access/verify beside it; the stub
	// uses its own prefix so it can run next to the real approvals mount.
	StubAccessPrefix = Root + "/settings-stub/access"
	// RealAccessPath is the existing route ST4 extends.
	RealAccessPath = Root + "/approval-inbox/access"
)

// Route is one stubbed route: the declaration the contract test compares with the admin library's layout.
type Route struct{ Method, Path string }

// AdminRoutes lists the per-group routes adminhttp serves for a group id.
func AdminRoutes(group string) []Route {
	b := AdminPrefix + "/settings/" + group
	return []Route{{"GET", b}, {"POST", b + "/validate"}, {"POST", b + "/update"}, {"POST", b + "/reset"}}
}

// Group ids in the initial registry (ST2 registers the real ones; fileview, forge and approvals follow).
const (
	GroupGeneral     = "general"
	GroupChat        = "chat"
	GroupAgents      = "agents"
	GroupTranscripts = "transcripts"
	GroupAlerts      = "alerts"
)

// ManifestRevision is the stub's declaration revision. The real one is a hash of all declarations.
const ManifestRevision = "stub-r1"

// Error codes beyond the admin library's (malformed_input, unauthenticated, forbidden, unknown_resource,
// manifest_changed 409, value_conflict 412, validation_failed 422, precondition_required 428, unsupported,
// backend_unavailable).
const (
	CodeOperatorAccess      = "operator_access_required" // the existing assurance code (the design called it assurance_insufficient)
	CodeInvalidCredentials  = "invalid_credentials"
	CodeLocked              = "locked"
	CodeChallengeExpired    = "challenge_expired"
	CodeMethodUnavailable   = "method_unavailable"
	CodeValidationFailed    = "validation_failed"
	CodeOriginDenied        = "origin_denied"
	CodeSessionRequired     = "session_required"
	CodeUnsupportedProvider = "unsupported"
)

// Limits.
const (
	MaxDisplayName  = 80
	MaxEmail        = 254
	MaxBodyBytes    = 64 << 10
	LockoutAfter    = 5  // failed attempts per window before locked (matches Tangent's 5/min)
	LockoutWindowS  = 60 // estimated; Tangent sends no Retry-After (request R1)
	ChallengeTTLS   = 120
	StubElevationS  = 30 * 60
	MaxProfileBytes = 4 << 10
)

// Auth methods the dialog may offer. Only operator_token exists today; the rest arrive with the hardening Provider.
const (
	MethodOperatorToken = "operator_token"
	MethodPassword      = "password"
	MethodPasskey       = "passkey"
)

// Method says whether a sign-in method can be used now.
type Method struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
}

// Session is the browser session's state, derived from the elevation expiry.
type Session struct {
	State     string `json:"state"` // active | expired | none
	ExpiresAt string `json:"expires_at,omitempty"`
}

// Lockout is the sign-in rate-limit state. AttemptsLeft is omitted until Tangent reports it (R1).
type Lockout struct {
	Locked       bool `json:"locked"`
	RetryAfterS  int  `json:"retry_after_s,omitempty"`
	AttemptsLeft *int `json:"attempts_left,omitempty"`
}

// StepUpSupport says whether /access/challenge works (false until the hardening Provider).
type StepUpSupport struct {
	Supported bool `json:"supported"`
}

// Access is GET /access extended: the existing assurance.Access body plus the fields the dialog needs.
// Embedding keeps ST4 from drifting away from what the approvals code already serves.
type Access struct {
	assurance.Access
	Methods []Method      `json:"methods"`
	Session Session       `json:"session"`
	Lockout Lockout       `json:"lockout"`
	StepUp  StepUpSupport `json:"step_up"`
}

// StepUp is the hint added to a 403 operator_access_required body so the dialog opens from any refused write.
type StepUp struct {
	Action  string `json:"action"`
	Risk    string `json:"risk"` // low | medium | high | destructive
	Summary string `json:"summary,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

// Refusal is the extended 403 body. {code,message} stay where the existing clients read them.
type Refusal struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	StepUp  StepUp   `json:"step_up"`
	Methods []Method `json:"methods"`
}

// Challenge is POST /access/challenge. Display tells the dialog what is being confirmed.
type Challenge struct {
	ChallengeID string   `json:"challenge_id"`
	Methods     []Method `json:"methods"`
	ExpiresAt   string   `json:"expires_at"`
	Display     StepUp   `json:"display"`
}

// Grant is the result of a verified challenge: action-bound or session-wide, short-lived, server-capped.
type Grant struct {
	Scope     string `json:"scope"` // session | action
	ExpiresAt string `json:"expires_at"`
	RememberS int    `json:"remember_s,omitempty"`
}

// Verified is POST /access/verify on success.
type Verified struct {
	OK        bool   `json:"ok"`
	Assurance string `json:"assurance"`
	Grant     Grant  `json:"grant"`
}

// Identity is the whoami. A profile name never verifies anyone: Assurance "verified" only means the operator
// token was presented on this session.
type Identity struct {
	State       string `json:"state"` // identified | unknown
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Source      string `json:"source"`    // local-profile | session
	Assurance   string `json:"assurance"` // local | verified
	VerifiedBy  string `json:"verified_by,omitempty"`
}

// Account is GET /account.
type Account struct {
	Identity         Identity `json:"identity"`
	Access           Access   `json:"access"`
	ProfileUpdatedAt string   `json:"profile_updated_at,omitempty"`
	CanSetProfile    bool     `json:"can_set_profile"`
}

// Profile is the PUT /account/profile body and the stored value (app_settings key account.profile).
type Profile struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
}

// FieldError is one validation problem, with a JSON pointer path as the admin contract uses.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidateProfile checks and trims a profile. It never contacts anything; email is syntax-checked only.
func ValidateProfile(p Profile) (Profile, []FieldError) {
	var errs []FieldError
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	p.Email = strings.TrimSpace(p.Email)
	if n := utf8.RuneCountInString(p.DisplayName); n < 1 || n > MaxDisplayName || strings.ContainsFunc(p.DisplayName, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		errs = append(errs, FieldError{"/display_name", "length", "Use 1 to 80 characters, without control characters."})
	}
	if p.Email != "" {
		a, err := mail.ParseAddress(p.Email)
		if len(p.Email) > MaxEmail || err != nil || a.Name != "" || a.Address != p.Email || !strings.Contains(a.Address[strings.LastIndex(a.Address, "@"):], ".") {
			errs = append(errs, FieldError{"/email", "format", "Enter an address like name@example.com."})
		}
	}
	return p, errs
}

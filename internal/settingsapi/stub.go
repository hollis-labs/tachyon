package settingsapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin/adminhttp"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

//go:embed openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <AdminPrefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// Stub serves the whole contract from memory: the admin manifest and groups through the libs adminhttp handler
// (so ETags, 409/412/422/428 and strict decoding are the library's own), plus the account and the extended
// access. Nothing is durable; a restart resets it.
//
// Controls (any route): X-Stub-Status forces an error (401, 403, 500, 503). On the access and account routes
// X-Stub-Scenario selects a state: expired, locked, elevated, no_step_up (access); unknown, identified,
// verified (account).
type Stub struct {
	Now func() time.Time

	store *store
	admin http.Handler

	mu         sync.Mutex
	profile    *Profile
	profileAt  time.Time
	expiresAt  time.Time
	wasElev    bool
	fails      int
	lockedTill time.Time
	challenges map[string]time.Time
	seq        int
}

func NewStub() *Stub {
	s := &Stub{Now: time.Now, store: newStore(), challenges: map[string]time.Time{}}
	h, err := adminhttp.NewHandler(adminhttp.Config{
		BasePath: Root,
		Authorize: func(r *http.Request, _ adminhttp.Resource) error {
			if r.Header.Get("Authorization") != "" {
				return &admin.Failure{Code: admin.Forbidden, Message: "Settings are available to the operator's browser session only."}
			}
			return nil
		},
		Resolve: func(*http.Request) (*admin.Registry, error) { return s.store.registry(s.elevated()) },
		ProtectCommand: func(r *http.Request) error {
			if !sameOrigin(r) {
				return &admin.Failure{Code: admin.Forbidden, Message: "Use the Tachyon browser origin."}
			}
			return nil
		},
		MaxBodyBytes: MaxBodyBytes,
	})
	if err != nil {
		panic("settingsapi: " + err.Error())
	}
	s.admin = h
	return s
}

// Routes lists every pattern the stub mounts, for the server's explicit one-by-one registration.
func Routes() []string {
	r := []string{"GET " + AdminPrefix + "/manifest", "GET " + AdminPrefix + "/openapi.yaml",
		"GET " + AccountPath, "PUT " + ProfilePath,
		"GET " + StubAccessPrefix, "POST " + StubAccessPrefix + "/challenge", "POST " + StubAccessPrefix + "/verify",
		"POST " + StubAccessPrefix + "/passkey/options", "POST " + StubAccessPrefix + "/reset"}
	for _, sp := range Specs() {
		for _, x := range AdminRoutes(sp.ID) {
			r = append(r, x.Method+" "+x.Path)
		}
	}
	return r
}

// Handler serves every route in Routes.
func (s *Stub) Handler() http.Handler {
	m := http.NewServeMux()
	for _, pat := range Routes() {
		var h http.HandlerFunc
		switch {
		case strings.HasSuffix(pat, "/openapi.yaml"):
			h = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/yaml")
				_, _ = w.Write(OpenAPI())
			}
		case strings.Contains(pat, AdminPrefix):
			h = s.adminRoute
		case pat == "GET "+AccountPath:
			h = s.account
		case pat == "PUT "+ProfilePath:
			h = s.putProfile
		case pat == "GET "+StubAccessPrefix:
			h = func(w http.ResponseWriter, r *http.Request) { write(w, 200, s.access(r)) }
		case strings.HasSuffix(pat, "/challenge"):
			h = s.challenge
		case strings.HasSuffix(pat, "/verify"):
			h = s.verify
		case strings.HasSuffix(pat, "/passkey/options"):
			h = func(w http.ResponseWriter, _ *http.Request) {
				fail(w, 400, CodeMethodUnavailable, "Passkeys are not available yet.")
			}
		default:
			h = s.reset
		}
		m.HandleFunc(pat, s.guard(strings.HasPrefix(pat, "GET "), h))
	}
	return m
}

func (s *Stub) guard(read bool, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Tachyon-Settings-Stub", "1")
		if code, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); code >= 400 {
			switch code {
			case 401:
				fail(w, 401, CodeSessionRequired, "Sign in to continue.")
			case 403:
				fail(w, 403, "forbidden", "Not allowed.")
			case 503:
				fail(w, 503, "backend_unavailable", "The settings backend is unavailable.")
			default:
				fail(w, 500, "internal", "Something went wrong.")
			}
			return
		}
		if r.Header.Get("Authorization") != "" {
			fail(w, 403, "forbidden", "Settings are available to the operator's browser session only.")
			return
		}
		if !read && !sameOrigin(r) {
			fail(w, 403, CodeOriginDenied, "Use the Tachyon browser origin.")
			return
		}
		f(w, r)
	}
}

func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail writes the flat {code,message} body the existing console clients read.
func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]string{"code": code, "message": msg})
}

func (s *Stub) elevated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Before(s.expiresAt)
}

func (s *Stub) now() time.Time { return s.Now() }

// adminRoute adds the step-up hint the library cannot: a write to an operator-token field without elevation is
// refused with 403 operator_access_required and what is being confirmed, instead of the library's generic 403.
func (s *Stub) adminRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && !s.elevated() && s.admin != nil {
		if g, op, ok := adminTarget(r.URL.Path); ok && (op == "update" || op == "reset") {
			body, _ := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
			r.Body = io.NopCloser(bytes.NewReader(body))
			var c struct {
				Set   map[string]json.RawMessage `json:"set"`
				Unset []string                   `json:"unset"`
				Keys  []string                   `json:"keys"`
			}
			if json.Unmarshal(body, &c) == nil {
				keys := append(append(c.Unset, c.Keys...), mapKeys(c.Set)...)
				if k := s.operatorKey(g, keys); k != "" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(403)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": Refusal{Code: CodeOperatorAccess,
						Message: "Operator access is needed for this action.",
						StepUp:  StepUp{Action: "settings.write", Risk: "medium", Summary: "Change " + g + "." + k, Ref: g + "." + k},
						Methods: methods(false)}})
					return
				}
			}
		}
	}
	s.admin.ServeHTTP(w, r)
}

func adminTarget(path string) (group, op string, ok bool) {
	rest, found := strings.CutPrefix(path, AdminPrefix+"/settings/")
	if !found {
		return "", "", false
	}
	g, o, _ := strings.Cut(rest, "/")
	return g, o, true
}

func mapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *Stub) operatorKey(group string, keys []string) string {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	g := s.store.groups[group]
	if g == nil {
		return ""
	}
	for _, k := range keys {
		if g.needsOperator(k) {
			return k
		}
	}
	return ""
}

// access builds the extended /access body for the current state or the X-Stub-Scenario override.
func (s *Stub) access(r *http.Request) Access {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	scenario := r.Header.Get("X-Stub-Scenario")
	exp := s.expiresAt
	locked := now.Before(s.lockedTill)
	lockRetry := int(s.lockedTill.Sub(now).Seconds()) + 1
	switch scenario {
	case "elevated":
		exp = now.Add(StubElevationS * time.Second)
	case "expired":
		exp, s.wasElev = time.Time{}, true
	case "locked":
		locked, lockRetry = true, LockoutWindowS
	}
	st := assurance.Status{Level: assurance.LevelBrowserSession, TTLPolicy: "30m", CanElevate: true}
	if now.Before(exp) {
		st.Level, st.ExpiresAt = assurance.LevelOperatorToken, exp
	}
	a := Access{Access: assurance.View(st, now), Methods: methods(false), Session: Session{State: "active"},
		StepUp: StepUpSupport{Supported: scenario != "no_step_up"}}
	if scenario == "expired" || (s.wasElev && !now.Before(exp)) {
		a.Session = Session{State: "expired", ExpiresAt: exp.UTC().Format(time.RFC3339)}
		if exp.IsZero() {
			a.Session.ExpiresAt = now.Add(-time.Minute).UTC().Format(time.RFC3339)
		}
	}
	if locked {
		a.Lockout = Lockout{Locked: true, RetryAfterS: lockRetry}
	}
	return a
}

func (s *Stub) account(w http.ResponseWriter, r *http.Request) {
	acc := s.access(r)
	s.mu.Lock()
	p, at := s.profile, s.profileAt
	s.mu.Unlock()
	switch r.Header.Get("X-Stub-Scenario") {
	case "unknown":
		p = nil
	case "identified", "verified":
		if p == nil {
			p, at = &Profile{DisplayName: "Sam Operator", Email: "sam@example.com"}, s.now().Add(-48*time.Hour)
		}
	}
	id := Identity{State: "unknown", Source: "session", Assurance: "local"}
	out := Account{Access: acc, CanSetProfile: true}
	if p != nil {
		id = Identity{State: "identified", DisplayName: p.DisplayName, Email: p.Email, Source: "local-profile", Assurance: "local"}
		out.ProfileUpdatedAt = at.UTC().Format(time.RFC3339)
	}
	if acc.State == "elevated" {
		id.Assurance, id.VerifiedBy = "verified", MethodOperatorToken
		if id.State == "unknown" {
			id.Source = "session"
		}
	}
	out.Identity = id
	write(w, 200, out)
}

func (s *Stub) putProfile(w http.ResponseWriter, r *http.Request) {
	var in Profile
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxProfileBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() {
		fail(w, 400, "malformed_input", "Send {display_name, email?} as JSON.")
		return
	}
	p, errs := ValidateProfile(in)
	if len(errs) > 0 {
		write(w, 422, map[string]any{"error": map[string]any{"code": CodeValidationFailed, "message": "The profile is invalid.", "errors": errs}})
		return
	}
	s.mu.Lock()
	s.profile, s.profileAt = &p, s.now()
	s.mu.Unlock()
	s.account(w, r)
}

func (s *Stub) challenge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action, Ref, Digest string
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxProfileBytes+1))
	if err := dec.Decode(&in); err != nil || in.Action == "" || len(in.Action) > 64 || len(in.Ref) > 120 {
		fail(w, 400, "malformed_input", "Send {action, ref?, digest?}.")
		return
	}
	if r.Header.Get("X-Stub-Scenario") == "no_step_up" {
		fail(w, 501, CodeUnsupportedProvider, "Step-up is not available with this sign-in.")
		return
	}
	s.mu.Lock()
	s.seq++
	id := "ch_" + strconv.Itoa(s.seq)
	exp := s.now().Add(ChallengeTTLS * time.Second)
	s.challenges[id] = exp
	s.mu.Unlock()
	write(w, 200, Challenge{ChallengeID: id, Methods: methods(false), ExpiresAt: exp.UTC().Format(time.RFC3339),
		Display: StepUp{Action: in.Action, Risk: assurance.Risk(assurance.Action(in.Action)), Ref: in.Ref}})
}

// verify scripts every dialog state: the credential "stub-operator-token" signs in; anything else fails, and the
// fifth failure locks for a minute. The challenge id "ch_expired" and any unknown id read as expired.
func (s *Stub) verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChallengeID string `json:"challenge_id"`
		Method      string `json:"method"`
		Credential  string `json:"credential"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxProfileBytes+1)).Decode(&in); err != nil || in.ChallengeID == "" {
		fail(w, 400, "malformed_input", "Send {challenge_id, method, credential}.")
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Before(s.lockedTill) {
		write(w, 429, map[string]any{"code": CodeLocked, "message": "Too many attempts. Try again shortly.", "retry_after_s": int(s.lockedTill.Sub(now).Seconds()) + 1})
		return
	}
	exp, ok := s.challenges[in.ChallengeID]
	if !ok || !now.Before(exp) {
		fail(w, 410, CodeChallengeExpired, "This confirmation expired. Start again.")
		return
	}
	if in.Method != MethodOperatorToken {
		fail(w, 400, CodeMethodUnavailable, "That sign-in method is not available yet.")
		return
	}
	if in.Credential != "stub-operator-token" {
		s.fails++
		if s.fails >= LockoutAfter {
			s.fails, s.lockedTill = 0, now.Add(LockoutWindowS*time.Second)
			write(w, 429, map[string]any{"code": CodeLocked, "message": "Too many attempts. Try again shortly.", "retry_after_s": LockoutWindowS})
			return
		}
		fail(w, 403, CodeInvalidCredentials, "That did not match.")
		return
	}
	delete(s.challenges, in.ChallengeID)
	s.fails, s.expiresAt, s.wasElev = 0, now.Add(StubElevationS*time.Second), true
	write(w, 200, Verified{OK: true, Assurance: assurance.LevelOperatorToken.String(),
		Grant: Grant{Scope: "session", ExpiresAt: s.expiresAt.UTC().Format(time.RFC3339)}})
}

// reset returns the stub's access state to signed-out and clears the profile, for GUI test runs.
func (s *Stub) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.profile, s.expiresAt, s.wasElev, s.fails, s.lockedTill, s.challenges = nil, time.Time{}, false, 0, time.Time{}, map[string]time.Time{}
	s.mu.Unlock()
	s.store = newStore()
	w.WriteHeader(204)
}

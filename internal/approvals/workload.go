package approvals

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

const AssuranceAudience = "tachyon-assurance"
const BasaliskStatusAudience = "basalisk-status"
const BasaliskStatusScope = "basalisk-status.assurance_read"
const ItemReadScope = AssuranceAudience + ".items_read"
const CompleteScope = AssuranceAudience + ".completions_deliver"
const OutcomeReadScope = AssuranceAudience + ".outcomes_read"

// CurrentWorkloadTrust must check principal/parent eligibility and exact scopes,
// plus issuer/token revocation, on every call. A cached denylist alone is NOT
// sufficient. The concrete narrow Tether endpoint is an activation dependency.
type CurrentWorkloadTrust interface {
	CheckCurrentWorkload(context.Context, auth.Principal) error
}

type AssuranceCaller struct {
	Subject      string   `json:"subject"`
	Kind         string   `json:"kind"`
	Applications []string `json:"applications"`
	Scopes       []string `json:"scopes"`
}
type WorkloadAuthConfig struct {
	Issuer  string            `json:"issuer"`
	JWKSURL string            `json:"jwks_url"`
	Callers []AssuranceCaller `json:"callers"`
}
type authenticatedCaller struct {
	AssuranceCaller
	ExpiresAt time.Time
}
type WorkloadAuthenticator struct {
	cfg     WorkloadAuthConfig
	pins    map[string]AssuranceCaller
	current CurrentWorkloadTrust
	client  *http.Client
	now     func() time.Time
}
type workloadClaims struct {
	Issuer   string `json:"iss"`
	Subject  string `json:"sub"`
	Audience string `json:"aud"`
	Scope    string `json:"scope"`
	Expiry   int64  `json:"exp"`
	Issued   int64  `json:"iat"`
	ID       string `json:"jti"`
	Kind     string `json:"kind"`
	Session  string `json:"sid,omitempty"`
	Parent   string `json:"par,omitempty"`
	Before   int64  `json:"nbf,omitempty"`
}

func (c workloadClaims) validAt(now time.Time) bool {
	return c.Issued > 0 && c.Issued <= now.Unix() && c.Expiry > c.Issued && c.Expiry-c.Issued <= 900 && now.Unix() < c.Expiry && c.Before >= 0 && c.Before <= now.Unix()
}
func validWorkloadToken(token string) bool {
	if len(token) == 0 || len(token) > 8192 || len(strings.Split(token, ".")) != 3 {
		return false
	}
	for _, c := range token {
		if c <= ' ' || c >= 127 {
			return false
		}
	}
	return true
}
func decodeWorkload(token string) (workloadClaims, error) {
	var c workloadClaims
	if !validWorkloadToken(token) {
		return c, auth.ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	var h struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		Key       string `json:"kid"`
	}
	hraw, e1 := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	craw, e2 := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if e1 != nil || e2 != nil || exactProfile(hraw, 1024, &h) != nil || strictProfileJSON(craw, 6144, &c) != nil || h.Algorithm != "EdDSA" || h.Type != "JWT" || !profileID(h.Key) {
		return c, auth.ErrInvalidToken
	}
	// Compare exact field spellings while retaining optional zero-valued claims.
	var fields map[string]json.RawMessage
	if json.Unmarshal(craw, &fields) != nil {
		return c, auth.ErrInvalidToken
	}
	for key := range fields {
		if !slices.Contains([]string{"iss", "sub", "aud", "scope", "exp", "iat", "jti", "kind", "sid", "par", "nbf"}, key) {
			return c, auth.ErrInvalidToken
		}
	}
	if !profileID(c.ID) || c.Parent != c.Subject || c.Session != "" || (c.Kind != auth.KindDevice && c.Kind != auth.KindService) {
		return c, auth.ErrInvalidToken
	}
	return c, nil
}
func trustedScope(scope string) bool {
	return scope == ItemReadScope || scope == CompleteScope || scope == OutcomeReadScope
}
func NewWorkloadAuthenticator(cfg WorkloadAuthConfig, current CurrentWorkloadTrust, client *http.Client) (*WorkloadAuthenticator, error) {
	u, err := url.Parse(cfg.JWKSURL)
	if !profileID(cfg.Issuer) || err != nil || !httpsOrigin(u.Scheme+"://"+u.Host) || u.User != nil || u.Path != "/.well-known/jwks.json" || u.RawQuery != "" || u.Fragment != "" || current == nil || len(cfg.Callers) == 0 {
		return nil, ErrCompletionUnavailable
	}
	pins := map[string]AssuranceCaller{}
	for _, p := range cfg.Callers {
		if !profileID(p.Subject) || (p.Kind != auth.KindService && p.Kind != auth.KindDevice) || len(p.Applications) == 0 || len(p.Scopes) == 0 {
			return nil, hitl.ErrInvalidRequest
		}
		if _, ok := pins[p.Subject]; ok {
			return nil, hitl.ErrInvalidRequest
		}
		for _, app := range p.Applications {
			if !profileID(app) || app == "cerberus" || strings.Contains(app, "*") {
				return nil, hitl.ErrInvalidRequest
			}
		}
		for _, scope := range p.Scopes {
			if !trustedScope(scope) {
				return nil, hitl.ErrInvalidRequest
			}
		}
		p.Applications = append([]string(nil), p.Applications...)
		p.Scopes = append([]string(nil), p.Scopes...)
		pins[p.Subject] = p
	}
	c := boundedClient(client)
	transport := c.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	c.Transport = freshKeysTransport{transport}
	return &WorkloadAuthenticator{cfg, pins, current, c, time.Now}, nil
}

type freshKeysTransport struct{ base http.RoundTripper }

func (t freshKeysTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Cache-Control", "no-cache, no-store")
	r.Header.Set("Pragma", "no-cache")
	return t.base.RoundTrip(r)
}
func (a *WorkloadAuthenticator) authenticate(ctx context.Context, r *http.Request, scope string) (authenticatedCaller, error) {
	if a == nil || a.current == nil {
		return authenticatedCaller{}, ErrCompletionUnavailable
	}
	if len(r.Header.Values("Authorization")) != 1 || r.Header.Get("Cookie") != "" {
		return authenticatedCaller{}, auth.ErrInvalidToken
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return authenticatedCaller{}, auth.ErrInvalidToken
	}
	token := strings.TrimPrefix(header, "Bearer ")
	claims, err := decodeWorkload(token)
	if err != nil {
		return authenticatedCaller{}, err
	}
	pin, ok := a.pins[claims.Subject]
	if !ok || claims.Issuer != a.cfg.Issuer || claims.Audience != AssuranceAudience || pin.Kind != claims.Kind || !claims.validAt(a.now()) || !slices.Contains(pin.Scopes, scope) {
		return authenticatedCaller{}, auth.ErrInvalidToken
	}
	scopes := strings.Fields(claims.Scope)
	if len(scopes) == 0 || strings.Join(scopes, " ") != claims.Scope || !slices.Contains(scopes, scope) {
		return authenticatedCaller{}, auth.ErrForbidden
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		if seen[s] || !slices.Contains(pin.Scopes, s) || !trustedScope(s) {
			return authenticatedCaller{}, auth.ErrForbidden
		}
		seen[s] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	verifier, err := auth.NewJWTVerifier(auth.JWTConfig{Issuer: a.cfg.Issuer, Audience: AssuranceAudience, JWKSURL: a.cfg.JWKSURL, Client: a.client, Now: a.now})
	if err != nil || verifier.Refresh(ctx) != nil {
		return authenticatedCaller{}, ErrCompletionUnavailable
	}
	principal, err := verifier.Verify(ctx, token)
	if err != nil || !principal.Verified {
		return authenticatedCaller{}, auth.ErrInvalidToken
	}
	if err = a.current.CheckCurrentWorkload(ctx, principal); err != nil {
		return authenticatedCaller{}, ErrCompletionUnavailable
	}
	if ctx.Err() != nil || !claims.validAt(a.now()) {
		return authenticatedCaller{}, auth.ErrInvalidToken
	}
	return authenticatedCaller{pin, time.Unix(claims.Expiry, 0)}, nil
}

// WorkloadExchange re-reads a private enrolled parent through the shared secret
// watcher and requests one short JWT per status read. No credential is persisted
// in Tachyon's application database or returned to the browser.
type WorkloadExchangeConfig struct {
	Origin          string `json:"origin"`
	Issuer          string `json:"issuer"`
	ParentTokenFile string `json:"parent_token_file"`
	Subject         string `json:"subject"`
	Kind            string `json:"kind"`
}
type WorkloadExchange struct {
	cfg    WorkloadExchangeConfig
	watch  *tokenfile.Watcher
	client *http.Client
	now    func() time.Time
}

func validParentToken(value string) bool {
	return strings.HasPrefix(value, "tth_") && len(value) <= 256 && strings.IndexFunc(value, func(c rune) bool { return c <= ' ' || c >= 127 }) < 0
}

func NewWorkloadExchange(cfg WorkloadExchangeConfig, client *http.Client) (*WorkloadExchange, error) {
	if !httpsOrigin(cfg.Origin) || !profileID(cfg.Issuer) || !profileID(cfg.Subject) || cfg.ParentTokenFile == "" || (cfg.Kind != auth.KindDevice && cfg.Kind != auth.KindService) {
		return nil, hitl.ErrInvalidRequest
	}
	watch := tokenfile.NewWatcher(cfg.ParentTokenFile, tokenfile.Options{Validate: validParentToken}, nil)
	return &WorkloadExchange{cfg, watch, boundedClient(client), time.Now}, nil
}
func (e *WorkloadExchange) Token(ctx context.Context) (string, error) {
	parent, err := e.watch.Token()
	if err != nil {
		return "", ErrCompletionUnavailable
	}
	body, _ := json.Marshal(map[string]any{"audience": BasaliskStatusAudience, "scopes": []string{BasaliskStatusScope}, "ttl_seconds": 300})
	r, err := http.NewRequestWithContext(ctx, "POST", e.cfg.Origin+"/auth/token", bytes.NewReader(body))
	if err != nil {
		return "", ErrCompletionUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+parent)
	r.Header.Set("Tether-Protocol", "1")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cache-Control", "no-store")
	resp, err := e.client.Do(r)
	if err != nil {
		return "", ErrCompletionUnavailable
	}
	defer resp.Body.Close()
	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
		TTL       int       `json:"ttl_seconds"`
		Audience  string    `json:"aud"`
		Scopes    []string  `json:"scopes"`
		Kind      string    `json:"kind"`
		KID       string    `json:"kid"`
		JTI       string    `json:"jti"`
	}
	if readBoundedJSON(resp, 16<<10, &result) != nil {
		return "", ErrCompletionUnavailable
	}
	c, err := decodeWorkload(result.Token)
	if err != nil || c.Issuer != e.cfg.Issuer || c.Subject != e.cfg.Subject || c.Kind != e.cfg.Kind || c.Audience != BasaliskStatusAudience || c.Scope != BasaliskStatusScope || !c.validAt(e.now()) || !result.ExpiresAt.Equal(time.Unix(c.Expiry, 0)) || result.Audience != c.Audience || result.Kind != c.Kind || result.JTI != c.ID || !slices.Equal(result.Scopes, []string{BasaliskStatusScope}) {
		return "", ErrCompletionUnavailable
	}
	return result.Token, nil
}

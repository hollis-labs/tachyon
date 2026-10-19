package approvals

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/security/auth"
	proof "github.com/hollis-labs/libs/security/webauthn"
	"github.com/hollis-labs/libs/security/webauthn/presencetest"
)

type currentWorkloadFunc func(context.Context, auth.Principal) error

func (f currentWorkloadFunc) CheckCurrentWorkload(ctx context.Context, p auth.Principal) error {
	return f(ctx, p)
}
func signWorkload(t *testing.T, key ed25519.PrivateKey, claims workloadClaims) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "test-key"})
	c, _ := json.Marshal(claims)
	body := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	return body + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(body)))
}

type assuranceNetwork struct {
	authenticator      *presencetest.Authenticator
	party              *proof.Party
	server             *httptest.Server
	origin             string
	client             *http.Client
	api                *TrustedAPI
	store              *Store
	key                ed25519.PrivateKey
	cfg                AssuranceConfig
	status             basaliskStatus
	claims             workloadClaims
	req                hitl.EnqueueRequest
	now                time.Time
	token              string
	checks             atomic.Int32
	statusReads        atomic.Int32
	revoked            atomic.Bool
	statusUnavailable  atomic.Bool
	keysUnavailable    atomic.Bool
	removedKey         atomic.Bool
	redirect           atomic.Bool
	staleEcho          atomic.Bool
	trustOutage        atomic.Bool
	trustStale         atomic.Bool
	trustWrongIdentity atomic.Bool
	mutateStatus       func(*basaliskStatus)
}

func newAssuranceNetwork(t *testing.T) *assuranceNetwork {
	t.Helper()
	f := &assuranceNetwork{now: time.Now().UTC().Truncate(time.Second), store: nativeStore(t)}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = key
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/.well-known/jwks.json":
			if r.Header.Get("Cache-Control") != "no-cache, no-store" {
				t.Error("cached JWKS permitted")
			}
			if f.keysUnavailable.Load() {
				w.WriteHeader(503)
				return
			}
			if f.removedKey.Load() {
				writeValue(w, map[string]any{"keys": []any{}})
				return
			}
			writeValue(w, map[string]any{"keys": []map[string]string{{"kid": "test-key", "kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
		case "/auth/workload-trust":
			f.checks.Add(1)
			if r.Header.Get("Authorization") != "Bearer tth_synthetic_trust_reader" || r.Header.Get("Tether-Protocol") != "1" {
				t.Error("wrong trust-reader credential")
			}
			var cmd struct {
				QueryID  string          `json:"query_id"`
				Workload currentWorkload `json:"workload"`
			}
			_ = json.NewDecoder(r.Body).Decode(&cmd)
			if !canonicalBase64(cmd.QueryID, 32) || cmd.Workload.Subject != "basalisk-reader" || cmd.Workload.Parent != cmd.Workload.Subject || cmd.Workload.Audience != AssuranceAudience {
				t.Error("wrong current workload query")
			}
			if f.trustOutage.Load() {
				w.WriteHeader(503)
				return
			}
			if f.trustStale.Load() {
				cmd.QueryID = "stale"
			}
			if f.trustWrongIdentity.Load() {
				cmd.Workload.TokenID = "different-token"
			}
			writeValue(w, map[string]any{"query_id": cmd.QueryID, "workload": cmd.Workload, "active": !f.revoked.Load(), "checked_at": time.Now().UTC()})
		case "/auth/token":
			if r.Header.Get("Authorization") != "Bearer tth_synthetic_parent" || r.Header.Get("Tether-Protocol") != "1" {
				t.Error("incorrect private parent transport")
			}
			var cmd struct {
				Audience string   `json:"audience"`
				Scopes   []string `json:"scopes"`
				TTL      int      `json:"ttl_seconds"`
			}
			_ = json.NewDecoder(r.Body).Decode(&cmd)
			if cmd.Audience != BasaliskStatusAudience || len(cmd.Scopes) != 1 || cmd.Scopes[0] != BasaliskStatusScope || cmd.TTL != 300 {
				t.Error("scope widening")
			}
			c := f.claims
			c.Subject = "tachyon-status"
			c.Parent = c.Subject
			c.Audience = BasaliskStatusAudience
			c.Scope = BasaliskStatusScope
			writeValue(w, map[string]any{"token": signWorkload(t, key, c), "expires_at": time.Unix(c.Expiry, 0), "ttl_seconds": 300, "aud": c.Audience, "scopes": []string{c.Scope}, "kind": c.Kind, "kid": "test-key", "jti": c.ID})
		case "/assurance/v1/status":
			f.statusReads.Add(1)
			if f.redirect.Load() {
				http.Redirect(w, r, "/unexpected", 302)
				return
			}
			if f.statusUnavailable.Load() {
				w.WriteHeader(503)
				return
			}
			// Actual signature verification of the separately exchanged status JWT.
			v, e := auth.NewJWTVerifier(auth.JWTConfig{Issuer: "test-tether", Audience: BasaliskStatusAudience, JWKSURL: f.origin + "/.well-known/jwks.json", Client: &http.Client{Transport: freshKeysTransport{f.client.Transport}}})
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			p, e := v.Verify(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if e != nil || !p.Verified || p.ID != "tachyon-status" || !p.HasScope(BasaliskStatusScope) {
				t.Errorf("bad exchanged JWT: %v", e)
				w.WriteHeader(401)
				return
			}
			var cmd struct {
				ID    string `json:"completion_id"`
				Query string `json:"query_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&cmd)
			if cmd.ID != f.status.Completion.ID || cmd.Query == "" {
				t.Error("bad completion lookup")
			}
			raw, _ := json.Marshal(f.status)
			var out basaliskStatus
			_ = json.Unmarshal(raw, &out)
			out.QueryID = cmd.Query
			if f.staleEcho.Load() {
				out.QueryID = "old-query"
			}
			if f.mutateStatus != nil {
				f.mutateStatus(&out)
			}
			writeValue(w, out)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	f.origin = "https://example.com"
	f.client = f.server.Client()
	transport := f.client.Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.server.Listener.Addr().String())
	}
	f.client.Transport = transport
	u, _ := url.Parse(f.origin)
	f.cfg = AssuranceConfig{Authentication: WorkloadAuthConfig{Issuer: "test-tether", JWKSURL: f.origin + "/.well-known/jwks.json", Callers: []AssuranceCaller{{Subject: "basalisk-reader", Kind: "device", Applications: []string{"test-producer"}, Scopes: []string{ItemReadScope, CompleteScope, OutcomeReadScope}}}}, Basalisk: BasaliskConfig{Authority: "bas_test", Origin: f.origin, RPID: u.Hostname()}, Audiences: map[string]string{"test-producer": "tether"}}
	path := filepath.Join(t.TempDir(), "parent.token")
	if err = os.WriteFile(path, []byte("tth_synthetic_parent"), 0600); err != nil {
		t.Fatal(err)
	}
	readerPath := filepath.Join(t.TempDir(), "trust-reader.token")
	if err = os.WriteFile(readerPath, []byte("tth_synthetic_trust_reader"), 0600); err != nil {
		t.Fatal(err)
	}
	f.cfg.CurrentTrust = WorkloadTrustConfig{Origin: f.origin, Issuer: "test-tether", ReaderTokenFile: readerPath}
	f.cfg.StatusExchange = WorkloadExchangeConfig{Origin: f.origin, Issuer: "test-tether", ParentTokenFile: path, Subject: "tachyon-status", Kind: "device"}
	f.claims = workloadClaims{Issuer: "test-tether", Subject: "basalisk-reader", Audience: AssuranceAudience, Scope: strings.Join([]string{ItemReadScope, CompleteScope, OutcomeReadScope}, " "), Issued: f.now.Unix(), Expiry: f.now.Add(5 * time.Minute).Unix(), ID: "test-token", Kind: "device", Parent: "basalisk-reader"}
	f.token = signWorkload(t, key, f.claims)
	fixture := loadCompletionFixture(t)
	var op operationBinding
	_ = json.Unmarshal(fixture.Case.Operation, &op)
	op.Audience = "tether"
	op.ExpiresAt = strconv.FormatInt(f.now.Add(10*time.Minute).Unix(), 10)
	var reg credentialRegistration
	_ = json.Unmarshal(op.AuthorizationDetails[0], &reg)
	reg.CredentialExpiresAt = strconv.FormatInt(f.now.Add(time.Hour).Unix(), 10)
	op.AuthorizationDetails[0], _ = json.Marshal(reg)
	operation, _ := json.Marshal(op)
	_, digest, err := parseAssuranceOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	party, err := proof.NewParty(u.Hostname(), "Test", []string{f.origin})
	if err != nil {
		t.Fatal(err)
	}
	user := proof.User{ID: []byte("usr_test"), Name: "Test", DisplayName: "Test"}
	authenticator := presencetest.New(t, u.Hostname(), f.origin)
	f.authenticator = authenticator
	f.party = party
	options, session, err := party.BeginRegistration(user, f.origin)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := party.FinishRegistration(user, *session, authenticator.Register(t, options))
	if err != nil {
		t.Fatal(err)
	}
	user.Credentials = append(user.Credentials, *credential)
	binding := proofBinding{Profile: evidenceProfile, Authority: "bas_test", HumanSubject: "usr_test", KeyRef: credentialKeyRef(credential.ID), Audience: "tether", OpDigest: digest, OperationID: op.OperationID, OperationRevision: op.Revision, ItemID: "item-example-1", Decision: "approved", ProofID: "proof-test", Nonce: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), IssuedAt: strconv.FormatInt(f.now.Unix(), 10), ExpiresAt: strconv.FormatInt(f.now.Add(4*time.Minute).Unix(), 10)}
	challenge, _ := bindingChallenge(binding)
	options, err = party.BeginAssertion(user, challenge, f.origin)
	if err != nil {
		t.Fatal(err)
	}
	raw := authenticator.Assert(t, options)
	var assertion struct {
		RawID    string `json:"rawId"`
		Response struct {
			Client string `json:"clientDataJSON"`
			Auth   string `json:"authenticatorData"`
			Sig    string `json:"signature"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &assertion)
	ev := assuranceEvidence{Binding: binding}
	ev.Assertion.CredentialID = assertion.RawID
	ev.Assertion.ClientDataJSON = assertion.Response.Client
	ev.Assertion.AuthenticatorData = assertion.Response.Auth
	ev.Assertion.Signature = assertion.Response.Sig
	f.status.Authority = "bas_test"
	f.status.RPID = u.Hostname()
	f.status.Origin = f.origin
	f.status.CheckedAt = f.now
	f.status.Credential = user
	f.status.Completion.ID = "completion-example"
	f.status.Completion.ItemID = "item-example-1"
	f.status.Completion.ExpectedRevision = 1
	f.status.Completion.AuthTime = f.now
	f.status.Completion.Evidence, _ = json.Marshal(ev)
	req := requestFixture(t, "network", "high")
	req.Extra["operation"] = operation
	req.Extra["op_digest"], _ = json.Marshal(digest)
	expiry := f.now.Add(10 * time.Minute)
	req.ExpiresAt = &expiry
	f.req = req
	service := hitl.NewService(f.store, hitl.Options{NewID: func() string { return "item-example-1" }})
	if _, err = service.Enqueue(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	f.api, err = NewTrustedAPI(f.store, &f.cfg, nil, f.client)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *assuranceNetwork) call(operation string, body any, token string) *httptest.ResponseRecorder {
	return serveJSON(f.api, "POST", TrustedPrefix+"/"+operation, body, token, "")
}
func deliveryBody() map[string]any {
	return map[string]any{"item_id": "item-example-1", "completion_id": "completion-example", "expected_revision": 1}
}
func lookupBody() map[string]any {
	return map[string]any{"item_id": "item-example-1", "query_id": "fresh-query"}
}

func TestAssuranceNetworkRealCryptoAndRecovery(t *testing.T) {
	f := newAssuranceNetwork(t)
	w := f.call("item", lookupBody(), f.token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatalf("lookup %d %s", w.Code, w.Body)
	}
	w = f.call("complete", deliveryBody(), f.token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"assurance_evidence"`) {
		t.Fatalf("complete %d %s", w.Code, w.Body)
	}
	first := w.Body.String()
	w = f.call("complete", deliveryBody(), f.token)
	if w.Code != 200 || w.Body.String() != first {
		t.Fatalf("retry %d %s", w.Code, w.Body)
	}
	f.statusUnavailable.Store(true)
	w = f.call("complete", deliveryBody(), f.token)
	if w.Code != 503 {
		t.Fatalf("cached trust accepted: %d", w.Code)
	}
	// Recovery does not consult expired/revoked proof status or spend a ceremony.
	before := f.statusReads.Load()
	w = f.call("outcome", lookupBody(), f.token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"decision":"approved"`) {
		t.Fatalf("recovery %d %s", w.Code, w.Body)
	}
	if f.statusReads.Load() != before {
		t.Fatal("recovery called proof status")
	}
	f.revoked.Store(true)
	w = f.call("outcome", lookupBody(), f.token)
	if w.Code != 503 {
		t.Fatal("recovery bypassed workload revocation")
	}
}

func TestAssuranceNetworkRejectsUntrustedEvidence(t *testing.T) {
	for _, name := range []string{"signature", "operation", "key", "human", "origin", "item", "revision", "authority", "expired", "stale_echo", "redirect", "outage", "duplicate_client_data", "no_uv", "no_up", "wrong_rp", "signed_wrong_origin"} {
		t.Run(name, func(t *testing.T) {
			f := newAssuranceNetwork(t)
			switch name {
			case "no_uv", "no_up", "wrong_rp", "signed_wrong_origin":
				f.resign(t, name)
			case "stale_echo":
				f.staleEcho.Store(true)
			case "redirect":
				f.redirect.Store(true)
			case "outage":
				f.statusUnavailable.Store(true)
			default:
				f.mutateStatus = func(status *basaliskStatus) {
					var ev assuranceEvidence
					_ = json.Unmarshal(status.Completion.Evidence, &ev)
					switch name {
					case "signature":
						ev.Assertion.Signature = "YWJj"
					case "operation":
						ev.Binding.OpDigest = "sha256:" + strings.Repeat("0", 64)
					case "key":
						status.Credential.Credentials[0].PublicKey = []byte("forged")
					case "human":
						status.Credential.ID = []byte("usr_other")
					case "origin":
						status.Origin = "https://other.example"
					case "item":
						status.Completion.ItemID = "other"
					case "revision":
						status.Completion.ExpectedRevision = 2
					case "authority":
						status.Authority = "other"
					case "expired":
						ev.Binding.ExpiresAt = strconv.FormatInt(f.now.Unix(), 10)
					case "duplicate_client_data":
						raw, _ := base64.RawURLEncoding.DecodeString(ev.Assertion.ClientDataJSON)
						raw = []byte(strings.Replace(string(raw), `"type":`, `"type":"webauthn.get","type":`, 1))
						ev.Assertion.ClientDataJSON = base64.RawURLEncoding.EncodeToString(raw)
					}
					status.Completion.Evidence, _ = json.Marshal(ev)
				}
			}
			w := f.call("complete", deliveryBody(), f.token)
			if w.Code == 200 {
				t.Fatalf("accepted %s", name)
			}
			assertPendingWithoutReceipt(t, f.store, "item-example-1")
		})
	}
}

func TestAssuranceNetworkJWTBoundaries(t *testing.T) {
	f := newAssuranceNetwork(t)
	for _, field := range []string{"issuer", "audience", "scope", "subject", "parent", "kind", "session", "expiry", "issued", "lifetime", "jti", "signature"} {
		t.Run(field, func(t *testing.T) {
			c := f.claims
			switch field {
			case "issuer":
				c.Issuer = "other"
			case "audience":
				c.Audience = "tachyon-enrollment"
			case "scope":
				c.Scope = "tachyon-assurance.*"
			case "subject":
				c.Subject = "other"
			case "parent":
				c.Parent = "operator"
			case "kind":
				c.Kind = "operator"
			case "session":
				c.Session = "session"
			case "expiry":
				c.Expiry = f.now.Unix()
			case "issued":
				c.Issued = f.now.Add(time.Minute).Unix()
			case "lifetime":
				c.Expiry = c.Issued + 901
			case "jti":
				c.ID = ""
			}
			token := signWorkload(t, f.key, c)
			if field == "signature" {
				_, key, _ := ed25519.GenerateKey(rand.Reader)
				token = signWorkload(t, key, c)
			}
			if w := f.call("item", lookupBody(), token); w.Code == 200 {
				t.Fatal("accepted wrong JWT")
			}
		})
	}
	for _, flag := range []*atomic.Bool{&f.revoked, &f.keysUnavailable, &f.removedKey, &f.trustOutage, &f.trustStale, &f.trustWrongIdentity} {
		flag.Store(true)
		if w := f.call("item", lookupBody(), f.token); w.Code == 200 {
			t.Fatal("cached auth accepted")
		}
		flag.Store(false)
	}
	for _, field := range []string{"participant", "operation", "assurance_evidence"} {
		cmd := deliveryBody()
		cmd[field] = map[string]string{"authority": "fake"}
		if w := f.call("complete", cmd, f.token); w.Code != 400 {
			t.Fatalf("accepted injected %s: %d", field, w.Code)
		}
	}
	w := serveJSON(f.api, "POST", TrustedPrefix+"/item", lookupBody(), f.token, "cookie")
	if w.Code != 401 {
		t.Fatal("cookie accepted")
	}
	// A configured caller may not read another producer's item.
	f.api.authentication.pins[f.claims.Subject] = AssuranceCaller{Subject: f.claims.Subject, Kind: "device", Applications: []string{"other"}, Scopes: []string{ItemReadScope, CompleteScope, OutcomeReadScope}}
	if w = f.call("item", lookupBody(), f.token); w.Code != 404 {
		t.Fatalf("cross-producer disclosure: %d", w.Code)
	}
	disabled := &TrustedAPI{}
	if w = serveJSON(disabled, "POST", TrustedPrefix+"/item", lookupBody(), f.token, ""); w.Code != 503 {
		t.Fatal("disabled integration accepted")
	}
	f.cfg.CurrentTrust = WorkloadTrustConfig{}
	if _, err := NewTrustedAPI(f.store, &f.cfg, nil, f.client); err == nil {
		t.Fatal("missing current lifecycle dependency accepted")
	}
}

func (f *assuranceNetwork) resign(t *testing.T, mode string) {
	t.Helper()
	switch mode {
	case "no_uv":
		f.authenticator.UV = false
	case "no_up":
		f.authenticator.Present = false
	case "wrong_rp":
		f.authenticator.RPID = "other.example"
	case "signed_wrong_origin":
		f.authenticator.Origin = "https://other.example"
	}
	var ev assuranceEvidence
	_ = json.Unmarshal(f.status.Completion.Evidence, &ev)
	challenge, _ := bindingChallenge(ev.Binding)
	options, err := f.party.BeginAssertion(f.status.Credential, challenge, f.origin)
	if err != nil {
		t.Fatal(err)
	}
	raw := f.authenticator.Assert(t, options)
	var assertion struct {
		Response struct {
			Client    string `json:"clientDataJSON"`
			Auth      string `json:"authenticatorData"`
			Signature string `json:"signature"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &assertion)
	ev.Assertion.ClientDataJSON = assertion.Response.Client
	ev.Assertion.AuthenticatorData = assertion.Response.Auth
	ev.Assertion.Signature = assertion.Response.Signature
	f.status.Completion.Evidence, _ = json.Marshal(ev)
}

func TestAssuranceOperationRejectsAmbiguity(t *testing.T) {
	f := newAssuranceNetwork(t)
	valid := string(f.req.Extra["operation"])
	for _, bad := range []string{
		strings.Replace(valid, `"profile":`, `"Profile":`, 1),
		strings.Replace(valid, `"revision":"1"`, `"revision":1`, 1),
		strings.Replace(valid, `"revision":"1"`, `"revision":"01"`, 1),
		strings.Replace(valid, `"purpose":"approval producer"`, `"purpose":"\ud800"`, 1),
		strings.Replace(valid, `"purpose":"approval producer"`, `"purpose":null`, 1),
		strings.Replace(valid, `"action":"create"`, `"action":"rotate"`, 1),
		strings.Replace(valid, `"scopes":["inbox.approvals_enqueue","inbox.approvals_read"]`, `"scopes":["z","a"]`, 1),
	} {
		if _, _, err := parseAssuranceOperation([]byte(bad)); err == nil {
			t.Fatalf("ambiguous operation accepted: %s", bad)
		}
	}
	if _, _, err := parseAssuranceOperation([]byte(strings.Replace(valid, "approval producer", "<é> 😀 \\u2028", 1))); err != nil {
		t.Fatal("valid Unicode rejected", err)
	}
}

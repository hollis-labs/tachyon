package dataverbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
	"github.com/hollis-labs/tachyon/next/internal/webui"
)

const nativePassword = "correct horse battery staple"

var nativeGrants = []string{"gate.enqueue", "gate.get", "gate.await", "gate.withdraw", "gate.assess-response"}

type nativeFixture struct {
	t                  *testing.T
	base               string
	producer           string
	owner              *http.Client
	anonymous          *http.Client
	inbox              *approvals.Inbox
	acc                *access.Service
	ids                *identity.Service
	credential         access.Credential
	callbackCalls      atomic.Int32
	callbackMode       atomic.Int32
	callbackIDs        *identity.Service
	callbackKeyID      string
	callbackCredential atomic.Value
	callbackIssuer     string
	stopOwner          func() error
}

// nativeStack uses real identity, scoped route middleware, HTTP, domain store
// and callback credential verification on isolated databases. No injected fake
// principal or seeded terminal record participates in these tests. Excluding
// scheduler lets this owner/auth/HTTP suite run on SQLite and Postgres alike.
func nativeStack(t *testing.T) *nativeFixture {
	t.Helper()
	t.Setenv("SUBSTRATE_CONFIG", "")

	ctx := context.Background()
	bootApp := func(name string) *boot.Running {
		cfg, _ := json.Marshal(map[string]any{"logging": map[string]any{"level": "error"}, "database": storetest.New(t).ModuleConfig(), "auth": map[string]any{"mode": "enforce"}, "identity": map[string]any{"key_file": filepath.Join(t.TempDir(), "signing.key"), "setup": "offline"}})
		run, err := boot.StartWith(ctx, name, []string{"config", "logging", "health", "database", "auth", "identity", "httphost"}, string(cfg), boot.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = run.Stop(context.Background()) })
		if _, err = identity.From(run.App).SetupOwner(ctx, identity.OwnerInput{Username: "owner", DisplayName: "Owner", Password: nativePassword}); err != nil {
			t.Fatal(err)
		}
		return run
	}
	run := bootApp("tachyon")
	host := bootApp("gatecrasher")
	ids := identity.From(run.App)
	db := database.From(run.App)
	acc := access.New(db, ids, nil)
	token, cred, err := acc.Issue(ctx, access.IssueInput{Name: "gatecrasher", Grants: access.Grants{Verbs: nativeGrants}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	cbids := identity.From(host.App)
	cbtoken, cbcred, err := cbids.CreateKey(ctx, identity.KeyInput{Name: "tachyon-workload", Scopes: []string{"gatecrasher.gate.authorize-response"}, Audiences: []string{"gatecrasher"}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f := &nativeFixture{t: t, producer: token, acc: acc, ids: ids, credential: cred, callbackIDs: cbids, callbackKeyID: cbcred.ID, anonymous: &http.Client{Timeout: 10 * time.Second}}
	f.callbackCredential.Store(cbtoken)
	issuerHandler, err := httphost.Handler(host.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	issuer := httptest.NewServer(cbids.Middleware(issuerHandler))
	t.Cleanup(issuer.Close)
	f.callbackIssuer = issuer.URL
	f.inbox = approvals.NewInbox(&approvals.Store{DB: store.Wrap(db)}, nil)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, verifyErr := cbids.Verify(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if verifyErr != nil || !p.Verified || p.Kind != "service" || p.TokenID != cbcred.ID || p.ID != "key:"+cbcred.ID || p.Audience != "gatecrasher" || !p.HasScope("gatecrasher.gate.authorize-response") || r.Method != "POST" || r.URL.Path != "/api/gate/v1/authorize-response" {
			w.WriteHeader(403)
			return
		}
		var q approvals.ProposedResponse
		if json.NewDecoder(r.Body).Decode(&q) != nil || q.ContractVersion != gate.ContractVersion || q.OwnerRef != "tachyon-test-owner" || q.ExpectedRevision != q.PresentedRevision || q.ItemID == "" || q.OptionID == "" || q.AssessedAt.IsZero() || !q.ExpiresAt.After(q.AssessedAt) || q.ExpiresAt.Sub(q.AssessedAt) > approvals.NativeAssessmentTTL || q.Binding.Validate() != nil || q.NativeRequestID.Validate() != nil || q.Participant.Responder == nil {
			w.WriteHeader(403)
			return
		}
		f.callbackCalls.Add(1)
		switch f.callbackMode.Load() {
		case 1:
			w.WriteHeader(503)
			return
		case 2:
			q.OptionID = "changed"
		case 3:
			q.ExpiresAt = q.AssessedAt
		case 4:
			q.ContractVersion = hitl.ContractVersion
		}
		_ = json.NewEncoder(w).Encode(approvals.ResponseAuthorization{ProposedResponse: q, Authorized: true})
	}))
	t.Cleanup(callback.Close)
	f.inbox.Native = &approvals.NativeGate{OwnerRef: "tachyon-test-owner", ProducerIdentity: cred.IdentityID, CallerScope: "gatecrasher-test", CurrentUser: ids.User, ProducerActive: func(ctx context.Context) error { return acc.ActiveGateProducer(ctx, cred.IdentityID) }, Authorize: approvals.NativeAuthorizer(callback.URL+"/api/gate/v1/authorize-response", true, func(context.Context) (string, error) { return f.callbackCredential.Load().(string), nil })}
	agent := NewAgentHTTP(NewLocal(objects.New(store.Wrap(db))), acc, Chat{}, f.inbox)
	rs := []routes.Route{}
	for _, method := range []string{"GET", "POST"} {
		rs = append(rs, routes.Route{Method: method, Pattern: AgentPrefix + "/providers/{provider}/verbs/{verb}", Scope: routes.Authenticated, Handler: agent})
	}
	rs = append(rs, routes.Route{Method: "GET", Pattern: AgentPrefix + "/providers", Scope: routes.Authenticated, Handler: agent})
	for _, r := range []struct{ method, path, scope string }{{"GET", "/inbox", "read"}, {"GET", "/history", "read"}, {"GET", "/items/{id}", "read"}, {"POST", "/items/{id}/present", "resolve"}, {"POST", "/items/{id}/resolve", "resolve"}, {"POST", "/items/{id}/defer", "resolve"}, {"POST", "/batch/resolve", "resolve"}} {
		rs = append(rs, routes.Route{Method: r.method, Pattern: approvals.Prefix + r.path, Scope: "tachyon.approvals." + r.scope, Handler: f.inbox})
	}
	mux, err := routes.Register(auth.ScopeTable(run.App), rs)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := webui.Assets()
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httphost.Handler(run.App, httphost.Options{Routes: mux, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(ids.Middleware(handler))
	t.Cleanup(server.Close)
	f.base = server.URL
	f.stopOwner = func() error { server.Close(); return run.Stop(context.Background()) }
	jar, _ := cookiejar.New(nil)
	f.owner = &http.Client{Jar: jar, Timeout: 10 * time.Second}
	f.want(f.owner, 200, "POST", "/api/login", map[string]string{"username": "owner", "password": nativePassword}, "")
	return f
}

func (f *nativeFixture) do(client *http.Client, method, path string, input any, token string) (int, []byte) {
	f.t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			f.t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.base+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return res.StatusCode, out
}
func (f *nativeFixture) want(c *http.Client, status int, method, path string, input any, token string) []byte {
	f.t.Helper()
	got, b := f.do(c, method, path, input, token)
	if got != status {
		f.t.Fatalf("%s %s: %d want %d: %s", method, strings.Split(path, "?")[0], got, status, b)
	}
	return b
}
func gatePath(verb string, input any) string {
	p := AgentPrefix + "/providers/tachyon/verbs/" + verb
	if input != nil {
		raw, _ := json.Marshal(input)
		p += "?input=" + url.QueryEscape(string(raw))
	}
	return p
}
func nativeRequest(key string) (gate.Request, hitl.EnqueueRequest) {
	n := gatetest.Request(time.Now().UTC())
	n.IdempotencyKey = key
	n.Options = append(n.Options, gate.NativeOption{ID: "native-cancel", Kind: gate.Cancel, Scope: n.Options[0].Scope})
	raw, _ := json.Marshal(n)
	quoted, _ := json.Marshal(string(raw))
	return n, hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: approvals.NativeKind, IdempotencyKey: key, Source: hitl.SourceAssertion{ApplicationID: "gatecrasher-test", AgentID: string(n.Binding.OperativeID)}, ExpiresAt: &n.ExpiresAt, Extra: map[string]json.RawMessage{"gate_request": quoted}}
}
func decodeGateData[T any](t *testing.T, verb string, raw []byte) T {
	t.Helper()
	var env response
	if json.Unmarshal(raw, &env) != nil || env.Version != ContractVersion || env.Provider != "tachyon" || env.Verb != verb {
		t.Fatalf("invalid success envelope: %s", raw)
	}
	var value T
	if err := json.Unmarshal(env.Data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func (f *nativeFixture) enqueue(key string) (gate.Request, hitl.Handle) {
	f.t.Helper()
	n, q := nativeRequest(key)
	b := f.want(f.anonymous, 200, "POST", gatePath("gate.enqueue", nil), q, f.producer)
	return n, decodeGateData[hitl.Handle](f.t, "gate.enqueue", b)
}
func getCommand(h hitl.Handle) hitl.GetCommand {
	return hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: hitl.CallerAssertion{ApplicationID: "gatecrasher-test"}}
}
func (f *nativeFixture) get(h hitl.Handle) hitl.RetrievalResult {
	f.t.Helper()
	return decodeGateData[hitl.RetrievalResult](f.t, "gate.get", f.want(f.anonymous, 200, "GET", gatePath("gate.get", getCommand(h)), nil, f.producer))
}
func choice(h hitl.Handle, option string) map[string]any {
	return map[string]any{"expected_revision": h.Revision, "presented_projection_revision": h.Revision, "response": hitl.Response{Kind: approvals.NativeKind, Decision: option}}
}
func browserPath(h hitl.Handle) string { return approvals.Prefix + "/items/" + h.ItemID }
func assessment(h hitl.Handle) approvals.AssessResponseCommand {
	return approvals.AssessResponseCommand{GetCommand: getCommand(h), ExpectedTerminalRevision: h.Revision + 1}
}

func TestNativeGateRealHTTPChoiceAndAssessment(t *testing.T) {
	f := nativeStack(t)
	for _, option := range []string{"native-allow-once", "native-deny", "native-cancel"} {
		t.Run(option, func(t *testing.T) {
			n, h := f.enqueue(option)
			v := f.get(h)
			if v.Validate() != nil {
				t.Fatal("invalid retrieval")
			}
			var retained hitl.EnqueueRequest
			_ = json.Unmarshal(v.Item.RequestSnapshot, &retained)
			actual, err := approvals.NativeRequest(retained)
			if err != nil || !actual.NativeRequestID.Equal(n.NativeRequestID) || len(retained.Extra) != 1 {
				t.Fatalf("immutable native snapshot changed: %s", v.Item.RequestSnapshot)
			}
			// An idempotent replay retains the same owner interaction; changed native
			// number/string identity conflicts through the native typed codec.
			_, q := nativeRequest(option)
			q.ExpiresAt = &n.ExpiresAt
			raw, _ := json.Marshal(n)
			q.Extra["gate_request"], _ = json.Marshal(string(raw))
			same := decodeGateData[hitl.Handle](t, "gate.enqueue", f.want(f.anonymous, 200, "POST", gatePath("gate.enqueue", nil), q, f.producer))
			if same.ItemID != h.ItemID {
				t.Fatal("replay created second item")
			}
			n.NativeRequestID = gate.NativeRequestID(`"9007199254740993"`)
			raw, _ = json.Marshal(n)
			q.Extra["gate_request"], _ = json.Marshal(string(raw))
			b := f.want(f.anonymous, 409, "POST", gatePath("gate.enqueue", nil), q, f.producer)
			var conflict *hitl.IdempotencyConflictError
			if !errors.As(hitl.DecodeError(b), &conflict) || conflict.ExistingItemID != h.ItemID {
				t.Fatalf("lost conflict fields: %s", b)
			}
			n.NativeRequestID = actual.NativeRequestID
			f.want(f.anonymous, 403, "POST", browserPath(h)+"/resolve", choice(h, option), f.producer)
			f.want(f.owner, 400, "POST", browserPath(h)+"/resolve", choice(h, "allow"), "")
			f.want(f.owner, 400, "POST", browserPath(h)+"/defer", map[string]any{"expected_revision": h.Revision, "duration_s": 3600}, "")
			batch := map[string]any{"batch_id": "batch-" + option, "decision": "approved", "items": []map[string]any{{"item_id": h.ItemID, "expected_revision": h.Revision, "presented_projection_revision": h.Revision}}}
			f.want(f.owner, 200, "POST", approvals.Prefix+"/batch/resolve", batch, "")
			if f.get(h).Item.State.IsTerminal() {
				t.Fatal("native item entered batch path")
			}
			f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, option), "")
			terminal := f.get(h)
			resolved, ok := terminal.Item.TerminalOutcome.(hitl.Resolved)
			if !ok || resolved.Resolution.Response.Decision != option || resolved.Resolution.Participant.Responder == nil || !strings.HasPrefix(resolved.Resolution.Participant.PrincipalRef, "user:") {
				t.Fatal("actual human outcome missing")
			}
			a := assessment(h)
			observed := decodeGateData[approvals.ResponseAssessment](t, "gate.assess-response", f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", a), nil, f.producer))
			if observed.TerminalRevision != terminal.Item.Revision || observed.Participant.PrincipalRef != resolved.Resolution.Participant.PrincipalRef || observed.ExpiresAt.Sub(observed.AssessedAt) > approvals.NativeAssessmentTTL || !observed.ExpiresAt.After(observed.AssessedAt) {
				t.Fatal("assessment not bound to actual outcome/time")
			}
			if f.get(h).Item.Revision != terminal.Item.Revision {
				t.Fatal("assessment wrote a decision")
			}
			a.ExpectedTerminalRevision--
			b = f.want(f.anonymous, 409, "GET", gatePath("gate.assess-response", a), nil, f.producer)
			var stale *hitl.StaleRevisionError
			if !errors.As(hitl.DecodeError(b), &stale) || stale.Outcome == nil || stale.Actual != terminal.Item.Revision {
				t.Fatalf("lost typed revision/outcome: %s", b)
			}
			w := hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: getCommand(h).Caller}
			b = f.want(f.anonymous, 409, "POST", gatePath("gate.withdraw", nil), w, f.producer)
			var terminalError *hitl.TerminalConflictError
			if !errors.As(hitl.DecodeError(b), &terminalError) || terminalError.Outcome == nil {
				t.Fatalf("lost terminal outcome: %s", b)
			}
		})
	}
	if f.callbackCalls.Load() != 3 {
		t.Fatalf("expected one callback per actual choice, got %d", f.callbackCalls.Load())
	}
}

func TestNativeGateRefusesUnavailableAndMismatchedAuthority(t *testing.T) {
	f := nativeStack(t)
	for _, mode := range []int32{1, 2, 3, 4} {
		f.callbackMode.Store(mode)
		_, h := f.enqueue("callback-" + strconv.FormatInt(int64(mode), 10))
		status := 403
		if mode == 1 {
			status = 503
		}
		f.want(f.owner, status, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
		if f.get(h).Item.State.IsTerminal() {
			t.Fatal("refused callback wrote outcome")
		}
	}
	f.callbackMode.Store(0)
	_, h := f.enqueue("pending-assessment")
	pendingAssessment := assessment(h)
	pendingAssessment.ExpectedTerminalRevision = h.Revision
	f.want(f.anonymous, 403, "GET", gatePath("gate.assess-response", pendingAssessment), nil, f.producer)
	other, _, err := f.acc.Issue(context.Background(), access.IssueInput{Name: "other-producer", Grants: access.Grants{Verbs: nativeGrants}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.want(f.anonymous, 403, "GET", gatePath("gate.get", getCommand(h)), nil, other)
	wrong := getCommand(h)
	wrong.Caller.ApplicationID = "other-namespace"
	f.want(f.anonymous, 403, "GET", gatePath("gate.get", wrong), nil, f.producer)
	f.want(f.anonymous, 405, "POST", gatePath("gate.assess-response", nil), assessment(h), f.producer)
	f.want(f.anonymous, 404, "POST", gatePath("gate.respond", nil), choice(h, "native-allow-once"), f.producer)
	if err = f.callbackIDs.RevokeKey(context.Background(), f.callbackKeyID); err != nil {
		t.Fatal(err)
	}
	f.want(f.owner, 403, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
	if f.get(h).Item.State.IsTerminal() {
		t.Fatal("revoked callback credential wrote outcome")
	}
}

func TestNativeGateCurrentRolesOverrideStaleUserToken(t *testing.T) {
	f := nativeStack(t)
	raw := f.want(f.owner, 201, "POST", "/api/identity/users", map[string]any{"username": "reviewer", "display_name": "Reviewer", "password": nativePassword, "scopes": []string{"tachyon.operator", "tachyon.approvals.read", "tachyon.approvals.resolve"}}, "")
	var user identity.PublicUser
	if json.Unmarshal(raw, &user) != nil || user.ID == "" {
		t.Fatalf("create user: %s", raw)
	}
	jar, _ := cookiejar.New(nil)
	reviewer := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	f.want(reviewer, 200, "POST", "/api/login", map[string]string{"username": "reviewer", "password": nativePassword}, "")
	var tok struct{ Token string }
	raw = f.want(reviewer, 200, "POST", "/api/token", map[string]any{"scopes": []string{"tachyon.operator", "tachyon.approvals.read", "tachyon.approvals.resolve"}}, "")
	_ = json.Unmarshal(raw, &tok)
	if tok.Token == "" {
		t.Fatal("missing real JWT")
	}
	_, h := f.enqueue("roles")
	f.want(f.anonymous, 200, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), tok.Token)
	a := assessment(h)
	f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	_, pending := f.enqueue("stale-role")
	for _, scopes := range [][]string{{"tachyon.operator", "tachyon.approvals.read"}, {"tachyon.approvals.read", "tachyon.approvals.resolve"}} {
		f.want(f.owner, 200, "PUT", "/api/identity/users/"+user.ID, map[string]any{"scopes": scopes}, "")
		f.want(f.anonymous, 403, "POST", browserPath(pending)+"/resolve", choice(pending, "native-allow-once"), tok.Token)
		f.want(f.anonymous, 403, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	}
	f.want(f.owner, 200, "PUT", "/api/identity/users/"+user.ID, map[string]any{"disabled": true}, "")
	f.want(f.anonymous, 403, "POST", browserPath(pending)+"/resolve", choice(pending, "native-allow-once"), tok.Token)
	f.want(f.anonymous, 403, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	// The live lookup can fail independently of the authenticated old JWT.
	current := f.inbox.Native.CurrentUser
	f.inbox.Native.CurrentUser = nil
	f.want(f.anonymous, 503, "POST", browserPath(pending)+"/resolve", choice(pending, "native-allow-once"), tok.Token)
	f.want(f.anonymous, 503, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	f.inbox.Native.CurrentUser = current
	if err := f.acc.Disable(context.Background(), f.credential.IdentityID); err != nil {
		t.Fatal(err)
	}
	f.want(f.anonymous, 403, "GET", gatePath("gate.assess-response", a), nil, f.producer)
}

func TestNativeGateAwaitExpiryAndWithdraw(t *testing.T) {
	f := nativeStack(t)
	_, h := f.enqueue("withdraw")
	wait := 0
	q := hitl.AwaitCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: getCommand(h).Caller, WaitMs: &wait}
	v := decodeGateData[hitl.RetrievalResult](t, "gate.await", f.want(f.anonymous, 200, "GET", gatePath("gate.await", q), nil, f.producer))
	if v.Item.State.IsTerminal() {
		t.Fatal("wait withdrew pending item")
	}
	w := hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: q.Caller, ExpectedRevision: &h.Revision, Reason: "caller ended request"}
	raw := decodeGateData[json.RawMessage](t, "gate.withdraw", f.want(f.anonymous, 200, "POST", gatePath("gate.withdraw", nil), w, f.producer))
	out, err := hitl.UnmarshalOutcome(raw)
	if err != nil || out.OutcomeState() != hitl.StateCanceled {
		t.Fatal("withdraw did not return native canceled Outcome")
	}
	n, req := nativeRequest("expired")
	n.ExpiresAt = time.Now().Add(-time.Second)
	req.ExpiresAt = &n.ExpiresAt
	for index := range n.Options {
		n.Options[index].Scope.ExpiresAt = n.ExpiresAt
	}
	b, _ := json.Marshal(n)
	req.Extra["gate_request"], _ = json.Marshal(string(b))
	h = decodeGateData[hitl.Handle](t, "gate.enqueue", f.want(f.anonymous, 200, "POST", gatePath("gate.enqueue", nil), req, f.producer))
	// A read-only assessment of an expired pending record must not materialize
	// expiry. Only the four native HITL verbs retain their computed-expiry behavior.
	before, err := f.inbox.Store.Get(context.Background(), h.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	a := assessment(h)
	f.want(f.anonymous, 409, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	after, err := f.inbox.Store.Get(context.Background(), h.ItemID)
	if err != nil || after.Revision != before.Revision || after.State != before.State || after.Outcome != nil {
		t.Fatal("read-only assessment materialized expiry")
	}
	q.ItemID = h.ItemID
	v = decodeGateData[hitl.RetrievalResult](t, "gate.await", f.want(f.anonymous, 200, "GET", gatePath("gate.await", q), nil, f.producer))
	if v.Item.State != hitl.StateExpired || v.Item.TerminalOutcome == nil {
		t.Fatal("await did not return computed expiry")
	}
}

func TestNativeGateConcurrentChoices(t *testing.T) {
	f := nativeStack(t)
	_, h := f.enqueue("concurrent-choices")
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, option := range []string{"native-allow-once", "native-deny"} {
		go func() {
			<-start
			status, _ := f.do(f.owner, "POST", browserPath(h)+"/resolve", choice(h, option), "")
			results <- status
		}()
	}
	close(start)
	first, second := <-results, <-results
	switch {
	case first == 200 && second == 409:
	case first == 409 && second == 200:
	default:
		t.Fatalf("concurrent choices: %d %d", first, second)
	}
	v := f.get(h)
	resolved, ok := v.Item.TerminalOutcome.(hitl.Resolved)
	if !ok || v.Item.Revision != h.Revision+1 || (resolved.Resolution.Response.Decision != "native-allow-once" && resolved.Resolution.Response.Decision != "native-deny") {
		t.Fatal("missing single authoritative winning choice")
	}
}

func TestNativeGateRejectsUnsupportedProfileAndForgedAssessment(t *testing.T) {
	f := nativeStack(t)
	n, q := nativeRequest("unsupported-scope")
	n.Options[0].Scope.Kind = gate.Session
	n.Options[0].Scope.MaxUses = 0
	raw, _ := json.Marshal(n)
	q.Extra["gate_request"], _ = json.Marshal(string(raw))
	f.want(f.anonymous, 400, "POST", gatePath("gate.enqueue", nil), q, f.producer)
	q.Kind = "card.tool_approval"
	f.want(f.anonymous, 400, "POST", gatePath("gate.enqueue", nil), q, f.producer)
	n, h := f.enqueue("forged-assessment")
	f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
	a := assessment(h)
	// This is a native HITL lookup, even though it returns a gate observation.
	// The gate discriminator must never be accepted as its input version.
	a.ContractVersion = gate.ContractVersion
	f.want(f.anonymous, 400, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	a = assessment(h)
	observed := decodeGateData[approvals.ResponseAssessment](t, "gate.assess-response", f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", a), nil, f.producer))
	digest, _ := n.Digest()
	if observed.ContractVersion != gate.ContractVersion || observed.IdempotencyKey != n.IdempotencyKey || observed.OptionID != n.Options[0].ID || observed.RequestDigest != digest || observed.OperationDigest != n.OperationDigest || observed.Binding != n.Binding || !observed.NativeRequestID.Equal(n.NativeRequestID) {
		t.Fatal("assessment did not derive full immutable tuple")
	}
	a = assessment(h)
	var forged map[string]any
	raw, _ = json.Marshal(a)
	_ = json.Unmarshal(raw, &forged)
	forged["participant"] = map[string]string{"principal_ref": "user:invented"}
	f.want(f.anonymous, 400, "GET", gatePath("gate.assess-response", forged), nil, f.producer)
	a = assessment(h)
	a.ItemID = "missing-item"
	b := f.want(f.anonymous, 404, "GET", gatePath("gate.assess-response", a), nil, f.producer)
	if !errors.Is(hitl.DecodeError(b), hitl.ErrNotFound) {
		t.Fatalf("missing scoped notfound error: %s", b)
	}
	partial, _, err := f.acc.Issue(context.Background(), access.IssueInput{Name: "gatecrasher", IdentityID: f.credential.IdentityID, Grants: access.Grants{Verbs: []string{"gate.get"}}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.want(f.anonymous, 403, "GET", gatePath("gate.assess-response", assessment(h)), nil, partial)
}

func TestNativeGateCallbackCredentialDirections(t *testing.T) {
	f := nativeStack(t)
	original := f.callbackCredential.Load().(string)
	_, h := f.enqueue("callback-credentials")
	req, err := http.NewRequest("POST", f.callbackIssuer+"/api/token", strings.NewReader(`{"scopes":["gatecrasher.gate.authorize-response"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+original)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.anonymous.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var jwt struct{ Token string }
	err = json.NewDecoder(res.Body).Decode(&jwt)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != 200 || jwt.Token == "" {
		t.Fatal("could not mint isolated callback-key JWT")
	}
	f.callbackCredential.Store(jwt.Token)
	f.want(f.owner, 503, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
	for _, scopes := range [][]string{{"gatecrasher.gate.authorize-response"}, {"gatecrasher.other.read"}} {
		wrong, _, err := f.callbackIDs.CreateKey(context.Background(), identity.KeyInput{Name: "unmapped-callback", Scopes: scopes, Audiences: []string{"gatecrasher"}, TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		f.callbackCredential.Store(wrong)
		f.want(f.owner, 403, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
	}
	f.callbackCredential.Store(f.producer)
	f.want(f.owner, 403, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
	if f.callbackCalls.Load() != 0 || f.get(h).Item.State.IsTerminal() {
		t.Fatal("untrusted callback credential retained decision authority")
	}
	f.callbackCredential.Store(original)
	f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, "native-allow-once"), "")
}

func TestNativeGateBrowser(t *testing.T) {
	if os.Getenv("TACHYON_NATIVE_BROWSER") != "1" {
		t.Skip("explicit real-browser lane after building embedded web assets")
	}
	f := nativeStack(t)
	n, h := f.enqueue("browser-native")
	cmd := exec.Command("node", "../../web/e2e/native-gate-browser.mjs")
	cmd.Env = append(os.Environ(), "NATIVE_TEST_BASE="+f.base, "NATIVE_TEST_ITEM="+h.ItemID, "NATIVE_TEST_PASSWORD="+nativePassword)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real browser: %v: %s", err, out)
	}
	v := f.get(h)
	resolved, ok := v.Item.TerminalOutcome.(hitl.Resolved)
	if !ok || resolved.Resolution.Response.Decision != n.Options[0].ID || f.callbackCalls.Load() != 1 {
		t.Fatal("browser did not resolve through authenticated callback and owner CAS")
	}
}

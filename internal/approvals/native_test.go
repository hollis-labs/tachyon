package approvals

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/hitl/hitltest"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func nativeStore(t *testing.T) *Store {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{DB: db}
}
func TestNativeStoreContract(t *testing.T) {
	hitltest.RunStoreContract(t, func(t *testing.T) hitl.Store { return nativeStore(t) })
}
func TestNativeServiceConformance(t *testing.T) {
	inbox := NewInbox(nativeStore(t), nil)
	hitltest.RunConformance(t, hitltest.NewServiceAdapter(inbox.Service), hitltest.ServiceCaps()...)
}
func requestFixture(t *testing.T, key, class string) hitl.EnqueueRequest {
	t.Helper()
	var req hitl.EnqueueRequest
	raw := fmt.Sprintf(`{"contract_version":"1.0","kind":"approval","idempotency_key":%q,"source":{"application_id":"test-producer","agent_id":"test-agent"},"class":%q,"title":"Deploy","summary":"Deploy one resource","request":"Deploy resource revision one","op_digest":"sha256:%s","handoff_url":"https://cerberus.example/approvals/one"}`, key, class, strings.Repeat("a", 64))
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	return req
}
func serveJSON(h http.Handler, method, path string, value any, token, cookie string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(value)
	req := httptest.NewRequest(method, "http://tachyon.test"+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://tachyon.test")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestNativeBrowserDecisionAuthority(t *testing.T) {
	elevated := false
	sessions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" {
			t.Errorf("unexpected Tangent queue call: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		assurance := "browser-session"
		if elevated {
			assurance = "operator-token"
		}
		writeValue(w, map[string]any{"elevated": elevated, "assurance": assurance})
	}))
	defer sessions.Close()
	inbox := NewInbox(nativeStore(t), New(sessions.URL))
	for _, class := range []string{"low", "medium", "high"} {
		t.Run(class, func(t *testing.T) {
			elevated = false
			handle, err := inbox.Service.Enqueue(context.Background(), requestFixture(t, class, class))
			if err != nil {
				t.Fatal(err)
			}
			path := Prefix + "/items/" + handle.ItemID + "/resolve"
			cmd := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "approved"}}
			w := serveJSON(inbox, "POST", path, cmd, "requester", "session")
			if w.Code != 403 {
				t.Fatalf("bearer decision: %d %s", w.Code, w.Body)
			}
			req := httptest.NewRequest("POST", "http://tachyon.test"+path, strings.NewReader(`{}`))
			req.Header.Set("Origin", "https://other.test")
			denied := httptest.NewRecorder()
			inbox.ServeHTTP(denied, req)
			if denied.Code != 403 {
				t.Fatal("cross-origin decision accepted")
			}
			cmd["participant"] = map[string]string{"assurance": "operator-token"}
			w = serveJSON(inbox, "POST", path, cmd, "", "session")
			if w.Code != 400 {
				t.Fatalf("injected participant: %d %s", w.Code, w.Body)
			}
			delete(cmd, "participant")
			w = serveJSON(inbox, "POST", path, cmd, "", "session")
			if class == "medium" || class == "high" {
				if w.Code != 403 {
					t.Fatalf("unelevated decision: %d %s", w.Code, w.Body)
				}
				elevated = true
				w = serveJSON(inbox, "POST", path, cmd, "", "session")
			}
			if class == "high" {
				if w.Code != 403 {
					t.Fatalf("high decision: %d %s", w.Code, w.Body)
				}
				return
			}
			if w.Code != 200 {
				t.Fatalf("authorized decision: %d %s", w.Code, w.Body)
			}
			rec, err := inbox.Store.Get(context.Background(), handle.ItemID)
			if err != nil || rec.State != hitl.StateResolved {
				t.Fatalf("retained result: %+v %v", rec, err)
			}
			if strings.Contains(w.Body.String(), `"proof"`) || !strings.Contains(w.Body.String(), `browser-session:`) {
				t.Fatalf("fabricated authority: %s", w.Body)
			}
			w = serveJSON(inbox, "POST", path, cmd, "", "session")
			if w.Code != 409 {
				t.Fatalf("stale replay: %d %s", w.Code, w.Body)
			}
		})
	}
	w := serveJSON(inbox, "GET", Prefix+"/inbox", nil, "", "session")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"history"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("inbox: %d %s", w.Code, w.Body)
	}
}
func TestNativeProducerJWTAndIsolation(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeValue(w, map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "kid": "test", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
	}))
	defer jwks.Close()
	inbox := NewInbox(nativeStore(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := NewProducer(ctx, inbox.Service, ProducerConfig{Issuer: "test-tether", Audience: "tachyon", JWKSURL: jwks.URL, Producers: map[string]ProducerPolicy{
		"producer-a": {ApplicationID: "test-producer", MinimumClass: "medium", HandoffOrigins: []string{"https://cerberus.example"}},
		"producer-b": {ApplicationID: "tether", MinimumClass: "low"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	token := func(over map[string]any) string {
		claims := map[string]any{"iss": "test-tether", "aud": "tachyon", "sub": "producer-a", "exp": time.Now().Add(time.Hour).Unix(), "scope": "tachyon.approvals_enqueue tachyon.approvals_read tachyon.approvals_withdraw"}
		for k, v := range over {
			claims[k] = v
		}
		enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
		raw := enc(map[string]string{"alg": "EdDSA", "kid": "test"}) + "." + enc(claims)
		return raw + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(raw)))
	}
	req := requestFixture(t, "producer", "medium")
	for _, tc := range []struct {
		name string
		over map[string]any
		code int
	}{
		{"issuer", map[string]any{"iss": "someone-else"}, 401}, {"audience", map[string]any{"aud": "cerberus"}, 401},
		{"expiry", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}, 401}, {"scope", map[string]any{"scope": "tachyon.approvals_read"}, 403},
		{"unsupported nested scope", map[string]any{"scope": "tachyon.approvals.enqueue"}, 403},
		{"unmapped", map[string]any{"sub": "unknown"}, 403}, {"impersonation", map[string]any{"sub": "producer-b"}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serveJSON(p, "POST", ProducerPrefix+"/enqueue", req, token(tc.over), "")
			if w.Code != tc.code {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}
	w := serveJSON(p, "POST", ProducerPrefix+"/enqueue", req, token(nil), "")
	if w.Code != 200 {
		t.Fatalf("enqueue: %d %s", w.Code, w.Body)
	}
	var handle hitl.Handle
	if err := json.Unmarshal(w.Body.Bytes(), &handle); err != nil {
		t.Fatal(err)
	}
	replay := serveJSON(p, "POST", ProducerPrefix+"/enqueue", req, token(nil), "")
	if replay.Code != 200 || replay.Body.String() != w.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	req.Extra["summary"] = json.RawMessage(`"Changed operation"`)
	w = serveJSON(p, "POST", ProducerPrefix+"/enqueue", req, token(nil), "")
	if w.Code != 409 {
		t.Fatalf("idempotency conflict: %d %s", w.Code, w.Body)
	}
	for _, op := range []string{"get", "await", "withdraw"} {
		cmd := map[string]any{"contract_version": "1.0", "item_id": handle.ItemID, "caller": map[string]string{"application_id": "tether", "principal_ref": "producer-b"}}
		w = serveJSON(p, "POST", ProducerPrefix+"/"+op, cmd, token(map[string]any{"sub": "producer-b"}), "")
		if w.Code != 404 {
			t.Fatalf("cross-producer %s: %d %s", op, w.Code, w.Body)
		}
	}
	cmd := map[string]any{"contract_version": "1.0", "item_id": handle.ItemID, "caller": map[string]string{"application_id": "test-producer"}, "wait_ms": 1}
	w = serveJSON(p, "POST", ProducerPrefix+"/await", cmd, token(nil), "")
	if w.Code != 200 {
		t.Fatalf("await: %d %s", w.Code, w.Body)
	}
	rec, _ := inbox.Store.Get(context.Background(), handle.ItemID)
	if rec.State.IsTerminal() {
		t.Fatal("await timeout terminated request")
	}
	delete(cmd, "wait_ms")
	w = serveJSON(p, "POST", ProducerPrefix+"/withdraw", cmd, token(nil), "")
	if w.Code != 200 {
		t.Fatalf("withdraw: %d %s", w.Code, w.Body)
	}
	w = serveJSON(p, "POST", ProducerPrefix+"/get", cmd, token(nil), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"canceled"`) {
		t.Fatalf("lookup: %d %s", w.Code, w.Body)
	}
	w = serveJSON(p, "POST", ProducerPrefix+"/resolve", cmd, token(nil), "")
	if w.Code != 404 {
		t.Fatal("producer resolve route exposed")
	}
	req = requestFixture(t, "too-low", "low")
	w = serveJSON(p, "POST", ProducerPrefix+"/enqueue", req, token(nil), "")
	if w.Code != 400 {
		t.Fatalf("class downgrade: %d %s", w.Code, w.Body)
	}
	req = requestFixture(t, "evil-handoff", "high")
	req.Extra["handoff_url"] = json.RawMessage(`"https://evil.example/approve"`)
	w = serveJSON(p, "POST", ProducerPrefix+"/enqueue", req, token(nil), "")
	if w.Code != 400 {
		t.Fatalf("untrusted handoff: %d %s", w.Code, w.Body)
	}
}
func TestNativeRestartAndPaging(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retained.db")
	db, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	inbox := NewInbox(&Store{DB: db}, nil)
	handle, err := inbox.Service.Enqueue(ctx, requestFixture(t, "retain", "low"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: handle.ItemID, Response: hitl.Response{Kind: "approval", Decision: "denied"}, Participant: hitl.Participant{PrincipalRef: "operator", Assurance: "browser-session"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inbox = NewInbox(&Store{DB: db}, nil)
	rec, err := inbox.Store.Get(ctx, handle.ItemID)
	if err != nil || rec.State != hitl.StateResolved || rec.Outcome == nil {
		t.Fatalf("restart: %+v %v", rec, err)
	}
	for n := 0; n < 4; n++ {
		_, err = inbox.Service.Enqueue(ctx, requestFixture(t, fmt.Sprint(n), "low"))
		if err != nil {
			t.Fatal(err)
		}
	}
	first, cursor, err := inbox.Store.Page(ctx, false, "", 2)
	if err != nil || len(first) != 2 || cursor == "" {
		t.Fatalf("first page: %v %q %v", first, cursor, err)
	}
	second, next, err := inbox.Store.Page(ctx, false, cursor, 2)
	if err != nil || len(second) != 2 || next != "" || second[0].ItemID == first[0].ItemID {
		t.Fatalf("second page: %v %q %v", second, next, err)
	}
}

func TestProducerPolicyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"valid", `{"issuer":"tether","audience":"tachyon","jwks_url":"https://tether.example/.well-known/jwks.json","producers":{"service-one":{"application_id":"cerberus","minimum_class":"high","handoff_origins":["https://cerberus.example"]}}}`, true},
		{"audience", `{"issuer":"tether","audience":"tangent","producers":{"a":{"application_id":"app","minimum_class":"low"}}}`, false},
		{"cerberus downgrade", `{"issuer":"tether","audience":"tachyon","producers":{"a":{"application_id":"cerberus","minimum_class":"low"}}}`, false},
		{"shared application", `{"issuer":"tether","audience":"tachyon","producers":{"a":{"application_id":"app","minimum_class":"low"},"b":{"application_id":"app","minimum_class":"low"}}}`, false},
		{"handoff path", `{"issuer":"tether","audience":"tachyon","producers":{"a":{"application_id":"app","minimum_class":"high","handoff_origins":["https://good.example/redirect"]}}}`, false},
		{"public HTTP", `{"issuer":"tether","audience":"tachyon","jwks_url":"http://example.com/keys","producers":{"a":{"application_id":"app","minimum_class":"low"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadProducerConfig(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	req := requestFixture(t, "cerberus", "medium")
	req.Source.ApplicationID = "cerberus"
	if err := validateInboxRequest(req, ProducerPolicy{MinimumClass: "low", HandoffOrigins: []string{"https://cerberus.example"}}); err == nil {
		t.Fatal("Cerberus local decision accepted")
	}
	disabled := serveJSON(&Producer{}, "POST", ProducerPrefix+"/enqueue", req, "anything", "")
	if disabled.Code != 503 {
		t.Fatal("unconfigured producer accepted")
	}
}

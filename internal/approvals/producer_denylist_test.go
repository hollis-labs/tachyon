package approvals

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Producer tokens are revocable: with deny_list_url the producer polls the
// issuer's revocation list. That matters most for long-lived kind=service
// tokens (a distant exp), which nothing else would ever expire.

type denyIssuer struct {
	priv   ed25519.PrivateKey
	jwks   *httptest.Server
	deny   *httptest.Server
	status atomic.Int32
	jtis   atomic.Value // []string
}

func newDenyIssuer(t *testing.T) *denyIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &denyIssuer{priv: priv}
	d.status.Store(200)
	d.jtis.Store([]string{})
	d.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeValue(w, map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "kid": "test", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
	}))
	d.deny = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if code := int(d.status.Load()); code != 200 {
			http.Error(w, "down", code)
			return
		}
		writeValue(w, map[string]any{"jti": d.jtis.Load().([]string), "sub": []string{}, "sid": []string{}})
	}))
	t.Cleanup(d.jwks.Close)
	t.Cleanup(d.deny.Close)
	return d
}

// serviceToken is a long-lived kind=service token: a distant exp, a jti.
func (d *denyIssuer) serviceToken(jti string) string {
	claims := map[string]any{"iss": "test-tether", "aud": "tachyon", "sub": "producer-a", "kind": "service", "jti": jti,
		"exp": time.Now().Add(10 * 365 * 24 * time.Hour).Unix(), "scope": "tachyon.approvals_enqueue tachyon.approvals_read tachyon.approvals_withdraw"}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	raw := enc(map[string]string{"alg": "EdDSA", "kid": "test"}) + "." + enc(claims)
	return raw + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(d.priv, []byte(raw)))
}

func (d *denyIssuer) producer(t *testing.T) *Producer {
	t.Helper()
	old := denyListInterval
	denyListInterval = 20 * time.Millisecond
	t.Cleanup(func() { denyListInterval = old })
	inbox := NewInbox(nativeStore(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := NewProducer(ctx, inbox.Service, ProducerConfig{Issuer: "test-tether", Audience: "tachyon", JWKSURL: d.jwks.URL, DenyListURL: d.deny.URL,
		Producers: map[string]ProducerPolicy{"producer-a": {ApplicationID: "test-producer", MinimumClass: "medium", HandoffOrigins: []string{"https://cerberus.example"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func enqueueStatus(t *testing.T, p *Producer, token, key string) int {
	t.Helper()
	return serveJSON(p, "POST", ProducerPrefix+"/enqueue", requestFixture(t, key, "medium"), token, "").Code
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestProducerServiceTokenIsRevokedByTheDenyList(t *testing.T) {
	d := newDenyIssuer(t)
	p := d.producer(t)
	good, revoked := d.serviceToken("svc-good"), d.serviceToken("svc-revoked")
	eventually(t, "the first deny-list poll", func() bool { return p.current().feed.Ready() })
	if got := enqueueStatus(t, p, revoked, "k1"); got != 200 {
		t.Fatalf("a long-lived service token before revocation: %d", got)
	}
	d.jtis.Store([]string{"svc-revoked"}) // the issuer revokes it
	eventually(t, "the revocation to be applied", func() bool { return enqueueStatus(t, p, revoked, "k2") == 401 })
	if got := enqueueStatus(t, p, good, "k3"); got != 200 {
		t.Fatalf("an unrevoked service token was refused: %d", got)
	}
}

func TestProducerKeepsKnownRevocationsWhileTheDenyListIsDown(t *testing.T) {
	d := newDenyIssuer(t)
	d.jtis.Store([]string{"svc-revoked"})
	p := d.producer(t)
	revoked := d.serviceToken("svc-revoked")
	eventually(t, "the revocation to be learned", func() bool { return p.current().feed.Ready() && enqueueStatus(t, p, revoked, "k1") == 401 })
	d.status.Store(503) // the issuer's deny-list goes down
	time.Sleep(200 * time.Millisecond)
	if got := enqueueStatus(t, p, revoked, "k2"); got != 401 {
		t.Fatalf("a revoked token was accepted during a deny-list outage: %d (the last list stays in force)", got)
	}
	if got := enqueueStatus(t, p, d.serviceToken("svc-ok"), "k3"); got != 200 {
		t.Fatalf("an unrevoked token should still be served from the stale list: %d", got)
	}
}

func TestProducerRefusesUntilTheFirstDenyListPollSucceeds(t *testing.T) {
	d := newDenyIssuer(t)
	d.status.Store(503) // the deny-list is down from the start
	p := d.producer(t)
	tok := d.serviceToken("svc-1")
	time.Sleep(150 * time.Millisecond)
	if p.current().feed.Ready() {
		t.Fatal("ready without a successful poll")
	}
	if got := enqueueStatus(t, p, tok, "k1"); got != 503 {
		t.Fatalf("before the first poll the producer must refuse, got %d", got)
	}
	d.status.Store(200)
	// The first-poll retry backs off (2s), so allow for it.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !p.current().feed.Ready() {
		time.Sleep(50 * time.Millisecond)
	}
	if !p.current().feed.Ready() {
		t.Fatal("never became ready after the deny-list recovered")
	}
	if got := enqueueStatus(t, p, tok, "k2"); got != 200 {
		t.Fatalf("after the first poll: %d", got)
	}
}

func TestProducerWithoutADenyListStillWorksAsBefore(t *testing.T) {
	d := newDenyIssuer(t)
	inbox := NewInbox(nativeStore(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := NewProducer(ctx, inbox.Service, ProducerConfig{Issuer: "test-tether", Audience: "tachyon", JWKSURL: d.jwks.URL,
		Producers: map[string]ProducerPolicy{"producer-a": {ApplicationID: "test-producer", MinimumClass: "medium", HandoffOrigins: []string{"https://cerberus.example"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.current().feed != nil {
		t.Fatal("a deny-list feed exists without deny_list_url")
	}
	if got := enqueueStatus(t, p, d.serviceToken("svc-1"), "k1"); got != 200 {
		t.Fatalf("no deny-list configured: %d", got)
	}
}

func TestProducerDenyListURLIsValidated(t *testing.T) {
	base := `{"issuer":"tether","audience":"tachyon","jwks_url":"https://tether.example/.well-known/jwks.json","deny_list_url":%q,"producers":{"a":{"application_id":"app","minimum_class":"low"}}}`
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://tether.example/auth/denylist", true},
		{"http://127.0.0.1:8081/auth/denylist", true},
		{"http://example.com/auth/denylist", false},
		{"https://user:pw@tether.example/auth/denylist", false},
		{"https://tether.example/auth/denylist?token=x", false},
		{"https://tether.example/auth/denylist#frag", false},
		{"ftp://tether.example/x", false},
	} {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(base, tc.url)), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadProducerConfig(path)
		if (err == nil) != tc.valid {
			t.Errorf("deny_list_url %q: valid=%v error=%v", tc.url, tc.valid, err)
		}
		if err != nil && containsSecret(err.Error(), "pw", "token=x") {
			t.Errorf("deny_list_url %q: the error echoes the URL: %v", tc.url, err)
		}
	}
}

func containsSecret(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestProducerFirstPollWaitIsLoggedByHostOnlyAndRefusalsLeakNothing(t *testing.T) {
	logs := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	d := newDenyIssuer(t)
	d.status.Store(503) // the deny list is down from the start
	old := denyListInterval
	denyListInterval = 20 * time.Millisecond
	t.Cleanup(func() { denyListInterval = old })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	secretURL := d.deny.URL + "/tenant-7f3a/denylist"
	p, err := NewProducer(ctx, NewInbox(nativeStore(t), nil).Service, ProducerConfig{Issuer: "test-tether", Audience: "tachyon", JWKSURL: d.jwks.URL, DenyListURL: secretURL,
		Producers: map[string]ProducerPolicy{"producer-a": {ApplicationID: "test-producer", MinimumClass: "medium", HandoffOrigins: []string{"https://cerberus.example"}}}})
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(d.deny.URL, "http://")
	eventually(t, "the first-poll wait log", func() bool {
		return strings.Contains(logs.String(), "refusing requests until the first revocation-list poll succeeds")
	})

	rec := serveJSON(p, "POST", ProducerPrefix+"/enqueue", requestFixture(t, "k1", "medium"), d.serviceToken("svc-1"), "")
	if rec.Code != 503 {
		t.Fatalf("before the first poll: %d", rec.Code)
	}
	for _, leak := range []string{host, "tenant-7f3a", "denylist", "http://", "127.0.0.1"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the 503 body leaks %q: %s", leak, rec.Body.String())
		}
	}
	out := logs.String()
	if !strings.Contains(out, "host="+host) || !strings.Contains(out, "level=WARN") {
		t.Errorf("the wait log must be a WARN naming the host %q: %s", host, out)
	}
	for _, leak := range []string{"tenant-7f3a", "denylist", "http://", "status 503", "Get "} {
		if strings.Contains(out, leak) {
			t.Errorf("the log leaks %q: %s", leak, out)
		}
	}

	d.status.Store(200) // recovery: one INFO, host only
	eventually(t, "the ready log", func() bool { return strings.Contains(logs.String(), "revocation list ready") })
	if strings.Contains(logs.String(), "tenant-7f3a") {
		t.Errorf("the ready log leaks the path: %s", logs.String())
	}
}

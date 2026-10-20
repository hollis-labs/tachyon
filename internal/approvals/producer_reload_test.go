package approvals

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A producer reload swaps one immutable generation (verifier, deny list,
// subject mapping): a request uses the generation it started with, a bad
// reload keeps the last good one, and a reload never forgets a revocation.

func (d *denyIssuer) config(denyURL, app string) ProducerConfig {
	return ProducerConfig{Issuer: "test-tether", Audience: "tachyon", JWKSURL: d.jwks.URL, DenyListURL: denyURL,
		Producers: map[string]ProducerPolicy{"producer-a": {ApplicationID: app, MinimumClass: "medium", HandoffOrigins: []string{"https://cerberus.example"}}}}
}

func (d *denyIssuer) reloadable(t *testing.T, cfg ProducerConfig) (*Producer, context.Context) {
	t.Helper()
	old := denyListInterval
	denyListInterval = 20 * time.Millisecond
	t.Cleanup(func() { denyListInterval = old })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := NewProducer(ctx, NewInbox(nativeStore(t), nil).Service, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g := p.current(); g.feed != nil {
		eventually(t, "the first deny-list poll", g.feed.Ready)
	}
	return p, ctx
}

func TestProducerRequestKeepsItsGenerationAcrossAReload(t *testing.T) {
	d := newDenyIssuer(t)
	p, _ := d.reloadable(t, d.config(d.deny.URL, "test-producer"))
	tok := d.serviceToken("svc-1")

	raw, _ := json.Marshal(requestFixture(t, "k-paused", "medium"))
	pr, pw := io.Pipe()
	req := httptest.NewRequest("POST", "http://tachyon.test"+ProducerPrefix+"/enqueue", pr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); p.ServeHTTP(rec, req) }()
	time.Sleep(100 * time.Millisecond) // authenticated, now blocked reading the body

	// The mapping moves the subject to another application.
	if err := p.Reload(d.config(d.deny.URL, "someone-else")); err != nil {
		t.Fatal(err)
	}
	_, _ = pw.Write(raw)
	_ = pw.Close()
	<-done
	if rec.Code != 200 {
		t.Fatalf("the paused request must finish on the generation it started with: %d %s", rec.Code, rec.Body.String())
	}
	if got := enqueueStatus(t, p, tok, "k-after"); got != 403 {
		t.Fatalf("a new request must see the new mapping (test-producer is no longer this subject's application): %d", got)
	}
}

func TestProducerReloadKeepsRevocationsThroughADenyListOutage(t *testing.T) {
	d := newDenyIssuer(t)
	d.jtis.Store([]string{"svc-revoked"})
	p, _ := d.reloadable(t, d.config(d.deny.URL, "test-producer"))
	revoked := d.serviceToken("svc-revoked")
	eventually(t, "the revocation to be learned", func() bool { return enqueueStatus(t, p, revoked, "k1") == 401 })

	d.status.Store(503) // the issuer's deny list goes down, then the config is reloaded
	if err := p.Reload(d.config(d.deny.URL, "test-producer")); err != nil {
		t.Fatalf("an unchanged deny-list endpoint must not need a poll to reload: %v", err)
	}
	if got := enqueueStatus(t, p, revoked, "k2"); got != 401 {
		t.Fatalf("a reload forgot a revocation: %d", got)
	}
	if got := enqueueStatus(t, p, d.serviceToken("svc-ok"), "k3"); got != 200 {
		t.Fatalf("an unrevoked token should be served from the stale list after a reload: %d", got)
	}
}

func TestProducerReloadToANewDenyListNeedsAFirstPoll(t *testing.T) {
	d := newDenyIssuer(t)
	p, _ := d.reloadable(t, d.config(d.deny.URL, "test-producer"))
	tok := d.serviceToken("svc-1")

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", 503) }))
	defer down.Close()
	err := p.Reload(d.config(down.URL+"/secret-path", "someone-else"))
	if err == nil {
		t.Fatal("a reload to an unreachable deny list must fail")
	}
	if strings.Contains(err.Error(), down.URL) || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("the error leaks the endpoint: %v", err)
	}
	if got := enqueueStatus(t, p, tok, "k1"); got != 200 {
		t.Fatalf("the failed reload must leave the last good generation (and mapping) in force: %d", got)
	}

	// A reachable new list is adopted, with its revocations.
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeValue(w, map[string]any{"jti": []string{"svc-1"}, "sub": []string{}, "sid": []string{}})
	}))
	defer next.Close()
	if err := p.Reload(d.config(next.URL, "test-producer")); err != nil {
		t.Fatal(err)
	}
	if got := enqueueStatus(t, p, tok, "k2"); got != 401 {
		t.Fatalf("the new deny list's revocation must apply immediately after the reload: %d", got)
	}
}

func TestProducerBadReloadKeepsTheLastGoodConfig(t *testing.T) {
	d := newDenyIssuer(t)
	p, _ := d.reloadable(t, d.config(d.deny.URL, "test-producer"))
	tok := d.serviceToken("svc-1")

	deadJWKS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", 500) }))
	defer deadJWKS.Close()
	cfg := d.config(d.deny.URL, "test-producer")
	cfg.JWKSURL = deadJWKS.URL
	if err := p.Reload(cfg); err == nil {
		t.Fatal("a reload whose JWKS cannot be fetched and has no bootstrap key must fail")
	}
	if err := p.Reload(ProducerConfig{}); err == nil {
		t.Fatal("a reload must not disable the producer API")
	}
	if got := enqueueStatus(t, p, tok, "k1"); got != 200 {
		t.Fatalf("bad reloads must leave the last good config serving: %d", got)
	}
}

func TestProducerWatchAppliesAFileChangeAndIgnoresABadEdit(t *testing.T) {
	d := newDenyIssuer(t)
	path := filepath.Join(t.TempDir(), "producers.json")
	write := func(cfg ProducerConfig) {
		b, _ := json.Marshal(cfg)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(d.config(d.deny.URL, "test-producer"))
	p, ctx := d.reloadable(t, d.config(d.deny.URL, "test-producer"))
	go func() { _ = p.Watch(ctx, path, 20*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	tok := d.serviceToken("svc-1")

	if err := os.WriteFile(path, []byte(`{"issuer": `), 0o600); err != nil { // a half-written / bad edit
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := enqueueStatus(t, p, tok, "k1"); got != 200 {
		t.Fatalf("a bad edit must not take the producer down: %d", got)
	}
	write(d.config(d.deny.URL, "someone-else"))
	eventually(t, "the file change to be applied", func() bool { return enqueueStatus(t, p, tok, "k-"+time.Now().Format("150405.000000")) == 403 })
}

func TestProducerReloadBetweenVerifyAndSubjectLookupDoesNotMixGenerations(t *testing.T) {
	d := newDenyIssuer(t)
	p, _ := d.reloadable(t, d.config(d.deny.URL, "test-producer"))
	reloaded := false
	p.afterVerify = func() {
		if reloaded {
			return
		}
		reloaded = true
		if err := p.Reload(d.config(d.deny.URL, "someone-else")); err != nil {
			t.Error(err)
		}
	}
	// Verified by the first generation, the subject must be looked up in it too.
	if got := enqueueStatus(t, p, d.serviceToken("svc-1"), "k1"); got != 200 {
		t.Fatalf("a reload between verification and the subject lookup changed the request's policy: %d", got)
	}
	p.afterVerify = nil
	if got := enqueueStatus(t, p, d.serviceToken("svc-1"), "k2"); got != 403 {
		t.Fatalf("the next request must see the new generation: %d", got)
	}
}

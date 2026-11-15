package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/agentsapi"
)

// The agents read routes in the served binary (slice B8a). Anonymous is 401; a key without
// tachyon.agents.read is refused by scope; a key with it reads Agent Ops, the ceremony meter, the bus
// feed and Needs you (the old guard let reads through), as does the owner. The writes are B8b's and are
// not served.
func TestAgentsRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "agents-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	reader, other := key("tachyon.agents.read"), key("tachyon.planner.read")
	p := agentsapi.Prefix
	reads := []string{p + "/sessions", p + "/sessions?include_ended=1", p + "/settings", p + "/seats", p + "/collector", p + "/bus/events",
		"/api/plugins/console/v1/ceremony", "/api/plugins/console/v1/needs-you"}
	anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, path := range append(reads, p+"/sessions/nope") {
		anon.want(401, "GET", path, nil, xrw)
		if code := flatCode(t, keys.want(403, "GET", path, nil, other)); code != "scope_required" {
			t.Errorf("%s without the scope: %s", path, code)
		}
	}
	for _, path := range reads {
		keys.want(200, "GET", path, nil, reader)
		owner.want(200, "GET", path, nil, xrw)
	}
	if code := flatCode(t, owner.want(404, "GET", p+"/sessions/nope", nil, xrw)); code != "not_found" {
		t.Errorf("unknown session: %s", code)
	}
	for _, w := range [][2]string{{"PUT", p + "/settings"}, {"POST", p + "/sessions/s1/nudge"}, {"POST", p + "/sessions/s1/restart"}, {"POST", p + "/controls/r1/skip"}} {
		if code, _ := owner.do(w[0], w[1], map[string]any{}, xrw); code != 404 {
			t.Errorf("%s %s served before B8b: %d", w[0], w[1], code)
		}
	}
}

// With Tether and Torque configured and the collector switch off, the Agent Ops reads dial nothing:
// the collector is not started and its routes read the store.
func TestAgentsReadsDialNothing(t *testing.T) {
	up := countingListener(t)
	base, _ := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q }\n    torque: { url: %q }\n", up.addr, up.addr))
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	p := agentsapi.Prefix
	for _, path := range []string{p + "/sessions", p + "/settings", p + "/seats", p + "/collector", p + "/bus/events"} {
		c.want(200, "GET", path, nil, xrw)
	}
	if n := up.conns.Load(); n != 0 {
		t.Errorf("%d connections to the upstreams with the collector off", n)
	}
}

// The agents event stream ends when the server shuts down, so Serve returns within its grace period.
func TestAgentsStreamEndsOnShutdown(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	createOwner(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- (&tachyon{app: run.App, ready: func(a net.Addr) { addr <- a }}).serve(ctx) }()
	base := "http://" + (<-addr).String()
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	req, _ := http.NewRequest(http.MethodGet, base+agentsapi.Prefix+"/stream", nil)
	res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	begin := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve ended with %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not return within the grace period with the agents stream open")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("shutdown took %s with the agents stream open", took)
	}
}

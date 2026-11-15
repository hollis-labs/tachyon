package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/builderapi"
)

// The builder and catalog reads in the served binary (slice B9a), on a fresh database with no
// catalog_state row. Anonymous is 401, a key without the scope is refused, and a key with
// tachyon.builder.read / tachyon.catalog.read reads them, as does the owner. Without Tether the
// registry and team policy answer as the old build did when unconfigured; the imported team snapshot is
// empty.
func TestBuilderRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	var n int
	if err := database.From(app).QueryRowContext(context.Background(), `SELECT count(*) FROM catalog_state`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("catalog_state rows on a fresh database: %d %v", n, err)
	}
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "builder-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	reader, other := key("tachyon.builder.read", "tachyon.catalog.read"), key("tachyon.planner.read")
	p := builderapi.Prefix
	reads := []string{p + "/summary", p + "/agents", p + "/launches", p + "/library/prompts", "/api/plugins/console/v1/catalog/imported-teams"}
	anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, path := range append(reads, builderapi.RegistryPrefix+"/projects", "/api/plugins/console/v1/catalog/team-policy") {
		anon.want(401, "GET", path, nil, xrw)
		if code := flatCode(t, keys.want(403, "GET", path, nil, other)); code != "scope_required" {
			t.Errorf("%s without the scope: %s", path, code)
		}
	}
	for _, path := range reads {
		keys.want(200, "GET", path, nil, reader)
		owner.want(200, "GET", path, nil, xrw)
	}
	if body := owner.want(200, "GET", "/api/plugins/console/v1/catalog/imported-teams", nil, xrw); !strings.Contains(string(body), `"teams":[]`) {
		t.Errorf("imported teams on a fresh database: %s", body)
	}
	if code := flatCode(t, owner.want(503, "GET", "/api/plugins/console/v1/catalog/team-policy", nil, xrw)); code != "configuration" {
		t.Errorf("team policy without Tether: %s", code)
	}
	owner.want(503, "GET", builderapi.RegistryPrefix+"/projects", nil, xrw)
	owner.want(404, "GET", p+"/agents/nope", nil, xrw)
}

// fakeCatalogTether counts requests and answers the catalog index's reads.
type fakeCatalogTether struct {
	mu   sync.Mutex
	reqs []string
}

func newFakeCatalogTether(t *testing.T) (*fakeCatalogTether, string) {
	t.Helper()
	f := &fakeCatalogTether{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/catalog/files":
			_, _ = w.Write([]byte(`{"files":[]}`))
		case "/catalog/history":
			_, _ = w.Write([]byte(`{"entries":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeCatalogTether) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// With Tether configured and catalog_index off, the catalog summary, the library and the imported team
// snapshot dial nothing; turning catalog_index on starts the index, which reads Tether's catalog.
func TestBuilderCatalogIndexSwitch(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprint("catalog_index ", on), func(t *testing.T) {
			fake, url := newFakeCatalogTether(t)
			base, _ := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q }\n  features: { catalog_index: %t }\n", url, on))
			c := newClient(t, base)
			c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
			for _, path := range []string{builderapi.Prefix + "/summary", builderapi.Prefix + "/library/prompts", "/api/plugins/console/v1/catalog/imported-teams"} {
				c.want(200, "GET", path, nil, xrw)
			}
			if !on {
				if n := fake.count(); n != 0 {
					t.Errorf("%d requests to Tether with catalog_index off", n)
				}
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for fake.count() == 0 && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if fake.count() == 0 {
				t.Error("the catalog index did not read Tether with catalog_index on")
			}
		})
	}
}

// The Builder's catalog stream ends when the server shuts down.
func TestBuilderStreamEndsOnShutdown(t *testing.T) {
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
	req, _ := http.NewRequest(http.MethodGet, base+builderapi.Prefix+"/stream", nil)
	res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
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
		t.Fatal("serve did not return within the grace period with the builder stream open")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("shutdown took %s with the builder stream open", took)
	}
}

// Builder writes in the served binary (slice B9b). Each write needs tachyon.builder.write and an operator.
// With builder_changes off nothing reaches Tether: the change routes answer 503 changes_unavailable, and the
// registry writes 501, as the old build did unconfigured. With it on (and only the read credential) the
// change service runs in handoff mode and the change routes reach it.
func TestBuilderWrites(t *testing.T) {
	writes := [][2]string{{"POST", builderapi.Prefix + "/changes"}, {"POST", builderapi.Prefix + "/changes/c1/submit"}, {"POST", builderapi.Prefix + "/changes/c1/cancel"},
		{"POST", builderapi.Prefix + "/changes/c1/rearm"}, {"POST", builderapi.Prefix + "/changes/c1/mark_planned"}, {"POST", builderapi.Prefix + "/changes/c1/check"},
		{"POST", builderapi.Prefix + "/launches/l1/launch"}, {"POST", builderapi.RegistryPrefix + "/projects"}, {"PATCH", builderapi.RegistryPrefix + "/projects/p1"},
		{"POST", builderapi.RegistryPrefix + "/projects/p1/link"}, {"DELETE", builderapi.RegistryPrefix + "/projects/p1/link"}}
	t.Run("off", func(t *testing.T) {
		fake, url := newFakeCatalogTether(t)
		tok := filepath.Join(t.TempDir(), "tether.token")
		if err := os.WriteFile(tok, []byte("test-read-credential"), 0o600); err != nil {
			t.Fatal(err)
		}
		base, app := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q, token: %q }\n", url, "file://"+tok))
		ids := identity.From(app)
		key := func(scopes ...string) map[string]string {
			k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "builder-write-test", Scopes: scopes})
			if err != nil {
				t.Fatal(err)
			}
			return map[string]string{"Authorization": "Bearer " + k}
		}
		reader, writer := key("tachyon.builder.read"), key("tachyon.builder.read", "tachyon.builder.write")
		anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
		owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
		for _, w := range writes {
			anon.want(401, w[0], w[1], map[string]any{}, xrw)
			if code := flatCode(t, keys.want(403, w[0], w[1], map[string]any{}, reader)); code != "scope_required" {
				t.Errorf("%s %s with the read scope: %s", w[0], w[1], code)
			}
			if code := flatCode(t, keys.want(403, w[0], w[1], map[string]any{}, writer)); code != "operator_required" {
				t.Errorf("%s %s for a key: %s", w[0], w[1], code)
			}
		}
		for _, w := range writes[:7] {
			if code := flatCode(t, owner.want(503, w[0], w[1], map[string]any{}, xrw)); code != "changes_unavailable" {
				t.Errorf("%s %s with builder_changes off: %s", w[0], w[1], code)
			}
		}
		owner.want(503, "GET", builderapi.Prefix+"/changes", nil, xrw)
		owner.want(501, "POST", builderapi.RegistryPrefix+"/projects", map[string]any{"display_name": "x"}, xrw)
		owner.want(200, "GET", builderapi.Prefix+"/capabilities", nil, xrw)
		if n := fake.count(); n != 0 {
			t.Errorf("%d requests to Tether with builder_changes off", n)
		}
	})
	t.Run("on", func(t *testing.T) {
		_, url := newFakeCatalogTether(t)
		tok := filepath.Join(t.TempDir(), "tether.token")
		if err := os.WriteFile(tok, []byte("test-read-credential"), 0o600); err != nil {
			t.Fatal(err)
		}
		base, _ := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q, token: %q }\n  features: { builder_changes: true }\n", url, "file://"+tok))
		owner := newClient(t, base)
		owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
		if body := owner.want(200, "GET", builderapi.Prefix+"/capabilities", nil, xrw); !strings.Contains(string(body), `"handoff"`) {
			t.Errorf("capabilities in handoff mode: %s", body)
		}
		owner.want(200, "GET", builderapi.Prefix+"/changes", nil, xrw)
		if code, body := owner.do("POST", builderapi.Prefix+"/changes", map[string]any{"request_id": "r1", "note": "n", "ops": []any{}}, xrw); code == 503 {
			t.Errorf("a change with builder_changes on did not reach the service: %d %s", code, body)
		}
	})
}

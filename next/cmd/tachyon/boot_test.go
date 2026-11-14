package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/scheduler"

	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// started boots the real stack (every module the manifest selects and the real handler chain) on
// a temp data directory and a free loopback port, and returns its base URL and the started app.
// Nothing is stubbed between the HTTP client and the database.
func started(t *testing.T, override string) (string, *module.App) {
	t.Helper()
	return startedIn(t, t.TempDir(), override)
}

// startedIn is started with a chosen data directory.
func startedIn(t *testing.T, dataDir, override string) (string, *module.App) {
	t.Helper()
	env := overrideEnv(t, dataDir, override)
	// First-run setup is offline: the owner exists before the server starts.
	createOwner(t, env)
	return serving(t, env)
}

// serving boots and serves on an existing data directory (the owner already created).
func serving(t *testing.T, env map[string]string) (string, *module.App) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	addr := make(chan net.Addr, 1)
	p := &tachyon{app: run.App, ready: func(a net.Addr) { addr <- a }}
	done := make(chan error, 1)
	go func() { done <- p.serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve ended with %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("serve did not stop on cancel")
		}
		_ = run.Stop(context.Background())
	})
	select {
	case a := <-addr:
		return "http://" + a.String(), run.App
	case err := <-done:
		t.Fatalf("serve returned before listening: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("serve never listened")
	}
	return "", nil
}

// overrideEnv writes the override file and returns the environment the boot hooks read. The config
// module reads SUBSTRATE_CONFIG from the process environment, so it is set there too.
func overrideEnv(t *testing.T, dataDir, override string) map[string]string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(file, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", file)
	return map[string]string{"TACHYON_DATA_DIR": dataDir, "SUBSTRATE_CONFIG": file}
}

const ownerPassword = "correct horse battery staple"

// createOwner runs `tachyon identity setup` against the data directory in env, as an operator does
// on the host before the first start (POST /api/setup is refused).
func createOwner(t *testing.T, env map[string]string) {
	t.Helper()
	args := []string{"identity", "setup", "--username", "owner", "--display-name", "Owner"}
	modules, prepare := plan(args)
	run, err := boot.StartWith(context.Background(), substrateApp, modules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, prepare))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	root := (&tachyon{app: run.App}).root(context.Background(), args)
	root.SetIn(strings.NewReader(ownerPassword + "\n"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("identity setup: %v\n%s", err, out.String())
	}
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

// do sends one request; headers may override Host.
func (c *client) do(method, path string, body any, headers map[string]string) (int, []byte) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (c *client) want(status int, method, path string, body any, headers map[string]string) []byte {
	c.t.Helper()
	got, out := c.do(method, path, body, headers)
	if got != status {
		c.t.Fatalf("%s %s: %d want %d: %s", method, path, got, status, out)
	}
	return out
}

// flatCode decodes the flat {code, message} error body and returns the code; it fails the test on
// any other shape.
func flatCode(t *testing.T, body []byte) string {
	t.Helper()
	var flat struct{ Code, Message string }
	if err := json.Unmarshal(body, &flat); err != nil || flat.Code == "" || flat.Message == "" {
		t.Fatalf("not a flat {code, message} body: %s (%v)", body, err)
	}
	return flat.Code
}

var xrw = map[string]string{"X-Requested-With": "tachyon-ui"}

const loopbackOverride = "httphost:\n  addr: \"127.0.0.1:0\"\n"

func TestAcceptance(t *testing.T) {
	base, _ := started(t, loopbackOverride)
	c := newClient(t, base)

	c.want(200, "GET", "/healthz", nil, nil)

	// Signed out: the guard refuses every /api/ route that is not public, in the flat body.
	if code := flatCode(t, c.want(401, "GET", "/api/x", nil, nil)); code != "unauthorized" {
		t.Fatalf("GET /api/x signed out: code %q", code)
	}
	c.want(401, "GET", "/api/identity/profile", nil, nil)
	// The plugin prefix is closed to everything not on the allowlist, before credentials matter.
	if code := flatCode(t, c.want(404, "GET", "/api/plugins/console/v1/tasks/T-1", nil, nil)); code != "not_found" {
		t.Fatalf("plugin path: code %q", code)
	}
	c.want(404, "POST", "/api/plugins/console/v1/messages", map[string]string{}, xrw)
	c.want(404, "GET", "/api/plugins/nope/v1/anything", nil, nil) // an unknown plugin id

	// First-run setup is offline: the owner was created before the server started, so setup is
	// closed, and over HTTP it is refused outright.
	if body := c.want(200, "GET", "/api/setup", nil, nil); !bytes.Contains(body, []byte(`"setup_required":false`)) {
		t.Fatalf("GET /api/setup = %s", body)
	}
	c.want(403, "POST", "/api/setup", map[string]string{"username": "x", "display_name": "X", "password": ownerPassword}, xrw)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	c.want(200, "GET", "/api/identity/profile", nil, nil)

	// Signed in as the owner (scope *): a route nobody declared is still refused, and the plugin
	// prefix still forwards nothing.
	if code := flatCode(t, c.want(403, "GET", "/api/x", nil, nil)); code != "route_not_mapped" {
		t.Fatalf("GET /api/x signed in: code %q", code)
	}
	// No catch-all for the owner either (the director's ruling: the allowlist is the fix for the old
	// open Torque fallback): an unknown console path, a Torque-shaped write and an unknown plugin id.
	c.want(404, "GET", "/api/plugins/console/v1/tasks/T-1", nil, nil)
	c.want(404, "POST", "/api/plugins/console/v1/messages", map[string]string{}, xrw)
	c.want(404, "DELETE", "/api/plugins/console/v1/anything", nil, xrw)
	c.want(404, "GET", "/api/plugins/nope/v1/anything", nil, nil)

	// Refusals: a cookie write without the CSRF header, a foreign Host.
	c.want(403, "POST", "/api/logout", nil, nil)
	c.want(403, "GET", "/healthz", nil, map[string]string{"Host": "evil.example.test"})
	c.want(403, "GET", "/api/identity/profile", nil, map[string]string{"Host": "evil.example.test"})

	c.want(204, "POST", "/api/logout", nil, xrw)
	c.want(401, "GET", "/api/identity/profile", nil, nil)
}

// Every route the running app serves under /api/ has a scope in the table the guard enforces:
// the framework modules' declared routes and Tachyon's own list. A route without one is refused
// before anything is served (internal/routes, TestUnscopedRouteFails).
func TestEveryServedRouteHasAScope(t *testing.T) {
	_, app := started(t, loopbackOverride)
	table := auth.ScopeTable(app)
	declared := module.Routes(app)
	if len(declared) == 0 {
		t.Fatal("no module routes: the check would prove nothing")
	}
	for _, r := range declared {
		method := r.Method
		if method == "*" {
			method = "GET"
		}
		path := strings.ReplaceAll(r.ScopePattern(), "{", "x")
		path = strings.ReplaceAll(strings.ReplaceAll(path, "}", ""), "...", "")
		// "" is authentication only (or, with Public, none); either way the route is mapped, which
		// is what the guard needs: an unmapped route is refused in enforce mode.
		if scope, known := table.ForRoute(method, path); !known || scope != r.Scope {
			t.Errorf("module route %s %s: scope %q known %v, want %q", r.Method, r.Path, scope, known, r.Scope)
		}
	}
	if err := routes.Check(appRoutes()); err != nil {
		t.Errorf("app routes: %v", err)
	}
	for _, r := range appRoutes() {
		if scope, known := table.ForRoute(r.Method, r.Pattern); !known || scope != routes.TableScope(r.Scope) {
			t.Errorf("app route %s %s: scope %q known %v, want %q", r.Method, r.Pattern, scope, known, r.Scope)
		}
	}
}

// The manifest's defaults turn every outbound feature off, and serving with them leaves the
// scheduler engine stopped.
func TestManifestDefaultsAreOff(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(substrateConfigJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	s, err := settings.Parse(cfg[settings.Key])
	if err != nil {
		t.Fatal(err)
	}
	feats, _ := cfg[settings.Key].(map[string]any)["features"].(map[string]any)
	for _, k := range settings.FeatureKeys() {
		if on, ok := s.Features.Enabled(k); !ok || on {
			t.Errorf("tachyon.features.%s = %v", k, on)
		}
		if _, listed := feats[k]; !listed {
			t.Errorf("tachyon.features.%s is not listed in the manifest (an operator reads the defaults there)", k)
		}
	}
	for k, u := range map[string]string{"tether": s.Upstreams.Tether.URL, "torque": s.Upstreams.Torque.URL, "tesseract": s.Upstreams.Tesseract.URL, "cerberus": s.Upstreams.Cerberus.URL} {
		if u != "" {
			t.Errorf("upstreams.%s has a default address %q; every upstream must be configured by the operator", k, u)
		}
	}

	_, app := started(t, loopbackOverride)
	if eng := scheduler.Engine(app); eng == nil {
		t.Fatal("the scheduler module is not started")
	}
	if cfg, _ := app.Config["scheduler"].(map[string]any); cfg["autostart"] != false {
		t.Fatalf("scheduler.autostart = %v, want false", cfg["autostart"])
	}
}

// An unknown or bad key in the tachyon block stops the start, before anything is created, and the
// error names the key.
func TestBadSettingsRefuseToStart(t *testing.T) {
	for override, want := range map[string]string{
		"tachyon:\n  allowed_host: [x.example.test]\n":            "tachyon.allowed_host",
		"tachyon:\n  features:\n    object_synk: true\n":          "tachyon.features.object_synk",
		"tachyon:\n  upstreams:\n    torque:\n      token: abc\n": "tachyon.upstreams.torque.token must be a secrets reference",
	} {
		data := filepath.Join(t.TempDir(), "data")
		env := overrideEnv(t, data, loopbackOverride+override)
		run, err := boot.StartWith(context.Background(), substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
		if err == nil {
			_ = run.Stop(context.Background())
			t.Errorf("%q: started", override)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one naming %s", override, err, want)
		}
		if _, statErr := os.Stat(data); statErr == nil {
			t.Errorf("%q: the data directory was created before the refusal", override)
		}
	}
}

// A data directory of the old build is refused and left exactly as it was (not opened as a
// database, not tightened, nothing added), wherever the configuration points into it: as the data
// directory with the database elsewhere, as the database's directory, as the signing key's, or as
// the cache's.
func TestOldDataDirectoryIsRefusedUntouched(t *testing.T) {
	for name, layout := range map[string]func(old, elsewhere string) (dataDir, override string){
		"the data directory": func(old, _ string) (string, string) { return old, loopbackOverride },
		"the data directory, database elsewhere": func(old, elsewhere string) (string, string) {
			return old, loopbackOverride + "database:\n  sqlite:\n    path: " + filepath.Join(elsewhere, "tachyon.db") + "\n"
		},
		"the database's directory": func(old, elsewhere string) (string, string) {
			return elsewhere, loopbackOverride + "database:\n  sqlite:\n    path: " + filepath.Join(old, "new.db") + "\n"
		},
		"the signing key's directory": func(old, elsewhere string) (string, string) {
			return elsewhere, loopbackOverride + "identity:\n  key_file: " + filepath.Join(old, "identity-signing.key") + "\n"
		},
		"the cache directory's parent": func(old, elsewhere string) (string, string) {
			return elsewhere, loopbackOverride + "tachyon:\n  cache_dir: " + filepath.Join(old, "cache") + "\n"
		},
	} {
		t.Run(name, func(t *testing.T) {
			old := filepath.Join(t.TempDir(), "old")
			if err := os.MkdirAll(old, 0o755); err != nil { //nolint:gosec // the old build's directory mode, on purpose
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "jobs.db"), []byte("old scheduler"), 0o600); err != nil {
				t.Fatal(err)
			}
			elsewhere := filepath.Join(t.TempDir(), "new")
			dataDir, override := layout(old, elsewhere)
			env := overrideEnv(t, dataDir, override)
			for _, args := range [][]string{nil, {"identity", "setup", "--username", "owner"}} {
				modules, prepare := plan(args)
				run, err := boot.StartWith(context.Background(), substrateApp, modules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, prepare))
				if err == nil {
					_ = run.Stop(context.Background())
					t.Fatalf("%v: started", args)
				}
				if !strings.Contains(err.Error(), "old Tachyon build") {
					t.Errorf("%v: error %v", args, err)
				}
			}
			if entries, _ := os.ReadDir(old); len(entries) != 1 {
				t.Errorf("the old directory changed: %v", entries)
			}
			if fi, _ := os.Stat(old); fi.Mode().Perm() != 0o755 {
				t.Errorf("the old directory's mode changed to %o", fi.Mode().Perm())
			}
		})
	}
}

// The manifest's relative defaults land in the data directory, private.
func TestDataPaths(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	base, _ := startedIn(t, data, loopbackOverride)
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for name, want := range map[string]os.FileMode{"tachyon.db": 0o600, "identity-signing.key": 0o600, "cache": os.ModeDir | 0o700} {
		fi, err := os.Stat(filepath.Join(data, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := fi.Mode() & (os.ModeDir | os.ModePerm); got != want {
			t.Errorf("%s: mode %v, want %v", name, got, want)
		}
	}
	if fi, err := os.Stat(data); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("data directory: %v, %v", err, fi)
	}
}

// The database module applies Tachyon's schema beside the framework's own tables, a second start on
// the same directory finds it current (and is not mistaken for the old layout, whose ledger table
// the schema keeps), and the store answers on its read pool.
func TestSchemaIsAppliedAndRestarts(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	createOwner(t, env)
	count := func(t *testing.T, app *module.App) (tables, ledger int) {
		t.Helper()
		st := store.From(app)
		if st == nil {
			t.Fatal("no store")
		}
		q := `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN
			('objects', 'approval_items', 'approval_decision_log', 'chat_turns', 'app_settings', 'tachyon_migrations', 'identity_users', 'gosched_schedules')`
		if err := st.Reader().QueryRowContext(context.Background(), q).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		if err := st.Reader().QueryRowContext(context.Background(), `SELECT count(*) FROM tachyon_migrations`).Scan(&ledger); err != nil {
			t.Fatal(err)
		}
		return tables, ledger
	}
	t.Run("first start", func(t *testing.T) {
		_, app := serving(t, env)
		if tables, ledger := count(t, app); tables != 8 || ledger != 0 {
			t.Fatalf("%d of 8 tables, %d ledger rows (the new build writes none)", tables, ledger)
		}
	})
	t.Run("second start", func(t *testing.T) {
		base, app := serving(t, env)
		if tables, _ := count(t, app); tables != 8 {
			t.Fatalf("%d of 8 tables", tables)
		}
		newClient(t, base).want(200, "GET", "/healthz", nil, nil)
	})
}

// migrate-from refuses a data directory that already holds anything, before it creates or changes
// a file there; and with an empty one it needs a source.
func TestMigrateFromTarget(t *testing.T) {
	args := []string{"migrate-from", "--source", t.TempDir()}
	if !migrating(args) || migrating([]string{"serve"}) {
		t.Fatal("migrating() misreads the command line")
	}
	modules, prepare := plan(args)

	used := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(used, 0o755); err != nil { //nolint:gosec // a mode the refusal must leave alone
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(used, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := overrideEnv(t, used, loopbackOverride)
	getenv := func(k string) string { return env[k] }
	run, err := boot.StartWith(context.Background(), substrateApp, modules, substrateConfigJSON, migrateOptions(getenv, bootOptions(getenv, prepare)))
	if err == nil {
		_ = run.Stop(context.Background())
		t.Fatal("started on a non-empty data directory")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(used); len(entries) != 1 {
		t.Fatalf("the refused directory changed: %v", entries)
	}
	if fi, _ := os.Stat(used); fi.Mode().Perm() != 0o755 {
		t.Fatalf("the refused directory's mode changed to %o", fi.Mode().Perm())
	}

	fresh := filepath.Join(t.TempDir(), "data")
	env = overrideEnv(t, fresh, loopbackOverride)
	getenv = func(k string) string { return env[k] }
	run, err = boot.StartWith(context.Background(), substrateApp, modules, substrateConfigJSON, migrateOptions(getenv, bootOptions(getenv, prepare)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	root := (&tachyon{app: run.App}).root(context.Background(), []string{"migrate-from"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--source is required") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(fresh, "migrate-from-report.json")); err != nil {
		t.Fatalf("no report beside the database: %v", err)
	}
}

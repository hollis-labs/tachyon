package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/scheduler"

	"github.com/hollis-labs/substrate/modules/identity"

	libsched "github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/dataverbs"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/objectsync"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
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

// testDBs is each data directory's database, so a test that boots twice on one data directory finds
// its data again on either dialect.
var testDBs sync.Map // data directory -> storetest.Database

// databaseFor is the database for a data directory: on SQLite the manifest's file inside it, on
// Postgres a fresh schema (storetest).
func databaseFor(t *testing.T, dataDir string) storetest.Database {
	t.Helper()
	if d, ok := testDBs.Load(dataDir); ok {
		return d.(storetest.Database)
	}
	d := storetest.NewAt(t, filepath.Join(dataDir, "tachyon.db"))
	testDBs.Store(dataDir, d)
	t.Cleanup(func() { testDBs.Delete(dataDir) })
	return d
}

// overrideEnv writes the override file and returns the environment the boot hooks read. The config
// module reads SUBSTRATE_CONFIG from the process environment, so it is set there too. On Postgres the
// override also points the database module at the data directory's schema, unless the test sets the
// database itself.
func overrideEnv(t *testing.T, dataDir, override string) map[string]string {
	t.Helper()
	storetest.SkipOnPostgres(t, "the served binary selects the scheduler module, which has no Postgres store yet (Substrate gap CW-20261115-0014)")
	if !strings.HasPrefix(override, "database:") && !strings.Contains(override, "\ndatabase:") {
		override += databaseFor(t, dataDir).OverrideYAML()
	}
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
	cfg, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		t.Fatal(err)
	}
	d := deps{access: access.New(store.From(app), identity.From(app), nil), local: dataverbs.NewLocal(objects.New(store.Wrap(store.From(app)))),
		planner: newPlanner(app, cfg, store.Wrap(store.From(app))), approvals: newApprovals(context.Background(), app, cfg, store.Wrap(store.From(app)))}
	rs := appRoutes(d)
	if len(rs) == 0 {
		t.Fatal("no app routes: the check would prove less than it says")
	}
	if err := routes.Check(rs); err != nil {
		t.Errorf("app routes: %v", err)
	}
	for _, r := range rs {
		path := r.Pattern
		if strings.HasSuffix(path, "/") {
			path += "x" // a subtree pattern covers the paths below it
		}
		if scope, known := table.ForRoute(r.Method, path); !known || scope != routes.TableScope(r.Scope) {
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

// runCmd runs one command line in-process on env's data directory, as the binary would.
func runCmd(t *testing.T, env map[string]string, stdin string, args ...string) (string, error) {
	t.Helper()
	modules, prepare := plan(args)
	run, err := boot.StartWith(context.Background(), substrateApp, modules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, prepare))
	if err != nil {
		return "", err
	}
	defer func() { _ = run.Stop(context.Background()) }()
	root := (&tachyon{app: run.App}).root(context.Background(), args)
	var out bytes.Buffer
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&out)
	err = root.Execute()
	return out.String(), err
}

// The agent data API end to end: only a key mapped to a service identity gets through; the owner's
// session, an unmapped key holding "*" and an unauthenticated call do not. `access issue` writes
// the key to a new 0600 file and prints no secret.
func TestAgentAPI(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	createOwner(t, env)
	keyFile := filepath.Join(t.TempDir(), "actions.key")
	out, err := runCmd(t, env, "", "access", "issue", "--name", "planner-reader", "--preset", "planner-read", "--ttl", "1h", "--out", keyFile)
	if err != nil {
		t.Fatalf("access issue: %v\n%s", err, out)
	}
	key, err := os.ReadFile(keyFile) //nolint:gosec // this test's temp file
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(keyFile); fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %o", fi.Mode().Perm())
	}
	if strings.Contains(out, strings.TrimSpace(string(key))) {
		t.Fatal("access issue printed the key")
	}
	star, err := runCmd(t, env, "", "identity", "key", "create", "--name", "star", "--scope", "*", "--stdout")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, env, "", "access", "issue", "--name", "x", "--verbs", "*", "--out", filepath.Join(t.TempDir(), "x.key")); err == nil {
		t.Error("access issue granted *")
	}

	base, _ := serving(t, env)
	c := newClient(t, base)
	bearer := func(k string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(k)}
	}
	body := c.want(200, "GET", "/api/agent/v1/data/providers", nil, bearer(string(key)))
	// Discovery lists the verbs this build serves that the credential allows (planner-read: the
	// object reads and planner.list), and nothing it does not grant.
	if !bytes.Contains(body, []byte(`"objects.get"`)) || !bytes.Contains(body, []byte(`"planner.list"`)) || bytes.Contains(body, []byte("planner.portfolio")) {
		t.Fatalf("discovery = %s", body)
	}
	if code := flatCode(t, c.want(403, "GET", "/api/agent/v1/data/providers", nil, bearer(star))); code != "credential_not_mapped" {
		t.Fatalf("unmapped * key: %s", code)
	}
	c.want(401, "GET", "/api/agent/v1/data/providers", nil, nil)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	if code := flatCode(t, c.want(403, "GET", "/api/agent/v1/data/providers", nil, nil)); code != "agent_required" {
		t.Fatalf("owner session: %s", code)
	}
}

// `access reissue` refuses before the owner exists, writes one 0600 key file per active old token
// and prints no secret, and refuses a second run.
func TestAccessReissueCommand(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	keys := filepath.Join(t.TempDir(), "keys")
	// The schema exists after a first start; old tokens arrive by migrate-from in real use, here by SQL.
	if out, err := runCmd(t, env, "", "access", "list"); err != nil {
		t.Fatalf("access list: %v\n%s", err, out)
	}
	db, err := sql.Open("sqlite", filepath.Join(data, "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO service_identities(id, name, created_at) VALUES ('11111111-0000-4000-8000-000000000001', 'reader', ?)`, []any{now}},
		{`INSERT INTO service_tokens(id, identity_id, token_hash, grants, created_at, expires_at) VALUES ('22222222-0000-4000-8000-000000000001', '11111111-0000-4000-8000-000000000001', 'h1', '{"verbs":["planner.list"],"kinds":["task"]}', ?, ?)`, []any{now - 100, now + 86400}},
		{`INSERT INTO service_tokens(id, identity_id, token_hash, grants, created_at, expires_at) VALUES ('22222222-0000-4000-8000-000000000002', '11111111-0000-4000-8000-000000000001', 'h2', '{"verbs":["planner.list"],"kinds":["task"]}', ?, ?)`, []any{now - 100, now - 10}},
	} {
		if _, err = db.Exec(q.sql, q.args...); err != nil {
			break
		}
	}
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if out, e := runCmd(t, env, "", "access", "reissue", "--out-dir", keys); e == nil || !strings.Contains(e.Error(), "no owner") {
		t.Fatalf("reissue before the owner: %v\n%s", e, out)
	}
	createOwner(t, env)
	out, err := runCmd(t, env, "", "access", "reissue", "--out-dir", keys)
	if err != nil {
		t.Fatalf("reissue: %v\n%s", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(keys, "*.key"))
	if len(files) != 1 || !strings.Contains(out, "reissued 1 credential(s)") || !strings.Contains(out, "allow-set equal true, expiry not extended true") {
		t.Fatalf("files %v, output:\n%s", files, out)
	}
	key, _ := os.ReadFile(files[0]) //nolint:gosec // this test's temp file
	if strings.Contains(out, strings.TrimSpace(string(key))) || strings.Contains(out, "key_") {
		t.Fatal("reissue printed a key")
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %o", fi.Mode().Perm())
	}
	if out, err := runCmd(t, env, "", "access", "reissue", "--out-dir", filepath.Join(t.TempDir(), "again")); err == nil || !strings.Contains(err.Error(), "already exist") {
		t.Fatalf("second reissue: %v\n%s", err, out)
	}
}

// Object sync runs on the scheduler module: a schedule in the main database under the old
// derivation's id is not fired while tachyon.features.object_sync is off; with it on, a fire is
// attempted under that same id and fails cleanly (the GitHub source has no credential, so nothing is
// fetched), and no schedule is added.
func TestObjectSyncOnScheduler(t *testing.T) {
	src, err := objectsync.NewGitHubRepositories("https://api.github.com", "example-org", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := objectsync.SourceID(src)
	data := filepath.Join(t.TempDir(), "data")
	gh := "tachyon:\n  upstreams:\n    github:\n      org: example-org\n"
	off := overrideEnv(t, data, loopbackOverride+gh)
	createOwner(t, off)

	count := func(t *testing.T, app *module.App, q string, args ...any) int {
		t.Helper()
		var n int
		if err := store.From(app).Reader().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Run("feature off", func(t *testing.T) {
		_, app := serving(t, off)
		st, _ := scheduler.Store(app).(*sqlstore.Store)
		err := st.CreateSchedule(context.Background(), libsched.Schedule{ID: id, JobType: objectsync.JobType, Enabled: true, Interval: 5 * time.Minute,
			NextRun: time.Now().UTC().Add(-time.Minute), Overlap: libsched.OverlapSkip, Misfire: libsched.MisfireRunOnce, KeepLastN: 100})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2500 * time.Millisecond) // more than two ticks of the default cadence
		if n := count(t, app, `SELECT count(*) FROM gosched_fires`); n != 0 {
			t.Fatalf("%d fire(s) with object_sync off", n)
		}
	})
	t.Run("feature on", func(t *testing.T) {
		on := overrideEnv(t, data, loopbackOverride+gh+"  features:\n    object_sync: true\n")
		_, app := serving(t, on)
		deadline := time.Now().Add(15 * time.Second)
		for count(t, app, `SELECT count(*) FROM gosched_fires WHERE schedule_id = ?`, id) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("no fire under the schedule's id")
			}
			time.Sleep(200 * time.Millisecond)
		}
		for count(t, app, `SELECT count(*) FROM object_sync_state WHERE source_id = ? AND problem <> ''`, id) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the failed scan was not recorded")
			}
			time.Sleep(200 * time.Millisecond)
		}
		if n := count(t, app, `SELECT count(*) FROM gosched_schedules`); n != 1 {
			t.Fatalf("%d schedules, want the one", n)
		}
	})
}

// `sync-objects --status` lists every schedule, configured in this process or not, without
// fetching; with no source configured the other actions refuse.
func TestSyncObjectsStatus(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	if out, err := runCmd(t, env, "", "sync-objects", "--status"); err != nil || !strings.Contains(out, `"schedules": []`) {
		t.Fatalf("status on an empty store: %v\n%s", err, out)
	}
	if _, err := runCmd(t, env, "", "sync-objects", "--enable"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("enable with no source: %v", err)
	}
	if _, err := runCmd(t, env, "", "sync-objects", "--status", "--disable"); err == nil {
		t.Fatal("conflicting options accepted")
	}
}

// The planner routes in the served binary: a signed-in owner reads and writes under both prefixes.
// A key holding both planner scopes still cannot write (planner writes need tachyon.operator, which a
// key never has). A read-only key is refused a write by scope, and a key without a planner scope is
// refused a read. Identity creates no person but the owner, so a signed-in non-operator person
// cannot be set up here; keys carry the operator refusal.
func TestPlannerRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "planner-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	readWrite := key("tachyon.planner.read", "tachyon.planner.write")
	readOnly := key("tachyon.planner.read")
	noPlanner := key("tachyon.objects.read")
	const console, legacy = "/api/plugins/console/v1", "/api/plugins/planner/v1"
	keys := newClient(t, base)
	refused := func(name string, h map[string]string, method, path, want string) {
		t.Helper()
		var body any
		if method != "GET" {
			body = map[string]any{}
		}
		if code := flatCode(t, keys.want(403, method, path, body, h)); code != want {
			t.Errorf("%s %s %s: %s, want %s", name, method, path, code, want)
		}
	}
	for _, prefix := range []string{console, legacy} {
		keys.want(200, "GET", prefix+"/planner/themes", nil, readWrite)
		keys.want(200, "GET", prefix+"/planner/themes", nil, readOnly)
		refused("read+write key", readWrite, "POST", prefix+"/planner/ideas", "operator_required")
		refused("read+write key", readWrite, "PATCH", prefix+"/planner/ideas/x", "operator_required")
		refused("read-only key", readOnly, "POST", prefix+"/planner/ideas", "scope_required")
		refused("no-planner key", noPlanner, "GET", prefix+"/planner/themes", "scope_required")
	}
	keys.want(200, "GET", console+"/planner/assignments", nil, readOnly)
	refused("read+write key", readWrite, "POST", console+"/planner/assignments", "operator_required")

	c := newClient(t, base)
	c.want(401, "GET", console+"/planner/themes", nil, nil)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	c.want(200, "GET", console+"/planner/themes", nil, nil)
	c.want(200, "GET", legacy+"/planner/themes", nil, nil)
	// The owner may write: the request reaches the planner (which refuses this empty body itself).
	if code, body := c.do("POST", console+"/planner/ideas", map[string]any{}, xrw); code == 401 || code == 403 {
		t.Fatalf("owner write refused: %d %s", code, body)
	}
}

// B4's console data routes answer in the served binary (pluginhost, which shadowed /api/plugins/,
// is not selected): a signed-in owner lists the providers and calls an object verb; a key, even
// with the objects scope, is refused.
func TestConsoleDataRoutesServed(t *testing.T) {
	base, app := started(t, loopbackOverride)
	k, _, err := identity.From(app).CreateKey(context.Background(), identity.KeyInput{Name: "console-test", Scopes: []string{"tachyon.objects.read"}})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, base)
	c.want(401, "GET", dataverbs.Prefix+"/providers", nil, nil)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	if body := c.want(200, "GET", dataverbs.Prefix+"/providers", nil, nil); !bytes.Contains(body, []byte(`"objects.list"`)) {
		t.Fatalf("providers = %s", body)
	}
	in, _ := json.Marshal(map[string]string{"kind": "task"})
	c.want(200, "GET", dataverbs.Prefix+"/providers/tachyon/verbs/objects.list?input="+url.QueryEscape(string(in)), nil, nil)
	keys := newClient(t, base)
	keys.want(403, "GET", dataverbs.Prefix+"/providers", nil, map[string]string{"Authorization": "Bearer " + k})
}

// The planner event stream ends when the server shuts down (SIGTERM), well within the grace period:
// serve returns nil and the client sees the stream close.
func TestPlannerStreamEndsOnShutdown(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	createOwner(t, env)
	ctx, cancel := context.WithCancel(context.Background())
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
	req, _ := http.NewRequest(http.MethodGet, base+"/api/plugins/planner/stream/events", nil)
	res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	first := make([]byte, 64)
	if _, err := res.Body.Read(first); err != nil {
		t.Fatalf("no first frame: %v", err)
	}
	begin := time.Now()
	cancel() // what SIGTERM does (signal.NotifyContext)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve ended with %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not return within the grace period with a stream open")
	}
	if _, err := io.ReadAll(res.Body); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Logf("stream close: %v", err)
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("shutdown took %s with a stream open", took)
	}
}

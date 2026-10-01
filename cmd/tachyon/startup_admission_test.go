package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/plugins"
)

// Real subprocesses exercise framing, rejected-child reaping, and startup exit.
// These helpers never contact a provider and are isolated from the live tree.
func TestMain(m *testing.M) {
	if dir := os.Getenv("TACHYON_TEST_PLUGIN"); dir != "" {
		fakeAdmissionPlugin(dir)
		os.Exit(0)
	}
	if os.Getenv("TACHYON_TEST_HOST") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeAdmissionPlugin(dir string) {
	_ = os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var req subprocess.RPCRequest
		if dec.Decode(&req) != nil {
			return
		}
		resp := subprocess.RPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{}`)}
		switch req.Method {
		case "plugin/init":
			resp.Result, _ = json.Marshal(subprocess.InitResult{ID: filepath.Base(dir), Name: filepath.Base(dir), Version: "test"})
		case "plugin/load":
			if _, err := os.Stat(filepath.Join(dir, "hang")); err == nil {
				for {
					time.Sleep(time.Hour)
				}
			}
		case "plugin/unload":
			_ = os.WriteFile(filepath.Join(dir, "unload-started"), nil, 0600)
			if _, err := os.Stat(filepath.Join(dir, "hang-unload")); err == nil {
				for {
					time.Sleep(time.Hour)
				}
			}
			_ = os.WriteFile(filepath.Join(dir, "unload-ack"), nil, 0600)
		case "command/execute":
			var params subprocess.CommandExecParams
			paramsRaw, _ := json.Marshal(req.Params)
			_ = json.Unmarshal(paramsRaw, &params)
			if params.Name != "plugin_capabilities" {
				if result, e := os.ReadFile(filepath.Join(dir, "verb-result")); e == nil {
					resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "message", Content: string(result)})
					_ = enc.Encode(resp)
					continue
				}
			}
			content, _ := os.ReadFile(filepath.Join(dir, "declaration"))
			switch string(content) {
			case "rpc-legacy":
				resp.Error = &subprocess.RPCError{Code: -32601, Message: "method not found"}
			case "command-legacy":
				resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "error", Content: "unknown command"})
			case "wrong-rpc-shape":
				_ = enc.Encode(map[string]string{"error": "boom"})
				continue
			case "wrong-command-shape":
				resp.Result = json.RawMessage(`[]`)
			case "wrong-action":
				resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "navigate", Content: `{"modules":["agent"]}`})
			default:
				resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "message", Content: string(content)})
			}
		}
		if enc.Encode(resp) != nil {
			return
		}
	}
}

func startupFixture(t *testing.T) (string, *plugins.Manager, *slog.Logger) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("TACHYON_DATA_DIR", filepath.Join(root, "data"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := plugins.NewManager(logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := mgr.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return root, mgr, logger
}

func fixturePlugin(t *testing.T, root, id, declaration string, manifest bool) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if manifest {
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("id: "+id), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf("#!/bin/sh\nexport TACHYON_TEST_PLUGIN=%s\nexec %s\n", quote(dir), quote(exe))
	if err := os.WriteFile(filepath.Join(dir, id), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "declaration"), []byte(declaration), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requireReaped(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("child %d not reaped: %v", pid, err)
		}
	}
}

func TestStartupAdmissionRollback(t *testing.T) {
	cases := map[string]string{
		"null-declaration": `null`,
		"syntax":           `{`,
		"effect":           `{"modules":["bad"],"verbs":{"bad_list":{"effect":"unsafe"}}}`,
		"prefix":           `{"modules":["bad"],"verbs":{"other_list":{"effect":"reads"}}}`,
		"verb-collision":   `{"modules":["agent_list"],"verbs":{"agent_list_get":{"effect":"reads"}}}`,
		"collision":        `{"modules":["agent"]}`,
		"nav":              `{"modules":["bad"],"nav":{"items":[{"id":"bad","label":"Bad","group":"absent","route":"/bad"}]}}`,
		"settings":         `{"modules":["bad"],"settings":{"fields":[{"key":"enabled","label":"Enabled","type":"boolean","default":"yes"}]}}`,
		"rpc-shape":        "wrong-rpc-shape",
		"command-shape":    "wrong-command-shape",
		"action":           "wrong-action",
	}
	for name, declaration := range cases {
		t.Run(name, func(t *testing.T) {
			root, mgr, logger := startupFixture(t)
			first := fixturePlugin(t, root, "a-good", `{"modules":["agent"],"verbs":{"agent_list_get":{"effect":"reads"}}}`, true)
			rejected := fixturePlugin(t, root, "b-bad", declaration, true)
			fixturePlugin(t, root, "c-never", `{"modules":["config"]}`, true)
			if err := loadStartupPlugins(context.Background(), mgr, root, logger); err == nil {
				t.Fatal("invalid startup admitted")
			}
			if len(mgr.BuildRegistry().Plugins) != 0 || len(mgr.AllCapabilities()) != 0 {
				t.Fatal("partial catalog survived rollback")
			}
			requireReaped(t, first, rejected)
			if _, err := os.Stat(filepath.Join(root, "c-never", "pid")); !os.IsNotExist(err) {
				t.Fatalf("later child started: %v", err)
			}
		})
	}
}

func TestStartupEightPluginsAndCompatibility(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	fixturePlugin(t, root, "hello", "rpc-legacy", false)
	fixturePlugin(t, root, "legacy", "command-legacy", true)
	for _, module := range []string{"agent", "launch", "session", "observe", "work", "service", "scm", "config"} {
		// Cross-plugin nav IDs are soft collisions; alphabetical first owner wins.
		caps := fmt.Sprintf(`{"modules":[%q],"nav":{"groups":[{"id":"shared","label":%q}]}}`, module, module)
		fixturePlugin(t, root, module+"-ops", caps, true)
	}
	if err := os.MkdirAll(filepath.Join(root, "unbuilt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unbuilt", "plugin.yaml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err != nil {
		t.Fatal(err)
	}
	if len(mgr.AllCapabilities()) != 8 || len(mgr.BuildRegistry().Plugins) != 10 {
		t.Fatalf("incomplete admission: %v", mgr.BuildRegistry().Plugins)
	}
	for module := range mgr.AllCapabilities() {
		if mgr.ModuleOwner(module) != module+"-ops" {
			t.Fatalf("wrong owner: %s", module)
		}
	}
	nav := mgr.MergedNav()
	if len(nav.Groups) != 1 || nav.Groups[0].Label != "agent" {
		t.Fatalf("first-loaded nav lost: %+v", nav)
	}
	// A malformed restart affects that plugin alone, never repeats startup rollback.
	if err := os.WriteFile(filepath.Join(root, "agent-ops", "declaration"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RestartPlugin("agent-ops"); err == nil {
		t.Fatal("bad restart admitted")
	}
	if mgr.ModuleOwner("agent") != "" || mgr.ModuleOwner("config") != "config-ops" {
		t.Fatal("restart damaged other owners")
	}
}

func TestStartupHungLoadCleanup(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	first := fixturePlugin(t, root, "a-good", `{"modules":["agent"],"verbs":{"agent_list_get":{"effect":"reads"}}}`, true)
	hung := fixturePlugin(t, root, "b-hung", `{"modules":["work"]}`, true)
	if err := os.WriteFile(filepath.Join(hung, "hang"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := loadStartupPlugins(ctx, mgr, root, logger); err == nil {
		t.Fatal("hung startup admitted")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cleanup exceeded test deadline")
	}
	requireReaped(t, first, hung)
}

func TestHostRefusesHTTPOnAdmissionFailure(t *testing.T) {
	root, _, _ := startupFixture(t)
	children := filepath.Join(root, "plugins")
	first := fixturePlugin(t, children, "a-good", `{"modules":["agent"]}`, true)
	rejected := fixturePlugin(t, children, "b-bad", `{"modules":["agent"]}`, true)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TACHYON_TEST_HOST=1", "TACHYON_ADDR="+addr)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("startup did not exit promptly: %s", output)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("host exit=%v output=%s", err, output)
	}
	refusals := 0
	for _, line := range strings.Split(string(output), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["msg"] != "plugin startup refused" {
			continue
		}
		refusals++
		if record["level"] != "ERROR" || record["plugin_id"] != "b-bad" || record["path"] != filepath.Join("plugins", "b-bad", "b-bad") || record["reason_class"] != "collision" || !strings.Contains(fmt.Sprint(record["error"]), "already owned") {
			t.Fatalf("unclear startup refusal: %+v", record)
		}
	}
	if refusals != 1 {
		t.Fatalf("want one refusal line, got %d: %s", refusals, output)
	}
	if strings.Contains(string(output), "Tachyon listening") {
		t.Fatalf("HTTP startup reached before admission: %s", output)
	}
	requireReaped(t, first, rejected)
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("failed startup left a listener")
	}
}

func TestStartupOptionalAbsences(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	if err := loadStartupPlugins(context.Background(), mgr, filepath.Join(root, "missing"), logger); err != nil {
		t.Fatal(err)
	}
	dir := fixturePlugin(t, root, "unbuilt", `{"modules":["agent"]}`, true)
	if err := os.Chmod(filepath.Join(dir, "unbuilt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err != nil {
		t.Fatal(err)
	}
	if len(mgr.BuildRegistry().Plugins) != 0 {
		t.Fatal("optional absence registered")
	}
}

func TestStartupAuthoredValidationRollback(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	first := fixturePlugin(t, root, "a-good", `{"modules":["agent"],"verbs":{"agent_list_get":{"effect":"reads"}}}`, true)
	rejected := fixturePlugin(t, root, "b-bad", `{"modules":["work"]}`, true)
	if err := os.WriteFile(filepath.Join(rejected, "capabilities.json"), []byte(`{"modules":["work"],"settings":{"fields":[{"key":"url","label":"URL","type":"unknown"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err == nil {
		t.Fatal("invalid authored schema admitted")
	}
	requireReaped(t, first)
	if _, err := os.Stat(filepath.Join(rejected, "pid")); !os.IsNotExist(err) {
		t.Fatalf("invalid authored schema spawned: %v", err)
	}
}

func TestStartupRollbackBoundsHungUnloads(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	first := fixturePlugin(t, root, "a-good", `{"modules":["agent"]}`, true)
	second := fixturePlugin(t, root, "b-good", `{"modules":["work"]}`, true)
	rejected := fixturePlugin(t, root, "c-bad", `{"modules":["agent"]}`, true)
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, "hang-unload"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err == nil {
		t.Fatal("collision admitted")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("rollback exceeded shared 5s grace: %s", elapsed)
	}
	requireReaped(t, first, second, rejected)
	if len(mgr.BuildRegistry().Plugins) != 0 {
		t.Fatal("hung unload left registrations")
	}
}

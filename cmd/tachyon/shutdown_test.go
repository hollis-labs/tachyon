package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type shutdownFunc func(context.Context) error

func (f shutdownFunc) Shutdown(ctx context.Context) error { return f(ctx) }

func waitShutdown(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func requireListenerClosed(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("shutdown still accepting connections")
	}
}

func TestShutdownDrainsHTTPBeforePlugins(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "drained")
	}))
	defer srv.Close()
	response := make(chan string, 1)
	go func() {
		resp, err := srv.Client().Get(srv.URL)
		if err != nil {
			response <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		response <- string(b)
	}()
	<-entered
	plugins := make(chan struct{})
	done := make(chan struct{})
	go func() {
		shutdownHost(srv.Config, shutdownFunc(func(ctx context.Context) error {
			if _, deadline := ctx.Deadline(); deadline || ctx.Err() != nil {
				t.Error("HTTP budget leaked into plugins")
			}
			close(plugins)
			return nil
		}), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
		close(done)
	}()
	// Shutdown closes listeners immediately but waits for this admitted request.
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), 20*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("listener did not close")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-plugins:
		close(release)
		t.Fatal("plugins unloaded during active HTTP")
	default:
	}
	close(release)
	waitShutdown(t, done)
	if got := <-response; got != "drained" {
		t.Fatalf("HTTP did not drain: %s", got)
	}
	select {
	case <-plugins:
	default:
		t.Fatal("plugins not unloaded")
	}
}

func TestHTTPShutdownTimeoutForceClosesAndKeepsPluginBudget(t *testing.T) {
	entered, closed := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(closed) }))
	defer srv.Close()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		resp, err := srv.Client().Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	var logs bytes.Buffer
	called := false
	shutdownHost(srv.Config, shutdownFunc(func(ctx context.Context) error {
		called = true
		if _, deadline := ctx.Deadline(); deadline || ctx.Err() != nil {
			t.Error("expired HTTP context used for plugins")
		}
		return nil
	}), slog.New(slog.NewJSONHandler(&logs, nil)), 40*time.Millisecond)
	if !called {
		t.Fatal("HTTP timeout skipped plugins")
	}
	waitShutdown(t, closed)
	waitShutdown(t, requestDone)
	requireListenerClosed(t, srv.Listener.Addr().String())
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatal(err)
	}
	if record["stage"] != "http_drain" || record["timed_out"] != true {
		t.Fatalf("missing stage timeout diagnostic: %s", logs.Bytes())
	}
}

func waitFixtureEvent(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture never reached %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHostSignalShutdownGraceAndHungUnload(t *testing.T) {
	for _, hung := range []bool{false, true} {
		name := "graceful"
		if hung {
			name = "hung-unload"
		}
		t.Run(name, func(t *testing.T) {
			root, _, _ := startupFixture(t)
			children := filepath.Join(root, "plugins")
			first := fixturePlugin(t, children, "a-first", `{"modules":["agent"]}`, true)
			second := fixturePlugin(t, children, "b-second", `{"modules":["work"]}`, true)
			if hung {
				if err := os.WriteFile(filepath.Join(first, "hang-unload"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
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
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "TACHYON_TEST_HOST=1", "TACHYON_ADDR="+addr)
			var logs bytes.Buffer
			cmd.Stdout = &logs
			cmd.Stderr = &logs
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait(); close(done) }()
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("helper did not exit")
				}
			})
			client := &http.Client{Timeout: 100 * time.Millisecond}
			defer client.CloseIdleConnections()
			deadline := time.Now().Add(3 * time.Second)
			for {
				resp, err := client.Get("http://" + addr + "/api/plugins/registry")
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("host did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			waitFixtureEvent(t, filepath.Join(first, "unload-started"))
			requireListenerClosed(t, addr) // Even while the first unload is hung.
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("signal stop did not exit 0: %v; %s", err, logs.Bytes())
				}
			case <-ctx.Done():
				t.Fatal("host exceeded shutdown budgets")
			}
			requireReaped(t, first, second)
			waitFixtureEvent(t, filepath.Join(second, "unload-ack"))
			var firstGraceful, secondGraceful, timeout bool
			for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
				var record map[string]any
				if json.Unmarshal(line, &record) != nil || record["stage"] != "plugin_unload" {
					continue
				}
				if record["id"] == "a-first" {
					firstGraceful = record["graceful"] == true
					timeout = record["timed_out"] == true
				}
				if record["id"] == "b-second" {
					secondGraceful = record["graceful"] == true
				}
			}
			if !secondGraceful || (!hung && !firstGraceful) || (hung && !timeout) {
				t.Fatalf("wrong unload budgets/diagnostics: %s", logs.Bytes())
			}
		})
	}
}

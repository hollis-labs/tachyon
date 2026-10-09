package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/plugins"
)

// Exercise the real SDK Serve loop and host serial wire with a shorter test budget.
type deadlinePlugin struct{ plugin }

func (p *deadlinePlugin) Init(ctx context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	result, err := p.plugin.Init(ctx, params)
	if err == nil {
		p.adapter.(*TetherAdapter).submitTimeout = 100 * time.Millisecond
	}
	return result, err
}
func TestDeadlinePluginProcess(t *testing.T) {
	if os.Getenv("TACHYON_DEADLINE_TEST_CHILD") != "1" {
		return
	}
	if err := subprocess.Serve(&deadlinePlugin{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestStalledSubmitReleasesCommandAndRestart(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions/qa/turn" {
			entered <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"qa","state":"running"}`))
	}))
	defer srv.Close()
	defer close(release)
	t.Setenv("TETHER_ADDR", srv.URL)
	t.Setenv("TACHYON_DEADLINE_TEST_CHILD", "1")
	t.Setenv("TACHYON_DATA_DIR", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// LoadPlugin takes a path without arguments; exec replaces the launcher shell.
	path := filepath.Join(t.TempDir(), "session-ops")
	launcher := "#!/bin/sh\nexec '" + executable + "' -test.run=^TestDeadlinePluginProcess$\n"
	if err := os.WriteFile(path, []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := plugins.NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := manager.LoadPlugin(lifetime, path); err != nil {
		t.Fatal(err)
	}
	// Canceling the lifetime is also an escape hatch if a regression wedges callMu.
	defer manager.Shutdown(context.Background())
	run := func(verb string, payload string) contract.ResultEnvelope {
		t.Helper()
		done := make(chan struct{})
		var raw json.RawMessage
		var callErr error
		go func() {
			raw, callErr = manager.InvokeVerb(context.Background(), verb, json.RawMessage(payload))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatalf("%s wedged serial wire", verb)
		}
		if callErr != nil {
			t.Fatal(callErr)
		}
		var env contract.ResultEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	env := run("session_submit", `{"id":"qa","text":"mock turn"}`)
	select {
	case <-entered:
	default:
		t.Fatal("turn was not attempted")
	}
	if env.Status != contract.StatusError || env.Error.Code != "timeout" {
		t.Fatalf("submit result: %+v", env)
	}
	if env := run("session_read", `{"id":"qa"}`); env.Status != contract.StatusOK {
		t.Fatalf("read: %+v", env)
	}
	restarted := make(chan error, 1)
	go func() { restarted <- manager.RestartPlugin("session-ops") }()
	select {
	case err := <-restarted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("restart wedged")
	}
	if env := run("session_read", `{"id":"qa"}`); env.Status != contract.StatusOK {
		t.Fatalf("read after restart: %+v", env)
	}
}

func TestSubmitHonorsEarlierCallerDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	a, err := NewTetherAdapter(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := a.Submit(ctx, "qa", "mock turn"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not preserved: %v", err)
	}
}

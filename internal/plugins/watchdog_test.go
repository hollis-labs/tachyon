package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

func watchdogManager() *Manager { return NewManager(slog.New(slog.NewTextHandler(io.Discard, nil))) }

func awaitError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not unblock")
		return nil
	}
}

func awaitCondition(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

// This process either never reads stdin, or consumes one write and never
// responds. Counters detect replay of writes; pipes exercise actual blocking I/O.
func stalledProcess(t *testing.T, stage string) (*pluginProcess, <-chan struct{}, *atomic.Int32) {
	t.Helper()
	requests, input := io.Pipe()
	output, responses := io.Pipe()
	t.Cleanup(func() { input.Close(); output.Close(); requests.Close(); responses.Close() })
	started := make(chan struct{})
	writes := &atomic.Int32{}
	if stage == "decode" {
		go func() {
			var req subprocess.RPCRequest
			if json.NewDecoder(requests).Decode(&req) == nil {
				writes.Add(1)
				close(started)
			}
		}()
	} else {
		close(started)
	}
	return &pluginProcess{id: "hung", stdin: input, stdout: output, stdoutDec: json.NewDecoder(output), callTimeout: 80 * time.Millisecond}, started, writes
}

func registerStalled(m *Manager, proc *pluginProcess) {
	caps := declarationFor("hung", "Hung")
	proc.capabilities = &caps
	proc.binaryPath = "fake"
	m.plugins[proc.id] = proc
	m.modules["hung"] = proc.id
	m.loadOrder = append(m.loadOrder, proc.id)
	m.navGroups["shared"] = proc.id
	m.navItems["hung_list"] = proc.id
}

func TestWatchdogBoundsEncodeAndDecodeAndUnloadsOwnership(t *testing.T) {
	for _, stage := range []string{"encode", "decode"} {
		t.Run(stage, func(t *testing.T) {
			m := watchdogManager()
			proc, started, writes := stalledProcess(t, stage)
			registerStalled(m, proc)
			other := fakeProcess(t, "other", "declared", declarationFor("other", "Other"))
			if err := m.initializePlugin(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := m.CallPlugin(context.Background(), "hung", "command/execute", subprocess.CommandExecParams{Name: "hung_write", Args: "secret payload"})
				done <- err
			}()
			<-started
			if _, err := m.InvokeVerb(context.Background(), "other_list", nil); err != nil {
				t.Fatalf("other plugin blocked: %v", err)
			}
			if err := awaitError(t, done); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "completion unknown") {
				t.Fatalf("deadline: %v", err)
			}
			if _, err := m.CallPlugin(context.Background(), "hung", "command/execute", nil); err == nil {
				t.Fatal("dead pipe reused")
			}
			awaitCondition(t, func() bool { return m.ModuleOwner("hung") == "" })
			if nav := m.MergedNav(); len(nav.Items) != 1 || nav.Groups[0].Label != "Other" {
				t.Fatalf("stale ownership: %+v", nav)
			}
			if len(m.BuildRegistry().Plugins) != 1 || len(m.SettingsTargets()) != 1 {
				t.Fatal("stale registry/settings")
			}
			if stage == "decode" && writes.Load() != 1 {
				t.Fatalf("write retried: %d", writes.Load())
			}
			if _, err := m.InvokeVerb(context.Background(), "other_list", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueuedCancellationDoesNotKillActiveProcess(t *testing.T) {
	proc := fakeProcess(t, "normal", "declared")
	proc.callMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := callProcess(ctx, proc, "plugin/load", nil)
	if !errors.Is(err, context.DeadlineExceeded) || proc.dead.Load() {
		t.Fatalf("queue cancellation killed process: %v", err)
	}
	proc.callMu.Unlock()
	if _, err := callProcess(context.Background(), proc, "plugin/load", nil); err != nil {
		t.Fatal(err)
	}
	// Explicit cancellation before acquiring a free lock must not write either.
	cancelCtx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := callProcess(cancelCtx, proc, "command/execute", nil); !errors.Is(err, context.Canceled) || proc.dead.Load() {
		t.Fatalf("cancelled request sent: %v", err)
	}
}

func TestWatchdogRetirementCannotRemoveReplacement(t *testing.T) {
	m := watchdogManager()
	old, _, _ := stalledProcess(t, "encode")
	registerStalled(m, old)
	// Hold lifecycle ownership so retirement arrives only after replacement.
	m.lifecycleMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := m.CallPlugin(context.Background(), old.id, "command/execute", nil); done <- err }()
	if err := awaitError(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	m.detachProcess(old)
	replacement := fakeProcess(t, "hung", "declared", declarationFor("hung", "Replacement"))
	if err := m.initializePlugin(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	m.lifecycleMu.Unlock()
	// Joining retirement through its observable stopped flag then lifecycle lock
	// ensures the delayed cleanup has had a chance to run.
	awaitCondition(t, func() bool { old.callMu.Lock(); defer old.callMu.Unlock(); return old.stopped })
	m.lifecycleMu.Lock()
	m.lifecycleMu.Unlock()
	interruptProcess(old) // even another late interruption targets only old pipes
	if _, err := m.InvokeVerb(context.Background(), "hung_list", nil); err != nil {
		t.Fatalf("replacement killed: %v", err)
	}
	if m.ModuleOwner("hung") != "hung" || m.MergedNav().Groups[0].Label != "Replacement" {
		t.Fatal("replacement claims removed")
	}
}

func TestWatchdogConcurrentRestartAndShutdown(t *testing.T) {
	m := watchdogManager()
	proc, started, _ := stalledProcess(t, "decode")
	registerStalled(m, proc)
	spawnEntered, releaseSpawn := make(chan struct{}), make(chan struct{})
	m.spawn = func(context.Context, string) (*pluginProcess, error) {
		close(spawnEntered)
		<-releaseSpawn
		return fakeProcess(t, "hung", "declared", declarationFor("hung", "Replacement")), nil
	}
	call := make(chan error, 1)
	go func() { _, err := m.CallPlugin(context.Background(), "hung", "command/execute", nil); call <- err }()
	<-started
	restart := make(chan error, 1)
	go func() { restart <- m.RestartPlugin("hung") }()
	// Spawn happens under restart's lifecycle lock. Queue shutdown while it
	// is held, then allow registration to finish so shutdown removes replacement.
	select {
	case <-spawnEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not reach spawn")
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- m.Shutdown(context.Background()) }()
	close(releaseSpawn)
	_ = awaitError(t, call)
	if err := awaitError(t, restart); err != nil && !errors.Is(err, ErrPluginNotFound) {
		t.Fatal(err)
	}
	if err := awaitError(t, shutdown); err != nil {
		t.Fatal(err)
	}
	if len(m.AllCapabilities()) != 0 || len(m.BuildRegistry().Plugins) != 0 || len(m.MergedNav().Items) != 0 {
		t.Fatal("shutdown retained claims")
	}
}

func TestShutdownDeadlineClosesActivePipesAndLifecycleWaitCancels(t *testing.T) {
	m := watchdogManager()
	proc, started, _ := stalledProcess(t, "decode")
	proc.callTimeout = time.Minute
	registerStalled(m, proc)
	done := make(chan error, 1)
	go func() { _, err := m.CallPlugin(context.Background(), "hung", "command/execute", nil); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := awaitError(t, done); err == nil {
		t.Fatal("active call succeeded on stopped pipe")
	}
	if m.ModuleOwner("hung") != "" {
		t.Fatal("shutdown did not detach")
	}
	m.lifecycleMu.Lock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if err := m.LoadPlugin(ctx2, "must-not-spawn"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := m.Shutdown(ctx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	m.lifecycleMu.Unlock()
}

func TestWatchdogFakeSubprocess(t *testing.T) {
	stage := os.Getenv("TACHYON_WATCHDOG_HELPER")
	if stage == "" {
		return
	}
	_ = os.WriteFile(os.Getenv("TACHYON_WATCHDOG_PID"), []byte(strconv.Itoa(os.Getpid())), 0600)
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var req subprocess.RPCRequest
		if dec.Decode(&req) != nil {
			os.Exit(0)
		}
		if req.Method == stage || stage == "discovery" && req.Method == "command/execute" {
			for {
				time.Sleep(time.Hour)
			}
		}
		var result any = map[string]any{}
		switch req.Method {
		case "plugin/init":
			result = subprocess.InitResult{ID: "helper", Name: "Fake helper"}
		case "command/execute":
			result = subprocess.CommandExecResult{Action: "error", Content: "legacy"}
		}
		raw, _ := json.Marshal(result)
		if enc.Encode(subprocess.RPCResponse{JSONRPC: "2.0", Result: raw}) != nil {
			os.Exit(0)
		}
	}
}

func helperBinary(t *testing.T, stage string) (string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("TACHYON_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("TACHYON_WATCHDOG_HELPER", stage)
	pidPath := filepath.Join(root, "pid")
	t.Setenv("TACHYON_WATCHDOG_PID", pidPath)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "helper")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestWatchdogFakeSubprocess$\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path, pidPath
}

func TestLifecycleHungSubprocessesAreReaped(t *testing.T) {
	for _, stage := range []string{"plugin/init", "plugin/load", "discovery", "plugin/unload"} {
		t.Run(stage, func(t *testing.T) {
			path, pidPath := helperBinary(t, stage)
			m := watchdogManager()
			if stage == "plugin/unload" {
				if err := m.LoadPlugin(context.Background(), path); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- m.Shutdown(ctx) }()
				if err := awaitError(t, done); err != nil {
					t.Fatal(err)
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- m.LoadPlugin(ctx, path) }()
				if err := awaitError(t, done); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("hung startup admitted: %v", err)
				}
			}
			raw, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("helper not reaped: %v", err)
			}
			if len(m.BuildRegistry().Plugins) != 0 || len(m.AllCapabilities()) != 0 {
				t.Fatal("hung lifecycle registered")
			}
		})
	}
}

func TestSuccessfulCallsDisarmWatchdogAndStaySerial(t *testing.T) {
	m := watchdogManager()
	proc := fakeProcess(t, "normal", "declared")
	if err := m.initializePlugin(context.Background(), proc); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 16)
	for i := 0; i < cap(done); i++ {
		go func(i int) {
			ctx, cancel := context.WithCancel(context.Background())
			payload := json.RawMessage(strconv.Quote(strconv.Itoa(i)))
			raw, err := m.InvokeVerb(ctx, "agent_list", payload)
			cancel() // a successful call's watchdog must already be disarmed
			if err == nil {
				var envelope struct {
					Data struct {
						Payload string `json:"payload"`
					} `json:"data"`
				}
				err = json.Unmarshal(raw, &envelope)
				if err == nil && envelope.Data.Payload != string(payload) {
					err = errors.New("responses interleaved")
				}
			}
			done <- err
		}(i)
	}
	for i := 0; i < cap(done); i++ {
		if err := awaitError(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if proc.dead.Load() {
		t.Fatal("late watchdog stopped healthy plugin")
	}
	if _, err := m.InvokeVerb(context.Background(), "agent_list", nil); err != nil {
		t.Fatal(err)
	}
}

func TestTransportFailureRetiresRatherThanReusesStream(t *testing.T) {
	for _, response := range []string{"not JSON", ""} {
		t.Run(strconv.Quote(response), func(t *testing.T) {
			proc, _, _ := stalledProcess(t, "encode")
			// Read the first request and close output after a malformed/empty reply.
			// The writer end is retained separately for this deterministic failure.
			requests, input := io.Pipe()
			output, responses := io.Pipe()
			proc.stdin.Close()
			proc.stdout.Close()
			proc.stdin, proc.stdout, proc.stdoutDec = input, output, json.NewDecoder(output)
			t.Cleanup(func() { requests.Close(); responses.Close(); input.Close(); output.Close() })
			go func() {
				var req subprocess.RPCRequest
				_ = json.NewDecoder(requests).Decode(&req)
				_, _ = io.WriteString(responses, response)
				responses.Close()
			}()
			m := watchdogManager()
			registerStalled(m, proc)
			if _, err := m.CallPlugin(context.Background(), "hung", "command/execute", nil); err == nil {
				t.Fatal("bad response succeeded")
			}
			if _, err := m.CallPlugin(context.Background(), "hung", "command/execute", nil); err == nil {
				t.Fatal("broken stream reused")
			}
			awaitCondition(t, func() bool { return m.ModuleOwner("hung") == "" })
		})
	}
}

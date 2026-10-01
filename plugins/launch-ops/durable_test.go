package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	tether "github.com/hollis-labs/go-tether-client"
	"github.com/hollis-labs/plugin-sdk/subprocess"
)

func reopenStore(t *testing.T, path string) *LaunchStore {
	t.Helper()
	s, err := OpenLaunchStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNaniteLaunchesSurviveRestart(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "launches.db")
	s := reopenStore(t, path)
	a := NewNaniteLaunchAdapter(srv.URL, s)
	ctx := context.Background()
	prepared, err := a.Prepare(ctx, PrepareRequest{AgentID: "test-agent", Config: map[string]any{"nested": map[string]any{"value": "original"}}})
	if err != nil {
		t.Fatal(err)
	}
	running, err := a.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Execute(ctx, ExecuteRequest{LaunchID: running.ID}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := a.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Cancel(ctx, CancelRequest{LaunchID: cancelled.ID}); err != nil {
		t.Fatal(err)
	}
	// Mutating a returned nested config must not mutate durable intent.
	prepared.Config["nested"].(map[string]any)["value"] = "changed"
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = reopenStore(t, path)
	a = NewNaniteLaunchAdapter(srv.URL, s)
	for id, state := range map[string]LaunchState{prepared.ID: LaunchStatePrepared, running.ID: LaunchStateRunning, cancelled.ID: LaunchStateCancelled} {
		l, err := a.Read(ctx, ReadRequest{LaunchID: id})
		if err != nil {
			t.Fatal(err)
		}
		if l.State != state {
			t.Fatalf("%s state %s", id, l.State)
		}
		if id == running.ID && l.SessionID != "session-001" {
			t.Fatal("running session ID lost")
		}
		if id == prepared.ID && l.Config["nested"].(map[string]any)["value"] != "original" {
			t.Fatal("intent mutated")
		}
	}
	if _, err = a.Execute(ctx, ExecuteRequest{LaunchID: prepared.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Execute(ctx, ExecuteRequest{LaunchID: running.ID}); err == nil {
		t.Fatal("running launch re-executed")
	}
	all, err := a.List(ctx, ListRequest{})
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %+v %v", all, err)
	}
}

func TestNaniteInterruptedExecuteNotReplayed(t *testing.T) {
	s := testStore(t)
	if err := s.Create(context.Background(), &Launch{ID: "interrupted", Backend: "nanite", State: LaunchStateExecuting}); err != nil {
		t.Fatal(err)
	}
	a := NewNaniteLaunchAdapter("http://invalid.invalid", s)
	if _, err := a.Execute(context.Background(), ExecuteRequest{LaunchID: "interrupted"}); err == nil {
		t.Fatal("ambiguous Nanite request replayed")
	}
	l, err := a.Read(context.Background(), ReadRequest{LaunchID: "interrupted"})
	if err != nil || l.State != LaunchStateExecuting {
		t.Fatalf("interrupted intent lost: %+v %v", l, err)
	}
}

type tetherFixture struct {
	mu                                    sync.Mutex
	state                                 string
	keys                                  []string
	creates, launches, stops              int
	failCreateReply, failLaunch, failStop bool
}

func (f *tetherFixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/catalog/launches":
			json.NewEncoder(w).Encode(map[string]any{"launches": []tether.Launch{{ID: "catalog-launch", Agent: "test-agent", Project: "project-1", Provider: "codex"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/sessions":
			var req tether.LaunchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.Launch != "catalog-launch" || req.BootPrompt != "fixture prompt" || req.IdempotencyKey == "" {
				t.Errorf("bad create request: %+v", req)
			}
			f.keys = append(f.keys, req.IdempotencyKey)
			replay := f.creates > 0
			if !replay {
				f.creates++
				f.state = tether.SessionStateCreated
			}
			if f.failCreateReply {
				f.failCreateReply = false
				http.Error(w, "reply lost after create", 503)
				return
			}
			if replay {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusCreated)
			}
			json.NewEncoder(w).Encode(tether.LaunchResponse{ID: "tether-session", Replayed: replay})
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/tether-session":
			json.NewEncoder(w).Encode(tether.Session{ID: "tether-session", State: f.state})
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/tether-session/launch":
			f.launches++
			if f.failLaunch {
				f.failLaunch = false
				http.Error(w, "launch unavailable", 503)
				return
			}
			f.state = tether.SessionStateRunning
			json.NewEncoder(w).Encode(tether.LaunchResponse{ID: "tether-session"})
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/tether-session/stop":
			f.stops++
			if f.failStop {
				http.Error(w, "stop failed", 503)
				return
			}
			f.state = tether.SessionStateKilled
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Tether request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}
func tetherPrepare() PrepareRequest {
	return PrepareRequest{AgentID: "test-agent", Provider: "tether", Config: map[string]any{"launch_id": "catalog-launch", "boot_prompt": "fixture prompt"}}
}
func tetherAdapter(t *testing.T, addr string, s *LaunchStore) *TetherLaunchAdapter {
	t.Helper()
	a, err := NewTetherLaunchAdapter(addr, s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTetherExecuteAndStateRefresh(t *testing.T) {
	f := &tetherFixture{}
	srv := f.server(t)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "launches.db")
	s := reopenStore(t, path)
	a := tetherAdapter(t, srv.URL, s)
	ctx := context.Background()
	l, err := a.Prepare(ctx, tetherPrepare())
	if err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 {
		t.Fatal("prepare launched a session")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = reopenStore(t, path)
	a = tetherAdapter(t, srv.URL, s)
	running, err := a.Execute(ctx, ExecuteRequest{LaunchID: l.ID})
	if err != nil {
		t.Fatal(err)
	}
	if running.State != LaunchStateRunning || running.SessionID != "tether-session" {
		t.Fatalf("execute: %+v", running)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = reopenStore(t, path)
	a = tetherAdapter(t, srv.URL, s)
	f.mu.Lock()
	f.state = tether.SessionStateCompleted
	f.mu.Unlock()
	status, err := a.Status(ctx, StatusRequest{LaunchID: l.ID})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != LaunchStateCompleted {
		t.Fatalf("completed status: %+v", status)
	}
	stored, err := s.Get(ctx, l.ID)
	if err != nil || stored.EndedAt == nil {
		t.Fatalf("completion not saved: %+v %v", stored, err)
	}
	if f.creates != 1 || f.launches != 1 {
		t.Fatalf("duplicate side effects: %+v", f)
	}
}

func TestTetherInterruptedExecuteResumes(t *testing.T) {
	for _, afterCreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost_create_reply", true: "saved_session"}[afterCreate], func(t *testing.T) {
			f := &tetherFixture{failCreateReply: !afterCreate, failLaunch: afterCreate}
			srv := f.server(t)
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "launches.db")
			s := reopenStore(t, path)
			a := tetherAdapter(t, srv.URL, s)
			ctx := context.Background()
			l, err := a.Prepare(ctx, tetherPrepare())
			if err != nil {
				t.Fatal(err)
			}
			interrupted, err := a.Execute(ctx, ExecuteRequest{LaunchID: l.ID})
			if err != nil {
				t.Fatal(err)
			}
			if interrupted.State != LaunchStateExecuting || interrupted.Error == "" {
				t.Fatalf("interrupted: %+v", interrupted)
			}
			if afterCreate && interrupted.SessionID != "tether-session" {
				t.Fatal("creation checkpoint not saved")
			}
			s.Close()
			s = reopenStore(t, path)
			a = tetherAdapter(t, srv.URL, s)
			result, err := a.Execute(ctx, ExecuteRequest{LaunchID: l.ID})
			if err != nil {
				t.Fatal(err)
			}
			if result.State != LaunchStateRunning || result.Error != "" {
				t.Fatalf("resume: %+v", result)
			}
			if f.creates != 1 {
				t.Fatalf("created %d sessions", f.creates)
			}
			for _, key := range f.keys {
				if key != "tachyon:launch:"+l.ID {
					t.Fatalf("unstable replay key %s", key)
				}
			}
			if afterCreate && len(f.keys) != 1 {
				t.Fatal("recreated despite saved session ID")
			}
		})
	}
}

func TestTetherCancelAndFailure(t *testing.T) {
	f := &tetherFixture{}
	srv := f.server(t)
	defer srv.Close()
	s := testStore(t)
	a := tetherAdapter(t, srv.URL, s)
	ctx := context.Background()
	l, err := a.Prepare(ctx, tetherPrepare())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Execute(ctx, ExecuteRequest{LaunchID: l.ID}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.failStop = true
	f.mu.Unlock()
	if _, err = a.Cancel(ctx, CancelRequest{LaunchID: l.ID}); err == nil {
		t.Fatal("provider stop failure hidden")
	}
	stored, err := s.Get(ctx, l.ID)
	if err != nil || stored.State != LaunchStateRunning {
		t.Fatalf("false cancellation: %+v %v", stored, err)
	}
	f.mu.Lock()
	f.failStop = false
	f.mu.Unlock()
	cancelled, err := a.Cancel(ctx, CancelRequest{LaunchID: l.ID})
	if err != nil || cancelled.State != LaunchStateCancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	if f.state != tether.SessionStateKilled {
		t.Fatal("provider session not stopped")
	}
}

func TestProviderRoutingSurvivesDefaultChange(t *testing.T) {
	nanite := stubNaniteServer(t)
	defer nanite.Close()
	f := &tetherFixture{}
	srv := f.server(t)
	defer srv.Close()
	ctx := context.Background()
	dir := t.TempDir()
	p := &plugin{}
	params := subprocess.InitParams{DataDir: dir, Config: map[string]string{"nanite_url": nanite.URL, "tether_addr": srv.URL}}
	if _, err := p.Init(ctx, params); err != nil {
		t.Fatal(err)
	}
	l, err := p.adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent", Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Backend != "nanite" {
		t.Fatalf("default backend: %+v", l)
	}
	if err := p.Unload(ctx); err != nil {
		t.Fatal(err)
	}
	params.Config["default_provider"] = "tether"
	p = &plugin{}
	if _, err := p.Init(ctx, params); err != nil {
		t.Fatal(err)
	}
	defer p.Unload(ctx)
	result, err := p.adapter.Execute(ctx, ExecuteRequest{LaunchID: l.ID})
	if err != nil || result.SessionID != "session-001" {
		t.Fatalf("stored backend ignored: %+v %v", result, err)
	}
	tetherLaunch, err := p.adapter.Prepare(ctx, tetherPrepare())
	if err != nil || tetherLaunch.Backend != "tether" {
		t.Fatalf("Tether routing: %+v %v", tetherLaunch, err)
	}
	all, err := p.adapter.List(ctx, ListRequest{})
	if err != nil || len(all) != 2 {
		t.Fatalf("mixed list: %+v %v", all, err)
	}
}

func TestStoreWriteFailureDoesNotLaunch(t *testing.T) {
	s := testStore(t)
	s.Close()
	a := NewNaniteLaunchAdapter("http://invalid.invalid", s)
	if _, err := a.Execute(context.Background(), ExecuteRequest{LaunchID: "closed"}); err == nil {
		t.Fatal("closed store accepted execution")
	}
}

func TestPluginDataDirectoryFallbackAndPrecedence(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	xdg := t.TempDir()
	override := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("TACHYON_LAUNCH_DATA_DIR", "")
	p := &plugin{}
	if _, err := p.Init(ctx, subprocess.InitParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "tachyon", "plugins", "launch-ops", "launches.db")); err != nil {
		t.Fatal(err)
	}
	p.Unload(ctx)
	t.Setenv("TACHYON_LAUNCH_DATA_DIR", override)
	supplied := t.TempDir()
	if _, err := p.Init(ctx, subprocess.InitParams{DataDir: supplied}); err != nil {
		t.Fatal(err)
	}
	defer p.Unload(ctx)
	info, err := os.Stat(filepath.Join(supplied, "launches.db"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(override, "launches.db")); !os.IsNotExist(err) {
		t.Fatal("override ignored supplied data directory")
	}
}

func TestNaniteFailureSurvivesRestart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"agent": naniteAgent{ID: "test-agent"}})
			return
		}
		http.Error(w, "fixture session failure", 503)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "launches.db")
	s := reopenStore(t, path)
	a := NewNaniteLaunchAdapter(srv.URL, s)
	ctx := context.Background()
	l, err := a.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Execute(ctx, ExecuteRequest{LaunchID: l.ID}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = reopenStore(t, path)
	stored, err := s.Get(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != LaunchStateFailed || stored.Error == "" || stored.EndedAt == nil {
		t.Fatalf("failure lost: %+v", stored)
	}
}

func TestTetherPrepareValidation(t *testing.T) {
	f := &tetherFixture{}
	srv := f.server(t)
	defer srv.Close()
	a := tetherAdapter(t, srv.URL, testStore(t))
	for _, req := range []PrepareRequest{
		{AgentID: "test-agent", Model: "unsupported"},
		{AgentID: "test-agent", Config: map[string]any{"boot_prompt": true}},
		{AgentID: "test-agent", Config: map[string]any{"unknown": "value"}},
		{AgentID: "missing-agent"},
		{AgentID: "test-agent", ProjectID: "wrong-project"},
		{AgentID: "test-agent", Config: map[string]any{"launch_id": "missing-launch"}},
	} {
		if _, err := a.Prepare(context.Background(), req); err == nil {
			t.Fatalf("invalid prepare accepted: %+v", req)
		}
	}
	if f.creates != 0 {
		t.Fatal("validation caused execution")
	}
}

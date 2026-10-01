package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// stubNaniteServer mirrors Nanite's agent resolution and session creation routes.
func stubNaniteServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/test-agent":
			json.NewEncoder(w).Encode(map[string]any{"agent": naniteAgent{ID: "test-agent", Name: "Test Agent", Slug: "test-agent", Enabled: true}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/sessions":
			var req naniteCreateSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AgentID != "test-agent" {
				t.Errorf("invalid session request: %+v, error: %v", req, err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(naniteSession{ID: "session-001"})
		default:
			t.Errorf("unexpected Nanite route: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestNaniteLaunchAdapter_ImplementsInterface(t *testing.T) {
	var _ LaunchAdapter = (*NaniteLaunchAdapter)(nil)
}

func TestPrepareAndExecute(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	adapter := NewNaniteLaunchAdapter(srv.URL)
	ctx := context.Background()

	// Prepare
	launch, err := adapter.Prepare(ctx, PrepareRequest{
		AgentID:   "test-agent",
		Provider:  "tether",
		Model:     "claude-4",
		ProjectID: "proj-1",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if launch.State != LaunchStatePrepared {
		t.Errorf("expected state prepared, got %s", launch.State)
	}
	if launch.AgentID != "test-agent" {
		t.Errorf("expected agent_id test-agent, got %s", launch.AgentID)
	}
	if launch.AgentName != "Test Agent" {
		t.Errorf("expected agent_name Test Agent, got %s", launch.AgentName)
	}

	// Execute
	execResult, err := adapter.Execute(ctx, ExecuteRequest{
		LaunchID: launch.ID,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if execResult.State != LaunchStateRunning {
		t.Errorf("expected state running, got %s", execResult.State)
	}
	if execResult.SessionID != "session-001" {
		t.Errorf("expected session_id session-001, got %s", execResult.SessionID)
	}
}

func TestPrepareAndCancel(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	adapter := NewNaniteLaunchAdapter(srv.URL)
	ctx := context.Background()

	launch, err := adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	cancelled, err := adapter.Cancel(ctx, CancelRequest{
		LaunchID: launch.ID,
		Reason:   "user requested",
	})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.State != LaunchStateCancelled {
		t.Errorf("expected state cancelled, got %s", cancelled.State)
	}
	if cancelled.Error != "cancelled: user requested" {
		t.Errorf("unexpected error message: %s", cancelled.Error)
	}
}

func TestReadAndList(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	adapter := NewNaniteLaunchAdapter(srv.URL)
	ctx := context.Background()

	// Prepare two launches
	l1, err := adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent", Provider: "tether"})
	if err != nil {
		t.Fatalf("Prepare 1: %v", err)
	}
	l2, err := adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent", Provider: "nanite"})
	if err != nil {
		t.Fatalf("Prepare 2: %v", err)
	}

	// Read
	read, err := adapter.Read(ctx, ReadRequest{LaunchID: l1.ID})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.ID != l1.ID {
		t.Errorf("expected ID %s, got %s", l1.ID, read.ID)
	}

	// List all
	all, err := adapter.List(ctx, ListRequest{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 launches, got %d", len(all))
	}

	// List filtered by provider
	filtered, err := adapter.List(ctx, ListRequest{Provider: "tether"})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 launch with provider tether, got %d", len(filtered))
	}

	// Status
	status, err := adapter.Status(ctx, StatusRequest{LaunchID: l2.ID})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != LaunchStatePrepared {
		t.Errorf("expected state prepared, got %s", status.State)
	}
}

func TestExecuteRequiresPreparedState(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	adapter := NewNaniteLaunchAdapter(srv.URL)
	ctx := context.Background()

	launch, err := adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Execute once (succeeds)
	_, err = adapter.Execute(ctx, ExecuteRequest{LaunchID: launch.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Execute again (should fail — state is now "running")
	_, err = adapter.Execute(ctx, ExecuteRequest{LaunchID: launch.ID})
	if err == nil {
		t.Error("expected error re-executing a running launch")
	}
}

func TestCancelCompletedFails(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	adapter := NewNaniteLaunchAdapter(srv.URL)
	ctx := context.Background()

	launch, err := adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Cancel it
	_, err = adapter.Cancel(ctx, CancelRequest{LaunchID: launch.ID})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// Cancel again (should fail — already cancelled)
	_, err = adapter.Cancel(ctx, CancelRequest{LaunchID: launch.ID})
	if err == nil {
		t.Error("expected error cancelling an already-cancelled launch")
	}
}

func TestNotFound(t *testing.T) {
	adapter := NewNaniteLaunchAdapter("http://localhost:1") // won't be called
	ctx := context.Background()

	_, err := adapter.Read(ctx, ReadRequest{LaunchID: "nonexistent"})
	if err == nil {
		t.Error("expected error reading nonexistent launch")
	}

	_, err = adapter.Execute(ctx, ExecuteRequest{LaunchID: "nonexistent"})
	if err == nil {
		t.Error("expected error executing nonexistent launch")
	}

	_, err = adapter.Cancel(ctx, CancelRequest{LaunchID: "nonexistent"})
	if err == nil {
		t.Error("expected error cancelling nonexistent launch")
	}

	_, err = adapter.Status(ctx, StatusRequest{LaunchID: "nonexistent"})
	if err == nil {
		t.Error("expected error getting status of nonexistent launch")
	}
}

func TestVerbHandler(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	p := &plugin{
		adapter: NewNaniteLaunchAdapter(srv.URL),
	}
	ctx := context.Background()

	// Test launch_prepare verb
	prepPayload, _ := json.Marshal(PrepareRequest{AgentID: "test-agent"})
	env, err := p.HandleVerb(ctx, "launch_prepare", prepPayload)
	if err != nil {
		t.Fatalf("HandleVerb launch_prepare: %v", err)
	}
	if env.Status != "ok" {
		t.Errorf("expected ok, got %s", env.Status)
	}

	var launch Launch
	if err := json.Unmarshal(env.Data, &launch); err != nil {
		t.Fatalf("unmarshal launch: %v", err)
	}

	// Test launch_read verb
	readPayload, _ := json.Marshal(ReadRequest{LaunchID: launch.ID})
	env, err = p.HandleVerb(ctx, "launch_read", readPayload)
	if err != nil {
		t.Fatalf("HandleVerb launch_read: %v", err)
	}
	if env.Status != "ok" {
		t.Errorf("expected ok, got %s", env.Status)
	}

	// Test launch_list verb
	env, err = p.HandleVerb(ctx, "launch_list", nil)
	if err != nil {
		t.Fatalf("HandleVerb launch_list: %v", err)
	}
	if env.Status != "ok" {
		t.Errorf("expected ok, got %s", env.Status)
	}

	// Test launch_status verb
	statusPayload, _ := json.Marshal(StatusRequest{LaunchID: launch.ID})
	env, err = p.HandleVerb(ctx, "launch_status", statusPayload)
	if err != nil {
		t.Fatalf("HandleVerb launch_status: %v", err)
	}
	if env.Status != "ok" {
		t.Errorf("expected ok, got %s", env.Status)
	}

	// Test launch_execute verb
	execPayload, _ := json.Marshal(ExecuteRequest{LaunchID: launch.ID})
	env, err = p.HandleVerb(ctx, "launch_execute", execPayload)
	if err != nil {
		t.Fatalf("HandleVerb launch_execute: %v", err)
	}
	if env.Status != "ok" {
		t.Errorf("expected ok, got %s", env.Status)
	}

	// Test launch_cancel on a new launch
	prep2, _ := json.Marshal(PrepareRequest{AgentID: "test-agent"})
	env, _ = p.HandleVerb(ctx, "launch_prepare", prep2)
	var launch2 Launch
	json.Unmarshal(env.Data, &launch2)

	cancelPayload, _ := json.Marshal(CancelRequest{LaunchID: launch2.ID, Reason: "test"})
	env, err = p.HandleVerb(ctx, "launch_cancel", cancelPayload)
	if err != nil {
		t.Fatalf("HandleVerb launch_cancel: %v", err)
	}
	if env.Status != "ok" {
		t.Errorf("expected ok, got %s", env.Status)
	}

	// Test unknown verb
	env, err = p.HandleVerb(ctx, "launch_nonexistent", nil)
	if err != nil {
		t.Fatalf("HandleVerb unknown: %v", err)
	}
	if env.Status != "error" {
		t.Errorf("expected error for unknown verb, got %s", env.Status)
	}
}

func TestVerbValidation(t *testing.T) {
	p := &plugin{
		adapter: NewNaniteLaunchAdapter("http://localhost:1"),
	}
	ctx := context.Background()

	tests := []struct {
		verb    string
		payload string
		errCode string
	}{
		{"launch_prepare", `{}`, "validation"},
		{"launch_execute", `{}`, "validation"},
		{"launch_cancel", `{}`, "validation"},
		{"launch_read", `{}`, "validation"},
		{"launch_status", `{}`, "validation"},
	}

	for _, tt := range tests {
		env, err := p.HandleVerb(ctx, tt.verb, json.RawMessage(tt.payload))
		if err != nil {
			t.Errorf("HandleVerb %s: unexpected error: %v", tt.verb, err)
			continue
		}
		if env.Status != "error" {
			t.Errorf("HandleVerb %s: expected error status, got %s", tt.verb, env.Status)
		}
		if env.Error == nil || env.Error.Code != tt.errCode {
			t.Errorf("HandleVerb %s: expected error code %s, got %v", tt.verb, tt.errCode, env.Error)
		}
	}
}

func TestCapabilitiesEmbed(t *testing.T) {
	// Verify the embedded capabilities.json parses correctly
	var caps contract.PluginCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &caps); err != nil {
		t.Fatalf("parse capabilities.json: %v", err)
	}
	if err := caps.Validate(); err != nil {
		t.Fatalf("validate capabilities: %v", err)
	}

	if len(caps.Modules) != 1 || caps.Modules[0] != "launch" {
		t.Errorf("expected modules [launch], got %v", caps.Modules)
	}
	if len(caps.Verbs) != 6 {
		t.Errorf("expected 6 verbs, got %d", len(caps.Verbs))
	}

	expectedVerbs := map[string]string{
		"launch_prepare": "writes",
		"launch_execute": "open_world",
		"launch_cancel":  "open_world",
		"launch_read":    "reads",
		"launch_list":    "reads",
		"launch_status":  "reads",
	}
	for verb, expectedEffect := range expectedVerbs {
		decl, ok := caps.Verbs[verb]
		if !ok {
			t.Errorf("missing verb %s", verb)
			continue
		}
		if string(decl.Effect) != expectedEffect {
			t.Errorf("verb %s: expected effect %s, got %s", verb, expectedEffect, decl.Effect)
		}
	}
}

func TestPluginInit(t *testing.T) {
	p := &plugin{}
	result, err := p.Init(context.Background(), subprocess.InitParams{
		Config: map[string]string{"nanite_url": "http://example.com"},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result.ID != "launch-ops" {
		t.Errorf("expected ID launch-ops, got %s", result.ID)
	}
	if p.adapter == nil {
		t.Error("adapter not initialized")
	}

	// Verify capabilities were parsed
	caps := p.Capabilities()
	if len(caps.Modules) != 1 || caps.Modules[0] != "launch" {
		t.Errorf("expected modules [launch], got %v", caps.Modules)
	}
}

func TestListLimit(t *testing.T) {
	srv := stubNaniteServer(t)
	defer srv.Close()

	adapter := NewNaniteLaunchAdapter(srv.URL)
	ctx := context.Background()

	// Prepare 5 launches
	for i := 0; i < 5; i++ {
		_, err := adapter.Prepare(ctx, PrepareRequest{AgentID: "test-agent"})
		if err != nil {
			t.Fatalf("Prepare %d: %v", i, err)
		}
	}

	// List with limit
	limited, err := adapter.List(ctx, ListRequest{Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(limited) != 3 {
		t.Errorf("expected 3 launches, got %d", len(limited))
	}
}

// TestExecuteCancelRace checks cancellation survives both a successful and failed
// session request while Execute is waiting for Nanite.
func TestExecuteCancelRace(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(naniteSession{ID: "session-race"})
			}))
			defer srv.Close()
			a := NewNaniteLaunchAdapter(srv.URL)
			a.launches["race"] = &Launch{ID: "race", AgentID: "test-agent", State: LaunchStatePrepared}
			done := make(chan *Launch, 1)
			go func() {
				result, err := a.Execute(context.Background(), ExecuteRequest{LaunchID: "race"})
				if err != nil {
					t.Errorf("Execute: %v", err)
				}
				done <- result
			}()
			<-entered
			cancelled, err := a.Cancel(context.Background(), CancelRequest{LaunchID: "race", Reason: "operator"})
			close(release)
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			result := <-done
			if result == nil || result.State != LaunchStateCancelled || result.Error != cancelled.Error {
				t.Fatalf("Execute overwrote cancellation: %+v", result)
			}
			stored, err := a.Read(context.Background(), ReadRequest{LaunchID: "race"})
			if err != nil || stored.State != LaunchStateCancelled {
				t.Fatalf("stored launch: %+v, %v", stored, err)
			}
		})
	}
}

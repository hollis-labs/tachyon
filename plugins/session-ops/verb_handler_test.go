package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

type stubAdapter struct {
	calls int
	err   error
}

func (a *stubAdapter) Create(context.Context, CreateSessionRequest) (Session, error) {
	a.calls++
	return Session{ID: "s1"}, a.err
}
func (a *stubAdapter) Read(context.Context, string) (Session, error) {
	a.calls++
	return Session{ID: "s1"}, a.err
}
func (a *stubAdapter) List(context.Context, ListSessionsRequest) (SessionPage, error) {
	a.calls++
	return SessionPage{Sessions: []Session{{ID: "s1"}}}, a.err
}
func (a *stubAdapter) Attach(context.Context, string) (ConnectionInfo, error) {
	a.calls++
	return ConnectionInfo{SessionID: "s1"}, a.err
}
func (a *stubAdapter) Stop(context.Context, string) error           { a.calls++; return a.err }
func (a *stubAdapter) Submit(context.Context, string, string) error { a.calls++; return a.err }
func (a *stubAdapter) History(context.Context, HistoryRequest) (HistoryPage, error) {
	a.calls++
	return HistoryPage{Events: []HistoryEvent{}}, a.err
}

func TestCommandsDispatchSessionVerbs(t *testing.T) {
	for _, tc := range []struct{ verb, payload string }{
		{"session_create", `{"launch_id":"launch-1"}`},
		{"session_read", `{"id":"s1"}`},
		{"session_list", ``},
		{"session_attach", `{"id":"s1"}`},
		{"session_stop", `{"id":"s1"}`},
		{"session_submit", `{"id":"s1","text":"hello"}`},
		{"session_history", `{"id":"s1"}`},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			a := &stubAdapter{}
			p := &plugin{adapter: a}
			result, err := p.Command(context.Background(), subprocess.CommandRequest{Name: tc.verb, Args: tc.payload})
			if err != nil {
				t.Fatal(err)
			}
			var envelope contract.ResultEnvelope
			if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil {
				t.Fatal(err)
			}
			if result.Action != "message" || envelope.Status != contract.StatusOK || a.calls != 1 {
				t.Fatalf("result: %+v calls: %d", envelope, a.calls)
			}
			a.err = errors.New("offline")
			envelope, err = p.HandleVerb(context.Background(), tc.verb, json.RawMessage(tc.payload))
			if err != nil || envelope.Status != contract.StatusError || envelope.Error.Code != "provider_error" {
				t.Fatalf("provider error: %+v %v", envelope, err)
			}
		})
	}
}

func TestInvalidPayloadsNeverCallProvider(t *testing.T) {
	for _, tc := range []struct{ verb, payload string }{
		{"session_create", `{}`}, {"session_create", `null`},
		{"session_read", `{"id":" "}`}, {"session_attach", `{`},
		{"session_stop", `{}`}, {"session_submit", `{"id":"s1","text":" "}`},
		{"session_history", `{"id":"s1","cursor":-1}`},
		{"session_list", `{"limit":-1}`}, {"session_list", `[]`},
	} {
		t.Run(tc.verb+tc.payload, func(t *testing.T) {
			a := &stubAdapter{}
			result, err := (&plugin{adapter: a}).HandleVerb(context.Background(), tc.verb, json.RawMessage(tc.payload))
			if err != nil || result.Status != contract.StatusError || result.Error.Code != "validation" || a.calls != 0 {
				t.Fatalf("result: %+v %v; calls=%d", result, err, a.calls)
			}
		})
	}
}

func TestCapabilityCommandAndUnsupportedVerbs(t *testing.T) {
	p := &plugin{}
	result, err := p.Command(context.Background(), subprocess.CommandRequest{Name: pluginkit.CommandCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	var caps contract.PluginCapabilities
	if err := json.Unmarshal([]byte(result.Content), &caps); err != nil {
		t.Fatal(err)
	}
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"session_pause", "session_resume", "unknown"} {
		if _, declared := caps.Verbs[verb]; declared {
			t.Fatalf("unsupported verb advertised: %s", verb)
		}
		if _, handled, err := pluginkit.Dispatch(context.Background(), p, subprocess.CommandRequest{Name: verb}); handled || err != nil {
			t.Fatalf("unsupported verb handled: %s %t %v", verb, handled, err)
		}
		if _, err := p.Command(context.Background(), subprocess.CommandRequest{Name: verb}); err == nil {
			t.Fatalf("unknown command accepted: %s", verb)
		}
	}
}

func TestInitConfiguresClientWithoutDaemon(t *testing.T) {
	p := &plugin{}
	result, err := p.Init(context.Background(), subprocess.InitParams{Config: map[string]string{"tether_addr": "http://127.0.0.1:1"}})
	if err != nil || result.ID != "session-ops" || p.adapter == nil {
		t.Fatalf("init: %+v %v", result, err)
	}
	if _, err := p.Init(context.Background(), subprocess.InitParams{Config: map[string]string{"tether_addr": "invalid"}}); err == nil {
		t.Fatal("invalid address accepted")
	}
}

package hitl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	core "github.com/hollis-labs/go-hitl"
)

func TestStatusDoesNotExposeTerminalPrivateData(t *testing.T) {
	f := &fakeClient{}
	r, _ := NewRuntime(f, "")
	a := ask(t, r, `{}`, func() bool { return true })
	for _, state := range []core.State{core.StateResolved, core.StateFailed, core.StateCanceled} {
		terminal(f, state, "approval", "approved")
		if resolved, ok := f.result.Item.TerminalOutcome.(core.Resolved); ok {
			resolved.Resolution.ResolutionID = "secret-resolution"
			resolved.Resolution.Participant = core.Participant{PrincipalRef: "secret-principal", Authority: "secret-authority", Assurance: "secret-assurance", Proof: &core.Proof{Scheme: "secret-proof", KeyRef: "key", Binds: []core.ProofBind{{Name: "op", Value: "secret-binding"}}}}
			resolved.Resolution.Response.Note = "secret-note"
			resolved.Resolution.Response.Reply = "secret-reply"
			resolved.Resolution.Response.Extra = map[string]json.RawMessage{"secret_extra": json.RawMessage(`"secret-extra"`)}
			f.result.Item.TerminalOutcome = resolved
		}
		if failed, ok := f.result.Item.TerminalOutcome.(core.Failed); ok {
			failed.Message = "secret-failure"
			f.result.Item.TerminalOutcome = failed
		}
		s, e := r.Status(context.Background(), a.OperationID, 0)
		if e != nil {
			t.Fatal(e)
		}
		wire, _ := json.Marshal(s)
		if strings.Contains(string(wire), "secret") || strings.Contains(string(wire), "terminal_outcome") || strings.Contains(string(wire), "participant") {
			t.Fatal(string(wire))
		}
		if state == core.StateResolved && (s.Decision != "approved" || s.ResolvedAt == nil || !s.Approved) {
			t.Fatal(s)
		}
	}
}

type countedWaitClient struct {
	*fakeClient
	started, release chan struct{}
	calls            atomic.Int32
}

func (f *countedWaitClient) Retrieve(ctx context.Context, id string, wait int) (core.RetrievalResult, error) {
	if f.calls.Add(1) == 1 {
		close(f.started)
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return core.RetrievalResult{}, ctx.Err()
	}
	return f.fakeClient.Retrieve(ctx, id, wait)
}
func TestParallelStatusBoundAndLogs(t *testing.T) {
	f := &countedWaitClient{fakeClient: &fakeClient{}, started: make(chan struct{}), release: make(chan struct{})}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	r, _ := NewRuntime(f, "", logger)
	a := ask(t, r, `{}`, func() bool { return true })
	terminal(f.fakeClient, core.StateResolved, "approval", "approved")
	done := make(chan error, 1)
	go func() { _, e := r.Status(context.Background(), a.OperationID, 0); done <- e }()
	<-f.started
	for i := 0; i < 30; i++ {
		if _, e := r.Status(context.Background(), a.OperationID, 0); !errors.Is(e, ErrStatusBusy) {
			t.Fatal(e)
		}
	}
	if f.calls.Load() != 1 {
		t.Fatal("multiple backend sessions", f.calls.Load())
	}
	close(f.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if _, e := r.Status(context.Background(), a.OperationID, MaxStatusWait+1); e == nil {
		t.Fatal("oversized await")
	}
	if f.calls.Load() != 1 {
		t.Fatal("invalid await reached backend")
	}
	if !strings.Contains(logs.String(), `"reason_class":"poll_busy"`) || strings.Contains(logs.String(), a.OperationID) {
		t.Fatal(logs.String())
	}
}
func TestFailureLogsContainNoRequestSecrets(t *testing.T) {
	f := &fakeClient{err: errors.New("http://secret-host/token=secret payload secret")}
	var logs bytes.Buffer
	r, _ := NewRuntime(f, "", slog.New(slog.NewJSONHandler(&logs, nil)))
	a := ask(t, r, `{"secret":"payload"}`, func() bool { return true })
	if a.OperationID != "" {
		t.Fatal(a)
	}
	_, _ = r.Status(context.Background(), "http://secret-host/token", 0)
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "payload") || !strings.Contains(logs.String(), "enqueue_unavailable") {
		t.Fatal(logs.String())
	}
}
func TestPayloadBoundBeforeDecode(t *testing.T) {
	f := &fakeClient{}
	r, _ := NewRuntime(f, "")
	raw := []byte(`{"status":"ask","ask":{"prompt":"P"}}`)
	out := r.Attach(context.Background(), "work_write", bytes.Repeat([]byte("x"), MaxPayload+1), "g", func() bool { return true }, raw)
	if !strings.Contains(string(out), "payload_limit") || len(f.requests) != 0 {
		t.Fatal(string(out))
	}
	if _, e := Canonical(bytes.Repeat([]byte("x"), MaxPayload+1)); e == nil || e.Error() != "payload limit" {
		t.Fatal(e)
	}
}

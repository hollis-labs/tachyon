package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// newTestPlugin creates a plugin with a local adapter for testing.
func newTestPlugin() *plugin {
	return &plugin{
		adapter: NewLocalAdapter(100),
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandleVerbUnknown(t *testing.T) {
	p := newTestPlugin()
	env, err := p.handleVerb(context.Background(), "observe_nonexistent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusError {
		t.Errorf("status = %q, want error", env.Status)
	}
	if env.Error == nil || env.Error.Code != "unknown_verb" {
		t.Error("expected unknown_verb error code")
	}
}

func TestHandleVerbObserveActivity(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	// Seed data.
	local := p.adapter.(*LocalAdapter)
	local.RecordActivity(ActivityEntry{Kind: "verb", Source: "agent-ops", Summary: "test activity"})

	env, err := p.handleVerb(ctx, "observe_activity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok; error: %+v", env.Status, env.Error)
	}

	var entries []ActivityEntry
	if err := json.Unmarshal(env.Data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Summary != "test activity" {
		t.Errorf("summary = %q, want %q", entries[0].Summary, "test activity")
	}
}

func TestHandleVerbObserveActivityWithFilter(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	local := p.adapter.(*LocalAdapter)
	local.RecordActivity(ActivityEntry{Kind: "verb", Source: "agent-ops", Summary: "a"})
	local.RecordActivity(ActivityEntry{Kind: "lifecycle", Source: "observe-ops", Summary: "b"})

	filter := mustMarshal(t, ActivityFilter{Kind: "lifecycle"})
	env, err := p.handleVerb(ctx, "observe_activity", filter)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	var entries []ActivityEntry
	if err := json.Unmarshal(env.Data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

func TestHandleVerbObserveLogs(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	local := p.adapter.(*LocalAdapter)
	local.RecordLog(LogEntry{Level: "info", Source: "test", Message: "hello"})

	env, err := p.handleVerb(ctx, "observe_logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	var logs []LogEntry
	if err := json.Unmarshal(env.Data, &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(logs))
	}
}

func TestHandleVerbObserveMetrics(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	local := p.adapter.(*LocalAdapter)
	local.RecordMetric(MetricPoint{Name: "tokens_used", Value: 42})

	env, err := p.handleVerb(ctx, "observe_metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	var points []MetricPoint
	if err := json.Unmarshal(env.Data, &points); err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Value != 42 {
		t.Fatalf("got %+v, want 1 point with value 42", points)
	}
}

func TestHandleVerbObserveEvents(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	local := p.adapter.(*LocalAdapter)
	local.RecordEvent(Event{Kind: "agent_created", Source: "agent-ops"})

	env, err := p.handleVerb(ctx, "observe_events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	var events []Event
	if err := json.Unmarshal(env.Data, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
}

func TestHandleVerbObserveStatus(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	env, err := p.handleVerb(ctx, "observe_status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	var summary StatusSummary
	if err := json.Unmarshal(env.Data, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.HealthStatus != "healthy" {
		t.Errorf("health = %q, want healthy", summary.HealthStatus)
	}
}

func TestHandleVerbObserveSubscribe(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	payload := mustMarshal(t, SubscribeRequest{Channel: "activity"})
	env, err := p.handleVerb(ctx, "observe_subscribe", payload)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	var handle SubscriptionHandle
	if err := json.Unmarshal(env.Data, &handle); err != nil {
		t.Fatal(err)
	}
	if handle.Channel != "activity" {
		t.Errorf("channel = %q, want activity", handle.Channel)
	}
}

func TestHandleVerbObserveSubscribeValidation(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	// Missing channel.
	payload := mustMarshal(t, SubscribeRequest{})
	env, err := p.handleVerb(ctx, "observe_subscribe", payload)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusError {
		t.Fatalf("status = %q, want error", env.Status)
	}
	if env.Error == nil || env.Error.Code != "validation" {
		t.Error("expected validation error")
	}
}

func TestHandleVerbBadPayload(t *testing.T) {
	p := newTestPlugin()
	ctx := context.Background()

	bad := json.RawMessage(`{invalid`)
	env, err := p.handleVerb(ctx, "observe_activity", bad)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusError {
		t.Fatalf("status = %q, want error for bad payload", env.Status)
	}
	if env.Error == nil || env.Error.Code != "validation" {
		t.Error("expected validation error code")
	}
}

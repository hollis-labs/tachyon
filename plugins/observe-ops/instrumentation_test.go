package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// TestInstrumentedVerbRecordsSelfFeed verifies that calling HandleVerb
// (the instrumented path) records an activity entry, event, and metric
// that are then visible via observe_activity / observe_events /
// observe_metrics.
func TestInstrumentedVerbRecordsSelfFeed(t *testing.T) {
	p := &plugin{}
	la := NewLocalAdapter(100)
	p.local = la
	p.adapter = la

	ctx := context.Background()

	// Call observe_status through the instrumented path.
	env, err := p.HandleVerb(ctx, "observe_status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK {
		t.Fatalf("status = %q, want ok", env.Status)
	}

	// The instrumented wrapper should have recorded an activity entry.
	actEnv, err := p.handleVerb(ctx, "observe_activity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if actEnv.Status != contract.StatusOK {
		t.Fatalf("activity status = %q, want ok", actEnv.Status)
	}
	var entries []ActivityEntry
	if err := json.Unmarshal(actEnv.Data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 activity entry from instrumented verb call")
	}
	found := false
	for _, e := range entries {
		if e.Kind == "verb" && e.Source == "observe-ops" {
			found = true
			break
		}
	}
	if !found {
		t.Error("no verb activity entry from instrumented call")
	}

	// Should also have recorded an event.
	evEnv, err := p.handleVerb(ctx, "observe_events", nil)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	if err := json.Unmarshal(evEnv.Data, &events); err != nil {
		t.Fatal(err)
	}
	foundEvent := false
	for _, e := range events {
		if e.Kind == "verb_invoked" {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Error("no verb_invoked event from instrumented call")
	}

	// Should also have recorded a latency metric.
	mEnv, err := p.handleVerb(ctx, "observe_metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	var metrics []MetricPoint
	if err := json.Unmarshal(mEnv.Data, &metrics); err != nil {
		t.Fatal(err)
	}
	foundMetric := false
	for _, m := range metrics {
		if m.Name == "latency_ms" && m.Tags["verb"] == "observe_status" {
			foundMetric = true
			break
		}
	}
	if !foundMetric {
		t.Error("no latency_ms metric for observe_status")
	}
}

// TestInstrumentedVerbRecordsErrorLog verifies that a failed verb
// invocation records an error log entry.
func TestInstrumentedVerbRecordsErrorLog(t *testing.T) {
	p := &plugin{}
	la := NewLocalAdapter(100)
	p.local = la
	p.adapter = la

	ctx := context.Background()

	// Call an unknown verb — will return an error envelope.
	env, err := p.HandleVerb(ctx, "observe_nonexistent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusError {
		t.Fatalf("status = %q, want error", env.Status)
	}

	// Should have recorded an error log.
	logEnv, err := p.handleVerb(ctx, "observe_logs", mustMarshalInstr(t, LogFilter{Level: "error"}))
	if err != nil {
		t.Fatal(err)
	}
	var logs []LogEntry
	if err := json.Unmarshal(logEnv.Data, &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("expected an error log entry for failed verb")
	}
}

// TestLifecycleEventsRecorded verifies that Init and Load record
// lifecycle events and logs.
func TestLifecycleEventsRecorded(t *testing.T) {
	p := &plugin{}
	ctx := context.Background()
	if _, err := p.Init(ctx, subprocess.InitParams{Config: pollingTestConfig(t, nil)}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(ctx); err != nil {
		t.Fatal(err)
	}

	// Check events.
	evEnv, err := p.handleVerb(ctx, "observe_events", nil)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	if err := json.Unmarshal(evEnv.Data, &events); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, e := range events {
		kinds[e.Kind] = true
	}
	if !kinds["plugin_init"] {
		t.Error("missing plugin_init event")
	}
	if !kinds["plugin_load"] {
		t.Error("missing plugin_load event")
	}

	// Check logs.
	logEnv, err := p.handleVerb(ctx, "observe_logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	var logs []LogEntry
	if err := json.Unmarshal(logEnv.Data, &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) < 2 {
		t.Fatalf("expected at least 2 lifecycle logs, got %d", len(logs))
	}
}

func mustMarshalInstr(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCommandRecordsActivity(t *testing.T) {
	p := &plugin{}
	ctx := context.Background()
	if _, err := p.Init(ctx, subprocess.InitParams{Config: pollingTestConfig(t, nil)}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Command(ctx, subprocess.CommandRequest{Name: "observe_status"}); err != nil {
		t.Fatal(err)
	}
	env, err := p.HandleVerb(ctx, "observe_activity", nil)
	if err != nil {
		t.Fatal(err)
	}
	var entries []ActivityEntry
	if err := json.Unmarshal(env.Data, &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Summary == "verb observe_status invoked" {
			return
		}
	}
	t.Fatal("command dispatch did not record observe_status activity")
}

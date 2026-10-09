package main

import (
	"context"
	"encoding/json"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"testing"
)

func TestObserveReadsDoNotRecord(t *testing.T) {
	p := &plugin{local: NewLocalAdapter(100)}
	p.adapter = p.local
	for n := 0; n < 5; n++ {
		for _, verb := range []string{"observe_status", "observe_activity", "observe_events", "observe_logs", "observe_metrics", "observe_nonexistent"} {
			if _, e := p.HandleVerb(context.Background(), verb, nil); e != nil {
				t.Fatal(e)
			}
		}
	}
	p.local.mu.RLock()
	defer p.local.mu.RUnlock()
	if len(p.local.activities)+len(p.local.events)+len(p.local.metrics)+len(p.local.logs) != 0 {
		t.Fatal("Observe reads recursively recorded")
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

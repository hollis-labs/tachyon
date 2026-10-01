package main

import (
	"context"
	"testing"
	"time"
)

func TestNewLocalAdapter(t *testing.T) {
	a := NewLocalAdapter(10)
	if a.maxEntries != 10 {
		t.Fatalf("maxEntries = %d, want 10", a.maxEntries)
	}
	if a.startedAt.IsZero() {
		t.Fatal("startedAt should be set")
	}
}

func TestNewLocalAdapterDefaultCap(t *testing.T) {
	a := NewLocalAdapter(0)
	if a.maxEntries != 1000 {
		t.Fatalf("maxEntries = %d, want 1000 (default)", a.maxEntries)
	}
}

func TestRecordAndListActivity(t *testing.T) {
	a := NewLocalAdapter(100)
	ctx := context.Background()

	a.RecordActivity(ActivityEntry{Kind: "verb", Source: "agent-ops", Summary: "first"})
	a.RecordActivity(ActivityEntry{Kind: "lifecycle", Source: "observe-ops", Summary: "second"})
	a.RecordActivity(ActivityEntry{Kind: "verb", Source: "agent-ops", Summary: "third"})

	// Unfiltered — newest first.
	entries, err := a.ListActivity(ctx, ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Summary != "third" {
		t.Errorf("first entry = %q, want %q", entries[0].Summary, "third")
	}

	// Filter by source.
	entries, err = a.ListActivity(ctx, ActivityFilter{Source: "agent-ops"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (source=agent-ops)", len(entries))
	}

	// Filter by kind.
	entries, err = a.ListActivity(ctx, ActivityFilter{Kind: "lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (kind=lifecycle)", len(entries))
	}

	// Limit.
	entries, err = a.ListActivity(ctx, ActivityFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (limit=1)", len(entries))
	}
}

func TestActivityRingBuffer(t *testing.T) {
	a := NewLocalAdapter(3)
	for i := 0; i < 5; i++ {
		a.RecordActivity(ActivityEntry{Summary: "entry"})
	}
	entries, err := a.ListActivity(context.Background(), ActivityFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (ring buffer cap)", len(entries))
	}
}

func TestRecordAndListLogs(t *testing.T) {
	a := NewLocalAdapter(100)
	ctx := context.Background()

	a.RecordLog(LogEntry{Level: "info", Source: "agent-ops", Message: "agent created"})
	a.RecordLog(LogEntry{Level: "error", Source: "observe-ops", Message: "something broke"})
	a.RecordLog(LogEntry{Level: "info", Source: "agent-ops", Message: "agent updated"})

	// Unfiltered.
	logs, err := a.ListLogs(ctx, LogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("got %d logs, want 3", len(logs))
	}

	// Filter by level.
	logs, err = a.ListLogs(ctx, LogFilter{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1 (level=error)", len(logs))
	}

	// Search.
	logs, err = a.ListLogs(ctx, LogFilter{Search: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("got %d logs, want 2 (search=agent)", len(logs))
	}
}

func TestRecordAndListMetrics(t *testing.T) {
	a := NewLocalAdapter(100)
	ctx := context.Background()

	now := time.Now()
	a.RecordMetric(MetricPoint{Name: "tokens_used", Value: 100, Tags: map[string]string{"agent_id": "a1"}, Timestamp: now.Add(-2 * time.Second)})
	a.RecordMetric(MetricPoint{Name: "latency_ms", Value: 42, Tags: map[string]string{"verb": "agent_list"}, Timestamp: now.Add(-1 * time.Second)})
	a.RecordMetric(MetricPoint{Name: "tokens_used", Value: 200, Tags: map[string]string{"agent_id": "a2"}, Timestamp: now})

	// Unfiltered — returned chronologically (oldest first for charting).
	points, err := a.ListMetrics(ctx, MetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("got %d points, want 3", len(points))
	}
	if points[0].Value != 100 {
		t.Errorf("first point value = %v, want 100 (oldest first)", points[0].Value)
	}

	// Filter by name.
	points, err = a.ListMetrics(ctx, MetricFilter{Names: []string{"tokens_used"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2 (name=tokens_used)", len(points))
	}

	// Filter by tags.
	points, err = a.ListMetrics(ctx, MetricFilter{Tags: map[string]string{"agent_id": "a1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (agent_id=a1)", len(points))
	}
}

func TestRecordAndListEvents(t *testing.T) {
	a := NewLocalAdapter(100)
	ctx := context.Background()

	a.RecordEvent(Event{Kind: "agent_created", Source: "agent-ops"})
	a.RecordEvent(Event{Kind: "error", Source: "observe-ops"})
	a.RecordEvent(Event{Kind: "session_ended", Source: "agent-ops"})

	events, err := a.ListEvents(ctx, EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	// Filter by kind.
	events, err = a.ListEvents(ctx, EventFilter{Kind: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (kind=error)", len(events))
	}
}

func TestStatus(t *testing.T) {
	a := NewLocalAdapter(100)
	ctx := context.Background()

	// No errors — healthy.
	s, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.HealthStatus != "healthy" {
		t.Errorf("health = %q, want healthy", s.HealthStatus)
	}
	if s.UptimeSeconds < 0 {
		t.Error("uptime should be non-negative")
	}

	// Add errors to trigger degraded.
	for i := 0; i < 15; i++ {
		a.RecordEvent(Event{Kind: "error"})
	}
	s, err = a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.ErrorCount != 15 {
		t.Errorf("error_count = %d, want 15", s.ErrorCount)
	}
	if s.HealthStatus != "degraded" {
		t.Errorf("health = %q, want degraded", s.HealthStatus)
	}
}

func TestSubscribe(t *testing.T) {
	a := NewLocalAdapter(100)
	ctx := context.Background()

	handle, err := a.Subscribe(ctx, SubscribeRequest{Channel: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	if handle.Channel != "activity" {
		t.Errorf("channel = %q, want activity", handle.Channel)
	}
	if handle.ID == "" {
		t.Error("handle ID should be set")
	}
	if handle.Endpoint == "" {
		t.Error("handle endpoint should be set")
	}
}

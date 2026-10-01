package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// LocalAdapter is the MVP ObserveAdapter implementation. It maintains an
// in-memory ring buffer of activity entries, log lines, events, and
// metric points. Data is populated by the plugin's own Command() wrapper
// which instruments every verb invocation with activity, event, latency
// and error entries. Lifecycle events are recorded at Init and Load.
//
// PollingAdapter supplements this local telemetry with external snapshots.
//
// This is intentionally ephemeral — data lives only as long as the plugin
// process. Durable observability (Tether sessions, Flux streams, OTel) is
// a later integration.
type LocalAdapter struct {
	mu         sync.RWMutex
	activities []ActivityEntry
	logs       []LogEntry
	metrics    []MetricPoint
	events     []Event
	startedAt  time.Time
	nextID     int
	maxEntries int // ring buffer cap per category
}

// NewLocalAdapter creates a LocalAdapter with the given ring buffer size.
func NewLocalAdapter(maxEntries int) *LocalAdapter {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	return &LocalAdapter{
		startedAt:  time.Now(),
		maxEntries: maxEntries,
	}
}

func (a *LocalAdapter) allocID() string {
	a.nextID++
	return fmt.Sprintf("obs-%06d", a.nextID)
}

// RecordActivity appends an activity entry. Thread-safe.
func (a *LocalAdapter) RecordActivity(entry ActivityEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry.ID == "" {
		entry.ID = a.allocID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	a.activities = append(a.activities, entry)
	if len(a.activities) > a.maxEntries {
		a.activities = a.activities[len(a.activities)-a.maxEntries:]
	}
}

// RecordLog appends a log entry. Thread-safe.
func (a *LocalAdapter) RecordLog(entry LogEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry.ID == "" {
		entry.ID = a.allocID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	a.logs = append(a.logs, entry)
	if len(a.logs) > a.maxEntries {
		a.logs = a.logs[len(a.logs)-a.maxEntries:]
	}
}

// RecordMetric appends a metric point. Thread-safe.
func (a *LocalAdapter) RecordMetric(point MetricPoint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if point.Timestamp.IsZero() {
		point.Timestamp = time.Now()
	}
	a.metrics = append(a.metrics, point)
	if len(a.metrics) > a.maxEntries {
		a.metrics = a.metrics[len(a.metrics)-a.maxEntries:]
	}
}

// RecordEvent appends a lifecycle event. Thread-safe.
func (a *LocalAdapter) RecordEvent(event Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if event.ID == "" {
		event.ID = a.allocID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	a.events = append(a.events, event)
	if len(a.events) > a.maxEntries {
		a.events = a.events[len(a.events)-a.maxEntries:]
	}
}

// ListActivity implements ObserveAdapter. Returns entries newest-first.
func (a *LocalAdapter) ListActivity(_ context.Context, filter ActivityFilter) ([]ActivityEntry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	// Filter and collect in reverse order (newest first).
	var result []ActivityEntry
	for i := len(a.activities) - 1; i >= 0 && len(result) < limit; i-- {
		e := a.activities[i]
		if filter.Source != "" && e.Source != filter.Source {
			continue
		}
		if filter.Kind != "" && e.Kind != filter.Kind {
			continue
		}
		if filter.SinceID != "" {
			// Skip until we pass the since marker.
			if e.ID == filter.SinceID {
				break
			}
		}
		result = append(result, e)
	}
	return result, nil
}

// ListLogs implements ObserveAdapter.
func (a *LocalAdapter) ListLogs(_ context.Context, filter LogFilter) ([]LogEntry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	var since, until time.Time
	if filter.Since != "" {
		if t, err := time.Parse(time.RFC3339, filter.Since); err == nil {
			since = t
		}
	}
	if filter.Until != "" {
		if t, err := time.Parse(time.RFC3339, filter.Until); err == nil {
			until = t
		}
	}

	var result []LogEntry
	for i := len(a.logs) - 1; i >= 0 && len(result) < limit; i-- {
		e := a.logs[i]
		if filter.Source != "" && e.Source != filter.Source {
			continue
		}
		if filter.Level != "" && e.Level != filter.Level {
			continue
		}
		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		if !until.IsZero() && e.Timestamp.After(until) {
			continue
		}
		if filter.Search != "" && !strings.Contains(e.Message, filter.Search) {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

// ListMetrics implements ObserveAdapter.
func (a *LocalAdapter) ListMetrics(_ context.Context, filter MetricFilter) ([]MetricPoint, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	var since, until time.Time
	if filter.Since != "" {
		if t, err := time.Parse(time.RFC3339, filter.Since); err == nil {
			since = t
		}
	}
	if filter.Until != "" {
		if t, err := time.Parse(time.RFC3339, filter.Until); err == nil {
			until = t
		}
	}

	nameSet := make(map[string]bool, len(filter.Names))
	for _, n := range filter.Names {
		nameSet[n] = true
	}

	var result []MetricPoint
	for i := len(a.metrics) - 1; i >= 0 && len(result) < limit; i-- {
		m := a.metrics[i]
		if len(nameSet) > 0 && !nameSet[m.Name] {
			continue
		}
		if !since.IsZero() && m.Timestamp.Before(since) {
			continue
		}
		if !until.IsZero() && m.Timestamp.After(until) {
			continue
		}
		if len(filter.Tags) > 0 {
			match := true
			for k, v := range filter.Tags {
				if m.Tags[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		result = append(result, m)
	}

	// Return chronologically for metrics (oldest first is more natural
	// for time-series charting).
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})
	return result, nil
}

// ListEvents implements ObserveAdapter.
func (a *LocalAdapter) ListEvents(_ context.Context, filter EventFilter) ([]Event, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	var result []Event
	for i := len(a.events) - 1; i >= 0 && len(result) < limit; i-- {
		e := a.events[i]
		if filter.Kind != "" && e.Kind != filter.Kind {
			continue
		}
		if filter.Source != "" && e.Source != filter.Source {
			continue
		}
		if filter.SinceID != "" {
			if e.ID == filter.SinceID {
				break
			}
		}
		result = append(result, e)
	}
	return result, nil
}

// Status implements ObserveAdapter.
func (a *LocalAdapter) Status(_ context.Context) (*StatusSummary, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	errCount := 0
	for _, e := range a.events {
		if e.Kind == "error" {
			errCount++
		}
	}

	health := "healthy"
	if errCount > 10 {
		health = "degraded"
	}
	if errCount > 50 {
		health = "unhealthy"
	}

	return &StatusSummary{
		ErrorCount:    errCount,
		HealthStatus:  health,
		UptimeSeconds: int64(time.Since(a.startedAt).Seconds()),
		LastUpdated:   time.Now(),
	}, nil
}

// Subscribe implements ObserveAdapter without allocating subscription state.
func (a *LocalAdapter) Subscribe(_ context.Context, req SubscribeRequest) (*SubscriptionHandle, error) {
	return pollingDescriptor(req)
}

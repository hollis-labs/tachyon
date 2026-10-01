package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// Routes/DTOs mirror the actual Nanite, Torque and Tether authored handlers.
// All plugin Init-based tests use this fixture, never the default live URLs.
func pollingTestConfig(t *testing.T, override http.HandlerFunc) map[string]string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("observe wrote upstream: %s", r.Method)
			http.Error(w, "writes forbidden", 405)
			return
		}
		if override != nil {
			override(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/health", "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/scheduler/status":
			w.Write([]byte(`{"enabled":false,"max_workers":3,"active_workers":0,"queue_depth":0}`))
		case "/api/sessions":
			w.Write([]byte(`[
   {"id":"new","status":"active","provider":"anthropic","model":"model","project_id":"p","created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T12:00:00Z","last_activity":"2026-10-01T12:00:00Z","title":"DO_NOT_EXPORT","metadata":"DO_NOT_EXPORT"},
   {"id":"old","status":"paused","created_at":"2026-10-01T08:00:00Z","updated_at":"2026-10-01T09:00:00Z","last_activity":"2026-10-01T09:00:00Z"}
  ]`))
		default:
			t.Errorf("invented route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return map[string]string{"nanite_url": srv.URL, "torque_url": srv.URL, "tether_url": srv.URL}
}

func TestPollingPluginReadsRealSources(t *testing.T) {
	p := &plugin{}
	_, err := p.Init(context.Background(), subprocess.InitParams{Config: pollingTestConfig(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"observe_activity", "observe_events", "observe_status"} {
		result, err := p.Command(context.Background(), subprocess.CommandRequest{Name: verb, Args: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		var env contract.ResultEnvelope
		if err := json.Unmarshal([]byte(result.Content), &env); err != nil || env.Status != contract.StatusOK {
			t.Fatalf("%s: %s, %v", verb, result.Content, err)
		}
		if strings.Contains(string(env.Data), "DO_NOT_EXPORT") {
			t.Fatalf("%s exported user content", verb)
		}
		switch verb {
		case "observe_activity":
			var entries []ActivityEntry
			json.Unmarshal(env.Data, &entries)
			found := false
			for _, entry := range entries {
				if entry.Kind == "session_snapshot" && entry.Source == "nanite" {
					found = true
				}
			}
			if !found {
				t.Fatal("no real Nanite activity")
			}
		case "observe_events":
			var entries []Event
			json.Unmarshal(env.Data, &entries)
			found := false
			for _, entry := range entries {
				if entry.Kind == "session_snapshot" && entry.Payload["session_id"] == "new" {
					found = true
				}
			}
			if !found {
				t.Fatal("no Nanite session snapshot event")
			}
		case "observe_status":
			var status StatusSummary
			json.Unmarshal(env.Data, &status)
			if status.HealthStatus != "healthy" || !status.SessionCountKnown || status.ActiveSessions != 1 {
				t.Fatalf("status: %+v", status)
			}
			for _, dep := range status.Dependencies {
				if dep.Status != "reachable" {
					t.Fatalf("dependency: %+v", dep)
				}
			}
		}
	}
}

func TestSnapshotFiltersAndOrdering(t *testing.T) {
	local := NewLocalAdapter(100)
	local.RecordActivity(ActivityEntry{Kind: "verb", Source: "observe-ops", Summary: "local"})
	local.RecordEvent(Event{Kind: "plugin_load", Source: "observe-ops"})
	adapter, err := NewPollingAdapter(local, pollingTestConfig(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	activity, err := adapter.ListActivity(ctx, ActivityFilter{Source: "nanite", Kind: "session_snapshot", Limit: 1})
	if err != nil || len(activity) != 1 || activity[0].Detail.(map[string]any)["session_id"] != "new" {
		t.Fatalf("activity: %+v, %v", activity, err)
	}
	events, err := adapter.ListEvents(ctx, EventFilter{Source: "nanite", Kind: "session_snapshot"})
	if err != nil || len(events) != 2 || events[0].Payload["session_id"] != "new" || !events[0].Timestamp.After(events[1].Timestamp) {
		t.Fatalf("events: %+v, %v", events, err)
	}
	since, err := adapter.ListEvents(ctx, EventFilter{Source: "nanite", Kind: "session_snapshot", SinceID: events[1].ID})
	if err != nil || len(since) != 1 || since[0].ID != events[0].ID {
		t.Fatalf("since cursor: %+v, %v", since, err)
	}
	localOnly, err := adapter.ListActivity(ctx, ActivityFilter{Source: "observe-ops"})
	if err != nil || len(localOnly) != 1 || localOnly[0].Summary != "local" {
		t.Fatalf("lost local feed: %+v, %v", localOnly, err)
	}
	empty, err := adapter.ListEvents(ctx, EventFilter{Source: "absent"})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty result: %+v, %v", empty, err)
	}
}

func TestDependenciesDegradeIndependently(t *testing.T) {
	for _, failed := range []string{"/api/health", "/health", "/api/v1/scheduler/status", "/api/sessions"} {
		t.Run(failed, func(t *testing.T) {
			config := pollingTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == failed {
					http.Error(w, "PRIVATE_BACKEND_DETAIL", http.StatusServiceUnavailable)
					return
				}
				switch r.URL.Path {
				case "/api/sessions":
					w.Write([]byte(`[{"id":"s","status":"active","updated_at":"2026-10-01T12:00:00Z"}]`))
				case "/api/v1/scheduler/status":
					w.Write([]byte(`{"enabled":true}`))
				default:
					w.Write([]byte(`{"status":"ok"}`))
				}
			})
			adapter, err := NewPollingAdapter(NewLocalAdapter(100), config)
			if err != nil {
				t.Fatal(err)
			}
			status, err := adapter.Status(context.Background())
			if err != nil || status.HealthStatus != "degraded" {
				t.Fatalf("status: %+v, %v", status, err)
			}
			failures := 0
			for _, dep := range status.Dependencies {
				if dep.Status == "unreachable" {
					failures++
					if dep.Error != "http_status_503" {
						t.Fatalf("failure: %+v", dep)
					}
				}
			}
			if failures != 1 {
				t.Fatalf("failure affected other sources: %+v", status.Dependencies)
			}
			if failed == "/api/sessions" {
				if status.SessionCountKnown || status.ActiveSessions != 0 {
					t.Fatalf("unknown sessions presented as known: %+v", status)
				}
			} else if !status.SessionCountKnown || status.ActiveSessions != 1 {
				t.Fatalf("healthy session source lost: %+v", status)
			}
			events, err := adapter.ListEvents(context.Background(), EventFilter{})
			if err != nil || len(events) == 0 {
				t.Fatalf("partial failure erased events: %+v, %v", events, err)
			}
			raw, _ := json.Marshal(events)
			if strings.Contains(string(raw), "PRIVATE_BACKEND_DETAIL") {
				t.Fatal("backend error body exported")
			}
		})
	}
}

func TestEmptySessionsStillProduceProbes(t *testing.T) {
	adapter, err := NewPollingAdapter(NewLocalAdapter(100), pollingTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sessions":
			w.Write([]byte("null"))
		case "/api/v1/scheduler/status":
			w.Write([]byte(`{"enabled":true}`))
		default:
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	activity, err := adapter.ListActivity(context.Background(), ActivityFilter{})
	if err != nil || len(activity) == 0 {
		t.Fatalf("empty running system activity: %+v, %v", activity, err)
	}
	events, err := adapter.ListEvents(context.Background(), EventFilter{})
	if err != nil || len(events) == 0 {
		t.Fatalf("empty running system events: %+v, %v", events, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbesAreParallelAndBounded(t *testing.T) {
	adapter, err := NewPollingAdapter(NewLocalAdapter(100), nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	pending := 0
	allEntered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > probeTimeout {
			t.Error("probe has no short deadline")
		}
		mu.Lock()
		pending++
		if pending == 4 {
			close(allEntered)
		}
		mu.Unlock()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	done := make(chan pollResult, 1)
	go func() { done <- adapter.poll(ctx) }()
	select {
	case <-allEntered:
		cancel()
	case <-time.After(2 * probeTimeout):
		cancel()
		t.Fatal("probes did not run independently in parallel")
	}
	result := <-done
	for _, dep := range result.dependencies {
		if dep.Status != "unreachable" {
			t.Fatalf("cancelled probe: %+v", dep)
		}
	}
}

func TestMalformedAndUnreachableSources(t *testing.T) {
	config := pollingTestConfig(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) })
	adapter, err := NewPollingAdapter(NewLocalAdapter(100), config)
	if err != nil {
		t.Fatal(err)
	}
	status, err := adapter.Status(context.Background())
	if err != nil || status.HealthStatus != "unhealthy" || status.SessionCountKnown {
		t.Fatalf("malformed status: %+v, %v", status, err)
	}
	for _, dep := range status.Dependencies {
		if dep.Error != "invalid_response" {
			t.Fatalf("malformed source: %+v", dep)
		}
	}
	if _, err := NewPollingAdapter(NewLocalAdapter(100), map[string]string{"nanite_url": "http://user:secret@localhost"}); err == nil {
		t.Fatal("credential URL accepted")
	}
}

func TestStalledSourceTimesOutWithoutHidingOthers(t *testing.T) {
	config := pollingTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			<-r.Context().Done()
		case "/api/sessions":
			w.Write([]byte("[]"))
		case "/api/v1/scheduler/status":
			w.Write([]byte(`{"enabled":false}`))
		default:
			w.Write([]byte(`{"status":"ok"}`))
		}
	})
	adapter, err := NewPollingAdapter(NewLocalAdapter(100), config)
	if err != nil {
		t.Fatal(err)
	}
	// Keep this test quick while exercising the real HTTP timeout path.
	adapter.client.Timeout = 25 * time.Millisecond
	status, err := adapter.Status(context.Background())
	if err != nil || status.HealthStatus != "degraded" {
		t.Fatalf("status: %+v, %v", status, err)
	}
	for _, dep := range status.Dependencies {
		expected := "reachable"
		if dep.Source == "tether" {
			expected = "unreachable"
		}
		if dep.Status != expected {
			t.Fatalf("source lost behind stalled peer: %+v", dep)
		}
	}
}

func TestPollingEndpointOverrides(t *testing.T) {
	t.Setenv("TACHYON_OBSERVE_NANITE_URL", "http://127.0.0.1:19090")
	adapter, err := NewPollingAdapter(NewLocalAdapter(100), nil)
	if err != nil || adapter.endpoints[0] != "http://127.0.0.1:19090" {
		t.Fatalf("environment override: %+v, %v", adapter, err)
	}
	adapter, err = NewPollingAdapter(NewLocalAdapter(100), map[string]string{"nanite_url": "http://127.0.0.1:29090/"})
	if err != nil || adapter.endpoints[0] != "http://127.0.0.1:29090" {
		t.Fatalf("Init config precedence: %+v, %v", adapter, err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const probeTimeout = 750 * time.Millisecond

// DependencyStatus reports endpoint reachability, not the health of every
// agent/tool behind it. Failures are independent and never hide other sources.
type DependencyStatus struct {
	Source    string    `json:"source"`
	Status    string    `json:"status"` // reachable or unreachable
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// PollingAdapter adds on-demand external snapshots to local plugin telemetry.
// It performs only GETs, keeps no external history, and never persists data.
type PollingAdapter struct {
	ObserveAdapter
	client    *http.Client
	endpoints []string
}

func NewPollingAdapter(local ObserveAdapter, config map[string]string) (*PollingAdapter, error) {
	endpoints := make([]string, 0, 4)
	for _, setting := range []struct{ key, fallback string }{
		{"nanite_url", "http://127.0.0.1:8090"},
		{"torque_url", "http://127.0.0.1:8990"},
		{"tether_url", "http://127.0.0.1:8947"},
	} {
		base := setting.fallback
		if override := strings.TrimSpace(os.Getenv("TACHYON_OBSERVE_" + strings.ToUpper(setting.key))); override != "" {
			base = override
		}
		if configured := strings.TrimSpace(config[setting.key]); configured != "" {
			base = configured
		}
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("%s must be an HTTP(S) base URL without credentials, query or fragment", setting.key)
		}
		endpoints = append(endpoints, strings.TrimRight(base, "/"))
	}
	return &PollingAdapter{
		ObserveAdapter: local, endpoints: endpoints,
		client: &http.Client{Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// DTO subset verified against Nanite internal/api/sessions_types.go.
// Session status is active/paused/archived; active does not imply executing.
type sessionSnapshot struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	ProjectID    string `json:"project_id"`
	LastActivity string `json:"last_activity"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type pollResult struct {
	dependencies []DependencyStatus
	sessions     []sessionSnapshot
}

func (a *PollingAdapter) get(ctx context.Context, endpoint string, out any) string {
	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, endpoint, nil)
	if err != nil {
		return "invalid_request"
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "request_failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("http_status_%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return "read_failed"
	}
	if len(body) > 4<<20 {
		return "response_too_large"
	}
	if err := json.Unmarshal(body, out); err != nil {
		return "invalid_response"
	}
	return ""
}

// All requests run concurrently: a dead source consumes at most one probe
// timeout for the whole read, rather than accumulating serial pipe delays.
func (a *PollingAdapter) poll(ctx context.Context) pollResult {
	result := pollResult{dependencies: make([]DependencyStatus, 4)}
	sources := []struct{ name, endpoint string }{
		{"nanite", a.endpoints[0] + "/api/health"},
		// Torque has no health route; scheduler/status is its cheap read-only probe.
		{"torque", a.endpoints[1] + "/api/v1/scheduler/status"},
		{"tether", a.endpoints[2] + "/api/health"},
		{"nanite_sessions", a.endpoints[0] + "/api/sessions"},
	}
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func(i int, name, endpoint string) {
			defer wg.Done()
			var failure string
			switch name {
			case "nanite_sessions":
				failure = a.get(ctx, endpoint, &result.sessions)
				if failure != "" {
					result.sessions = nil
				}
			case "torque":
				var scheduler struct {
					Enabled *bool `json:"enabled"`
				}
				failure = a.get(ctx, endpoint, &scheduler)
				if failure == "" && scheduler.Enabled == nil {
					failure = "invalid_response"
				}
			default:
				var health struct {
					Status string `json:"status"`
				}
				failure = a.get(ctx, endpoint, &health)
				if failure == "" && health.Status != "ok" {
					failure = "invalid_response"
				}
			}
			status := "reachable"
			if failure != "" {
				status = "unreachable"
			}
			result.dependencies[i] = DependencyStatus{Source: name, Status: status, Error: failure, CheckedAt: time.Now().UTC()}
		}(i, source.name, source.endpoint)
	}
	wg.Wait()
	return result
}

func (s sessionSnapshot) timestamp() time.Time {
	var latest time.Time
	for _, raw := range []string{s.LastActivity, s.UpdatedAt, s.CreatedAt} {
		if stamp, err := time.Parse(time.RFC3339Nano, raw); err == nil && stamp.After(latest) {
			latest = stamp
		}
	}
	return latest
}

func (s sessionSnapshot) snapshotID() string { return "nanite:session:" + s.ID + ":" + s.UpdatedAt }
func (s sessionSnapshot) detail() map[string]any {
	// Deliberately excludes titles, metadata, messages and other user content.
	return map[string]any{"session_id": s.ID, "status": s.Status, "provider": s.Provider, "model": s.Model, "project_id": s.ProjectID}
}

func (a *PollingAdapter) ListActivity(ctx context.Context, filter ActivityFilter) ([]ActivityEntry, error) {
	// Query local data without a limit so global ordering/filtering applies once.
	local, err := a.ObserveAdapter.ListActivity(ctx, ActivityFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	snapshot := a.poll(ctx)
	entries := append([]ActivityEntry{}, local...)
	for _, dep := range snapshot.dependencies {
		entries = append(entries, ActivityEntry{ID: "probe:" + dep.Source, Timestamp: dep.CheckedAt, Kind: "dependency_probe",
			Source: dep.Source, Actor: "observe-ops", Summary: dep.Source + " " + dep.Status, Detail: dep})
	}
	for _, session := range snapshot.sessions {
		entries = append(entries, ActivityEntry{ID: session.snapshotID(), Timestamp: session.timestamp(), Kind: "session_snapshot",
			Source: "nanite", Actor: "nanite", Summary: "session " + session.ID + " snapshot", Detail: session.detail()})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Timestamp.After(entries[j].Timestamp) })
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]ActivityEntry, 0)
	for _, entry := range entries {
		if filter.SinceID != "" && entry.ID == filter.SinceID {
			break
		}
		if filter.Source != "" && entry.Source != filter.Source {
			continue
		}
		if filter.Kind != "" && entry.Kind != filter.Kind {
			continue
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (a *PollingAdapter) ListEvents(ctx context.Context, filter EventFilter) ([]Event, error) {
	local, err := a.ObserveAdapter.ListEvents(ctx, EventFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	snapshot := a.poll(ctx)
	events := append([]Event{}, local...)
	for _, dep := range snapshot.dependencies {
		events = append(events, Event{ID: "probe:" + dep.Source, Timestamp: dep.CheckedAt, Kind: "dependency_probe",
			Source: dep.Source, Payload: map[string]any{"status": dep.Status, "error": dep.Error}})
	}
	for _, session := range snapshot.sessions {
		events = append(events, Event{ID: session.snapshotID(), Timestamp: session.timestamp(), Kind: "session_snapshot", Source: "nanite", Payload: session.detail()})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp.After(events[j].Timestamp) })
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]Event, 0)
	for _, event := range events {
		if filter.SinceID != "" && event.ID == filter.SinceID {
			break
		}
		if filter.Source != "" && event.Source != filter.Source {
			continue
		}
		if filter.Kind != "" && event.Kind != filter.Kind {
			continue
		}
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (a *PollingAdapter) Status(ctx context.Context) (*StatusSummary, error) {
	status, err := a.ObserveAdapter.Status(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := a.poll(ctx)
	status.Dependencies = snapshot.dependencies
	unreachable := 0
	for _, dep := range snapshot.dependencies {
		if dep.Status == "unreachable" {
			unreachable++
		}
	}
	if unreachable > 0 && status.HealthStatus == "healthy" {
		status.HealthStatus = "degraded"
	}
	if unreachable == len(snapshot.dependencies) {
		status.HealthStatus = "unhealthy"
	}
	status.ErrorCount += unreachable
	status.SessionCountKnown = snapshot.dependencies[3].Status == "reachable"
	for _, session := range snapshot.sessions {
		if session.Status == "active" {
			status.ActiveSessions++
		}
	}
	status.LastUpdated = time.Now().UTC()
	return status, nil
}

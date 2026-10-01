package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NaniteLaunchAdapter implements LaunchAdapter using Nanite's HTTP API
// for agent profile resolution and session creation, with an in-memory
// store for launch state. The store is process-scoped: launches survive
// for the lifetime of the plugin binary but not across restarts.
// This is intentional for MVP — persistent launch storage would move
// to a durable backend (SQLite, Torque) in a later iteration.
type NaniteLaunchAdapter struct {
	baseURL    string
	httpClient *http.Client

	mu       sync.RWMutex
	launches map[string]*Launch // keyed by launch ID
	counter  int                // monotonic ID counter
}

// NewNaniteLaunchAdapter creates a new adapter targeting the given
// Nanite API URL (e.g. "http://localhost:8090").
func NewNaniteLaunchAdapter(baseURL string) *NaniteLaunchAdapter {
	return &NaniteLaunchAdapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		launches: make(map[string]*Launch),
	}
}

// naniteAgent is the wire DTO for an agent profile from Nanite.
type naniteAgent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Enabled bool   `json:"enabled"`
}

// naniteCreateSessionRequest matches Nanite's CreateSessionRequest type
// (POST /api/sessions). agent_id goes in the body, not the URL path.
type naniteCreateSessionRequest struct {
	AgentID   string `json:"agent_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
}

// naniteSession represents Nanite's session response.
type naniteSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
}

func (a *NaniteLaunchAdapter) nextID() string {
	a.counter++
	return fmt.Sprintf("launch-%d-%d", time.Now().Unix(), a.counter)
}

// doJSON performs an HTTP request with JSON body and response handling.
func (a *NaniteLaunchAdapter) doJSON(ctx context.Context, method, path string, body any, dst any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %s %s: status %d: %s", method, path, resp.StatusCode, string(b))
	}

	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// resolveAgent fetches the agent profile from Nanite for name resolution.
// The GET endpoint wraps the agent in {"agent": ...} (matching agent-ops).
func (a *NaniteLaunchAdapter) resolveAgent(ctx context.Context, agentID string) (*naniteAgent, error) {
	var wrapper struct {
		Agent naniteAgent `json:"agent"`
	}
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+agentID, nil, &wrapper); err != nil {
		return nil, fmt.Errorf("resolve agent %s: %w", agentID, err)
	}
	return &wrapper.Agent, nil
}

// Prepare assembles a launch intent, validates the agent exists via
// Nanite, and stores it in state "prepared" without executing.
func (a *NaniteLaunchAdapter) Prepare(ctx context.Context, req PrepareRequest) (*Launch, error) {
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	// Resolve agent profile from Nanite to validate it exists and
	// capture the display name.
	agent, err := a.resolveAgent(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now().UTC()
	launch := &Launch{
		ID:        a.nextID(),
		AgentID:   req.AgentID,
		AgentName: agent.Name,
		Provider:  req.Provider,
		Model:     req.Model,
		ProjectID: req.ProjectID,
		State:     LaunchStatePrepared,
		Config:    req.Config,
		CreatedAt: now,
		UpdatedAt: now,
	}
	a.launches[launch.ID] = launch
	return cloneLaunch(launch), nil
}

// Execute commits a prepared launch by creating a session on Nanite.
// Nanite sessions are created and immediately usable — there is no
// separate launch step (see agent-ops/nanite_adapter.go LaunchSession).
//
// The lock is dropped around the Nanite call. A concurrent Cancel may
// transition the launch to cancelled while the call is in flight; on
// return we only update if the state is still executing.
func (a *NaniteLaunchAdapter) Execute(ctx context.Context, req ExecuteRequest) (*Launch, error) {
	if req.LaunchID == "" {
		return nil, fmt.Errorf("launch_id is required")
	}

	a.mu.Lock()
	launch, ok := a.launches[req.LaunchID]
	if !ok {
		a.mu.Unlock()
		return nil, fmt.Errorf("launch %s not found", req.LaunchID)
	}
	if launch.State != LaunchStatePrepared {
		a.mu.Unlock()
		return nil, fmt.Errorf("launch %s is in state %s, expected prepared", req.LaunchID, launch.State)
	}
	launch.State = LaunchStateExecuting
	now := time.Now().UTC()
	launch.UpdatedAt = now
	launch.StartedAt = &now
	a.mu.Unlock()

	// Create session via Nanite POST /api/sessions (agent_id in body).
	sessionReq := naniteCreateSessionRequest{
		AgentID:   launch.AgentID,
		ProjectID: launch.ProjectID,
		Model:     launch.Model,
		Provider:  launch.Provider,
	}
	var sessionResp naniteSession
	err := a.doJSON(ctx, http.MethodPost, "/api/sessions", sessionReq, &sessionResp)

	a.mu.Lock()
	defer a.mu.Unlock()

	// A concurrent Cancel may have transitioned the launch while
	// we were waiting on Nanite. Only update if still executing.
	if launch.State != LaunchStateExecuting {
		return cloneLaunch(launch), nil
	}

	if err != nil {
		launch.State = LaunchStateFailed
		launch.Error = err.Error()
		endNow := time.Now().UTC()
		launch.EndedAt = &endNow
		launch.UpdatedAt = endNow
		return cloneLaunch(launch), nil
	}

	if sessionResp.ID == "" {
		launch.State = LaunchStateFailed
		launch.Error = "session ID not found in response"
		endNow := time.Now().UTC()
		launch.EndedAt = &endNow
		launch.UpdatedAt = endNow
		return cloneLaunch(launch), nil
	}

	launch.State = LaunchStateRunning
	launch.SessionID = sessionResp.ID
	launch.UpdatedAt = time.Now().UTC()

	return cloneLaunch(launch), nil
}

// Cancel aborts a pending or running launch.
func (a *NaniteLaunchAdapter) Cancel(ctx context.Context, req CancelRequest) (*Launch, error) {
	if req.LaunchID == "" {
		return nil, fmt.Errorf("launch_id is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	launch, ok := a.launches[req.LaunchID]
	if !ok {
		return nil, fmt.Errorf("launch %s not found", req.LaunchID)
	}

	switch launch.State {
	case LaunchStatePrepared, LaunchStateExecuting, LaunchStateRunning:
		// These states can be cancelled
	default:
		return nil, fmt.Errorf("launch %s is in state %s, cannot cancel", req.LaunchID, launch.State)
	}

	now := time.Now().UTC()
	launch.State = LaunchStateCancelled
	if req.Reason != "" {
		launch.Error = "cancelled: " + req.Reason
	} else {
		launch.Error = "cancelled"
	}
	launch.EndedAt = &now
	launch.UpdatedAt = now

	return cloneLaunch(launch), nil
}

// Read returns the full details of a launch.
func (a *NaniteLaunchAdapter) Read(_ context.Context, req ReadRequest) (*Launch, error) {
	if req.LaunchID == "" {
		return nil, fmt.Errorf("launch_id is required")
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	launch, ok := a.launches[req.LaunchID]
	if !ok {
		return nil, fmt.Errorf("launch %s not found", req.LaunchID)
	}
	return cloneLaunch(launch), nil
}

// List returns launches matching the given filters.
func (a *NaniteLaunchAdapter) List(_ context.Context, req ListRequest) ([]Launch, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var results []Launch
	for _, launch := range a.launches {
		if req.AgentID != "" && launch.AgentID != req.AgentID {
			continue
		}
		if req.State != "" && string(launch.State) != req.State {
			continue
		}
		if req.Provider != "" && launch.Provider != req.Provider {
			continue
		}
		results = append(results, *cloneLaunch(launch))
	}

	// Sort by creation time, newest first
	sortLaunchesByCreatedDesc(results)

	if req.Limit > 0 && len(results) > req.Limit {
		results = results[:req.Limit]
	}

	return results, nil
}

// Status returns a lightweight status snapshot for polling.
func (a *NaniteLaunchAdapter) Status(_ context.Context, req StatusRequest) (*LaunchStatus, error) {
	if req.LaunchID == "" {
		return nil, fmt.Errorf("launch_id is required")
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	launch, ok := a.launches[req.LaunchID]
	if !ok {
		return nil, fmt.Errorf("launch %s not found", req.LaunchID)
	}
	return &LaunchStatus{
		LaunchID:  launch.ID,
		State:     launch.State,
		SessionID: launch.SessionID,
		Error:     launch.Error,
		UpdatedAt: launch.UpdatedAt,
	}, nil
}

// cloneLaunch returns a shallow copy of a launch so callers cannot
// mutate the store's internal state.
func cloneLaunch(l *Launch) *Launch {
	c := *l
	if l.Config != nil {
		c.Config = make(map[string]any, len(l.Config))
		for k, v := range l.Config {
			c.Config[k] = v
		}
	}
	return &c
}

// sortLaunchesByCreatedDesc sorts a launch slice by CreatedAt descending.
func sortLaunchesByCreatedDesc(launches []Launch) {
	for i := 1; i < len(launches); i++ {
		for j := i; j > 0 && launches[j].CreatedAt.After(launches[j-1].CreatedAt); j-- {
			launches[j], launches[j-1] = launches[j-1], launches[j]
		}
	}
}

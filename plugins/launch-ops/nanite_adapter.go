package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NaniteLaunchAdapter uses Nanite for agent profiles and session creation.
// Launch intent and checkpoints live in the shared durable store.
type NaniteLaunchAdapter struct {
	*launchLifecycle
	baseURL    string
	httpClient *http.Client
}

func NewNaniteLaunchAdapter(baseURL string, store *LaunchStore) *NaniteLaunchAdapter {
	a := &NaniteLaunchAdapter{baseURL: strings.TrimRight(baseURL, "/"), httpClient: providerHTTPClient(http.DefaultTransport, 30*time.Second)}
	a.launchLifecycle = &launchLifecycle{store: store, backend: "nanite"}
	a.resolve = func(ctx context.Context, req PrepareRequest) (PrepareRequest, string, error) {
		agent, err := a.resolveAgent(ctx, req.AgentID)
		if err != nil {
			return req, "", err
		}
		return req, agent.Name, nil
	}
	a.start = func(ctx context.Context, l *Launch) (string, LaunchState, error) {
		var result naniteSession
		provider := l.Provider
		if provider == "nanite" {
			provider = ""
		}
		err := a.doJSON(ctx, http.MethodPost, "/api/sessions", naniteCreateSessionRequest{AgentID: l.AgentID, ProjectID: l.ProjectID, Model: l.Model, Provider: provider}, &result)
		if err != nil {
			return "", LaunchStateFailed, err
		}
		if result.ID == "" {
			return "", LaunchStateFailed, fmt.Errorf("session ID not found in response")
		}
		return result.ID, LaunchStateRunning, nil
	}
	a.stop = func(ctx context.Context, l *Launch) error {
		// Verified Nanite route: archive retains conversation data and invokes
		// CloseAgentSession plus orphan-process cleanup in handleDeleteSession.
		return a.doJSON(ctx, http.MethodDelete, "/api/sessions/"+url.PathEscape(l.SessionID), nil, nil)
	}
	return a
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
		return fmt.Errorf("invalid provider request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return safeProviderError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &providerResponseError{status: resp.StatusCode}
	}

	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("invalid provider JSON response")
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
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+url.PathEscape(agentID), nil, &wrapper); err != nil {
		return nil, fmt.Errorf("resolve agent %s: %w", agentID, err)
	}
	return &wrapper.Agent, nil
}

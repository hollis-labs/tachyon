package main

import (
	"context"
	"testing"
)

// TestAdapterInterface verifies that NaniteAdapter implements AgentAdapter.
func TestAdapterInterface(t *testing.T) {
	var _ AgentAdapter = (*NaniteAdapter)(nil)
}

// TestNaniteAdapterCreation tests that NewNaniteAdapter initializes correctly.
func TestNaniteAdapterCreation(t *testing.T) {
	mcpURL := "http://localhost:55970/mcp"
	adapter := NewNaniteAdapter(mcpURL)

	if adapter == nil {
		t.Fatal("NewNaniteAdapter returned nil")
	}

	if adapter.mcpURL != mcpURL {
		t.Errorf("expected mcpURL %q, got %q", mcpURL, adapter.mcpURL)
	}

	if adapter.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

// TestAgentTypeStructure validates the Agent struct can be marshaled/unmarshaled.
func TestAgentTypeStructure(t *testing.T) {
	agent := Agent{
		ID:           "test-agent",
		Name:         "Test Agent",
		SystemPrompt: "You are a test agent",
		AgentPrompt:  "Test persona",
		Roles:        []string{"tester", "validator"},
		Skills:       []string{"testing", "validation"},
		Layer:        "project",
		FilePath:     "/path/to/agent.yaml",
	}

	if agent.ID != "test-agent" {
		t.Errorf("expected ID %q, got %q", "test-agent", agent.ID)
	}

	if len(agent.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(agent.Roles))
	}

	if len(agent.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(agent.Skills))
	}
}

// TestCreateAgentRequest validates the CreateAgentRequest structure.
func TestCreateAgentRequest(t *testing.T) {
	req := CreateAgentRequest{
		ID:           "new-agent",
		Name:         "New Agent",
		SystemPrompt: "System prompt",
		AgentPrompt:  "Agent prompt",
		Roles:        []string{"role1"},
		Skills:       []string{"skill1"},
		Scope:        "project",
		ProjectID:    "PRJ-123",
	}

	if req.ID != "new-agent" {
		t.Errorf("expected ID %q, got %q", "new-agent", req.ID)
	}

	if req.Scope != "project" {
		t.Errorf("expected scope %q, got %q", "project", req.Scope)
	}
}

// TestUpdateAgentRequest validates the UpdateAgentRequest structure.
func TestUpdateAgentRequest(t *testing.T) {
	newName := "Updated Name"
	newPrompt := "Updated prompt"

	req := UpdateAgentRequest{
		Name:         &newName,
		SystemPrompt: &newPrompt,
		Roles:        []string{"new-role"},
		Skills:       []string{"new-skill"},
	}

	if req.Name == nil || *req.Name != "Updated Name" {
		t.Error("Name pointer not set correctly")
	}

	if req.SystemPrompt == nil || *req.SystemPrompt != "Updated prompt" {
		t.Error("SystemPrompt pointer not set correctly")
	}
}

// TestCreateSessionRequest validates the CreateSessionRequest structure.
func TestCreateSessionRequest(t *testing.T) {
	req := CreateSessionRequest{
		LaunchID:     "launch-123",
		AgentFile:    "/path/to/agent.yaml",
		BootProfile:  "/path/to/boot.yaml",
		BootPrompt:   "Boot prompt",
		PromptAppend: "Additional instructions",
	}

	if req.LaunchID != "launch-123" {
		t.Errorf("expected LaunchID %q, got %q", "launch-123", req.LaunchID)
	}
}

// TestLaunchResult validates the LaunchResult structure.
func TestLaunchResult(t *testing.T) {
	result := LaunchResult{
		SessionID:     "session-456",
		WorkspacePath: "/workspace",
		LogPath:       "/logs/session.log",
	}

	if result.SessionID != "session-456" {
		t.Errorf("expected SessionID %q, got %q", "session-456", result.SessionID)
	}
}

// MockAdapter is a minimal mock implementation for testing.
type MockAdapter struct {
	ListAgentsFunc    func(ctx context.Context) ([]Agent, error)
	GetAgentFunc      func(ctx context.Context, id string) (*Agent, error)
	CreateAgentFunc   func(ctx context.Context, req CreateAgentRequest) (*Agent, error)
	UpdateAgentFunc   func(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error)
	DeleteAgentFunc   func(ctx context.Context, id string) error
	CreateSessionFunc func(ctx context.Context, req CreateSessionRequest) (string, error)
	LaunchSessionFunc func(ctx context.Context, sessionID string) (*LaunchResult, error)
}

func (m *MockAdapter) ListAgents(ctx context.Context) ([]Agent, error) {
	if m.ListAgentsFunc != nil {
		return m.ListAgentsFunc(ctx)
	}
	return nil, nil
}

func (m *MockAdapter) GetAgent(ctx context.Context, id string) (*Agent, error) {
	if m.GetAgentFunc != nil {
		return m.GetAgentFunc(ctx, id)
	}
	return nil, nil
}

func (m *MockAdapter) CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	if m.CreateAgentFunc != nil {
		return m.CreateAgentFunc(ctx, req)
	}
	return nil, nil
}

func (m *MockAdapter) UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error) {
	if m.UpdateAgentFunc != nil {
		return m.UpdateAgentFunc(ctx, id, req)
	}
	return nil, nil
}

func (m *MockAdapter) DeleteAgent(ctx context.Context, id string) error {
	if m.DeleteAgentFunc != nil {
		return m.DeleteAgentFunc(ctx, id)
	}
	return nil
}

func (m *MockAdapter) CreateSession(ctx context.Context, req CreateSessionRequest) (string, error) {
	if m.CreateSessionFunc != nil {
		return m.CreateSessionFunc(ctx, req)
	}
	return "", nil
}

func (m *MockAdapter) LaunchSession(ctx context.Context, sessionID string) (*LaunchResult, error) {
	if m.LaunchSessionFunc != nil {
		return m.LaunchSessionFunc(ctx, sessionID)
	}
	return nil, nil
}

// TestMockAdapter verifies the mock implements the interface.
func TestMockAdapter(t *testing.T) {
	var _ AgentAdapter = (*MockAdapter)(nil)
}

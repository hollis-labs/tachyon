package main

import (
	"context"
	"testing"
)

// TestAdapterInterface verifies that NaniteAdapter implements AgentAdapter.
func TestAdapterInterface(t *testing.T) {
	var _ AgentAdapter = (*NaniteAdapter)(nil)
}

// TestNaniteAdapterCreation verifies the adapter can be instantiated.
func TestNaniteAdapterCreation(t *testing.T) {
	adapter := NewNaniteAdapter("http://localhost:8090")
	if adapter == nil {
		t.Fatal("NewNaniteAdapter returned nil")
	}
	if adapter.baseURL != "http://localhost:8090" {
		t.Errorf("expected baseURL http://localhost:8090, got %s", adapter.baseURL)
	}
	if adapter.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

// TestNaniteAdapterListAgents tests listing agents from the real Nanite API.
// This is an integration test - it requires Nanite to be running at localhost:8090.
func TestNaniteAdapterListAgents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	adapter := NewNaniteAdapter("http://localhost:8090")
	ctx := context.Background()

	agents, err := adapter.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}

	// Should have at least some agents (Nanite has built-in agents)
	if len(agents) == 0 {
		t.Error("ListAgents returned empty list - expected at least some agents")
	}

	// Verify agent structure
	for _, agent := range agents {
		if agent.ID == "" {
			t.Error("agent has empty ID")
		}
		if agent.Name == "" {
			t.Error("agent has empty Name")
		}
	}
}

// TestNaniteAdapterCRUD tests create/get/update/delete operations
// against the real Nanite API.
func TestNaniteAdapterCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	adapter := NewNaniteAdapter("http://localhost:8090")
	ctx := context.Background()

	// Create a test agent
	createReq := CreateAgentRequest{
		Name:         "Test CRUD Agent",
		SystemPrompt: "You are a test agent for verifying CRUD operations.",
		Description:  "This agent exists only for testing purposes.",
	}

	created, err := adapter.CreateAgent(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateAgent returned agent with empty ID")
	}
	if created.Name != createReq.Name {
		t.Errorf("expected name %q, got %q", createReq.Name, created.Name)
	}

	// Clean up at the end
	defer func() {
		if err := adapter.DeleteAgent(ctx, created.ID); err != nil {
			t.Errorf("cleanup: DeleteAgent failed: %v", err)
		}
	}()

	// Get the agent by ID
	retrieved, err := adapter.GetAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}
	if retrieved.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, retrieved.ID)
	}
	if retrieved.Name != created.Name {
		t.Errorf("expected name %q, got %q", created.Name, retrieved.Name)
	}

	// Update the agent
	newName := "Updated Test Agent"
	updateReq := UpdateAgentRequest{
		Name: &newName,
	}
	updated, err := adapter.UpdateAgent(ctx, created.ID, updateReq)
	if err != nil {
		t.Fatalf("UpdateAgent failed: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("expected updated name %q, got %q", newName, updated.Name)
	}

	// Verify the update persisted
	retrieved2, err := adapter.GetAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAgent after update failed: %v", err)
	}
	if retrieved2.Name != newName {
		t.Errorf("expected persisted name %q, got %q", newName, retrieved2.Name)
	}

	// Delete is handled by defer cleanup above
}

// TestNaniteAdapterCreateSession tests session creation against the real Nanite API.
func TestNaniteAdapterCreateSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	adapter := NewNaniteAdapter("http://localhost:8090")
	ctx := context.Background()

	// Create a session (agent_id is optional - Nanite will use a default)
	sessionReq := CreateSessionRequest{
		Model:    "claude-sonnet-4-5",
		Provider: "anthropic",
	}

	sessionID, err := adapter.CreateSession(ctx, sessionReq)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sessionID == "" {
		t.Fatal("CreateSession returned empty session ID")
	}

	// Launch the session (for Nanite this is a no-op, but tests the interface)
	result, err := adapter.LaunchSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("LaunchSession failed: %v", err)
	}
	if result.SessionID != sessionID {
		t.Errorf("expected session ID %q, got %q", sessionID, result.SessionID)
	}
}

// TestAgentTypeStructure verifies the Agent type has expected fields.
func TestAgentTypeStructure(t *testing.T) {
	agent := Agent{
		ID:           "test-id",
		Name:         "Test Agent",
		SystemPrompt: "Test prompt",
		Layer:        "managed",
	}

	if agent.ID != "test-id" {
		t.Errorf("expected ID test-id, got %s", agent.ID)
	}
	if agent.Name != "Test Agent" {
		t.Errorf("expected Name 'Test Agent', got %s", agent.Name)
	}
	if agent.SystemPrompt != "Test prompt" {
		t.Errorf("expected SystemPrompt 'Test prompt', got %s", agent.SystemPrompt)
	}
	if agent.Layer != "managed" {
		t.Errorf("expected Layer 'managed', got %s", agent.Layer)
	}
}

// TestCreateAgentRequest validates the CreateAgentRequest structure.
func TestCreateAgentRequest(t *testing.T) {
	req := CreateAgentRequest{
		Name:         "New Agent",
		SystemPrompt: "System prompt",
		Description:  "Agent description",
	}

	if req.Name != "New Agent" {
		t.Errorf("expected Name %q, got %q", "New Agent", req.Name)
	}
	if req.SystemPrompt != "System prompt" {
		t.Errorf("expected SystemPrompt %q, got %q", "System prompt", req.SystemPrompt)
	}
}

// TestUpdateAgentRequest validates the UpdateAgentRequest structure.
func TestUpdateAgentRequest(t *testing.T) {
	newName := "Updated Name"
	newPrompt := "Updated prompt"

	req := UpdateAgentRequest{
		Name:         &newName,
		SystemPrompt: &newPrompt,
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
		ProjectID: "proj-123",
		Model:     "claude-sonnet-4-5",
		Provider:  "anthropic",
		AgentID:   "agent-456",
	}

	if req.ProjectID != "proj-123" {
		t.Errorf("expected ProjectID %q, got %q", "proj-123", req.ProjectID)
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Errorf("expected Model %q, got %q", "claude-sonnet-4-5", req.Model)
	}
}

// TestLaunchResult validates the LaunchResult structure.
func TestLaunchResult(t *testing.T) {
	result := LaunchResult{
		SessionID: "session-456",
	}

	if result.SessionID != "session-456" {
		t.Errorf("expected SessionID %q, got %q", "session-456", result.SessionID)
	}
}

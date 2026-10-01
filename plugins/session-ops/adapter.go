package main

import "context"

// SessionAdapter keeps session state and execution with the provider.
type SessionAdapter interface {
	Create(context.Context, CreateSessionRequest) (Session, error)
	Read(context.Context, string) (Session, error)
	List(context.Context, ListSessionsRequest) (SessionPage, error)
	Attach(context.Context, string) (ConnectionInfo, error)
	Stop(context.Context, string) error
	Submit(context.Context, string, string) error
	History(context.Context, HistoryRequest) (HistoryPage, error)
}

type CreateSessionRequest struct {
	LaunchID       string `json:"launch_id"`
	BootPrompt     string `json:"boot_prompt,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type Session struct {
	ID           string  `json:"id"`
	LaunchID     string  `json:"launch_id"`
	AgentID      string  `json:"agent_id"`
	ProjectID    string  `json:"project_id"`
	ProviderID   string  `json:"provider_id"`
	ProviderKind string  `json:"provider_kind,omitempty"`
	State        string  `json:"state"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	EndedAt      *string `json:"ended_at,omitempty"`
}

type ListSessionsRequest struct {
	AgentID  string `json:"agent_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Provider string `json:"provider,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

type SessionPage struct {
	Sessions   []Session `json:"sessions"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// ConnectionInfo describes a Tether attachment for a server-side consumer.
// It contains no browser-facing provider URL; streaming requires a separate
// Tether client connection, not the serial Tachyon plugin command pipe.
type ConnectionInfo struct {
	SessionID  string `json:"session_id"`
	ProviderID string `json:"provider_id"`
	State      string `json:"state"`
	Transport  string `json:"transport"`
	Streaming  bool   `json:"streaming"`
}

type HistoryRequest struct {
	ID       string `json:"id"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   int64  `json:"cursor,omitempty"`
	SinceSeq int64  `json:"since_seq,omitempty"`
}

type HistoryEvent struct {
	Seq         int64  `json:"seq"`
	Timestamp   string `json:"timestamp"`
	Scope       string `json:"scope"`
	Kind        string `json:"kind"`
	SessionID   string `json:"session_id,omitempty"`
	PayloadJSON string `json:"payload_json,omitempty"`
}

type HistoryPage struct {
	Events     []HistoryEvent `json:"events"`
	NextCursor int64          `json:"next_cursor,omitempty"`
}

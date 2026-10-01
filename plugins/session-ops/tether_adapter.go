package main

import (
	"context"
	"fmt"
	"time"

	tether "github.com/hollis-labs/go-tether-client"
)

// Turns can outlast ordinary reads, but must release the serial plugin pipe.
const submitTimeout = 30 * time.Second

type TetherAdapter struct {
	client        *tether.Client
	submitTimeout time.Duration
}

func NewTetherAdapter(listenAddr string) (*TetherAdapter, error) {
	client, err := tether.New(listenAddr)
	if err != nil {
		return nil, err
	}
	return &TetherAdapter{client: client, submitTimeout: submitTimeout}, nil
}

func sessionFromTether(s tether.Session) Session {
	return Session{ID: s.ID, LaunchID: s.LaunchID, AgentID: s.LogicalAgentID,
		ProjectID: s.ProjectID, ProviderID: s.ProviderID, ProviderKind: s.ProviderKind,
		State: s.State, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, EndedAt: s.EndedAt}
}

// Create allocates the session; launch orchestration belongs to launch-ops.
func (a *TetherAdapter) Create(ctx context.Context, req CreateSessionRequest) (Session, error) {
	created, err := a.client.CreateSessionWithInput(ctx, tether.LaunchRequest{
		Launch: req.LaunchID, BootPrompt: req.BootPrompt, IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return Session{}, err
	}
	return a.Read(ctx, created.ID)
}

func (a *TetherAdapter) Read(ctx context.Context, id string) (Session, error) {
	s, err := a.client.GetSession(ctx, id)
	if err != nil {
		return Session{}, err
	}
	return sessionFromTether(s), nil
}

// List preserves Tether pagination. Agent/provider filters apply to each
// provider page; an empty filtered page may still have a next_cursor.
func (a *TetherAdapter) List(ctx context.Context, req ListSessionsRequest) (SessionPage, error) {
	page, err := a.client.ListSessions(ctx, tether.ListSessionsOptions{Limit: req.Limit, Cursor: req.Cursor, State: req.Status})
	if err != nil {
		return SessionPage{}, err
	}
	out := SessionPage{Sessions: make([]Session, 0, len(page.Sessions)), NextCursor: page.NextCursor}
	for _, s := range page.Sessions {
		if req.AgentID != "" && s.LogicalAgentID != req.AgentID {
			continue
		}
		if req.Provider != "" && s.ProviderID != req.Provider {
			continue
		}
		if req.Status != "" && s.State != req.Status {
			continue
		}
		out.Sessions = append(out.Sessions, sessionFromTether(s))
	}
	return out, nil
}

// Attach validates the session and returns connection metadata. It does not
// open AttachSession's long-lived output stream over the serial plugin pipe.
func (a *TetherAdapter) Attach(ctx context.Context, id string) (ConnectionInfo, error) {
	s, err := a.Read(ctx, id)
	if err != nil {
		return ConnectionInfo{}, err
	}
	if s.State != "running" {
		return ConnectionInfo{}, fmt.Errorf("session %q is not running (state %q)", id, s.State)
	}
	return ConnectionInfo{SessionID: s.ID, ProviderID: s.ProviderID, State: s.State, Transport: "tether", Streaming: true}, nil
}

func (a *TetherAdapter) Stop(ctx context.Context, id string) error {
	return a.client.StopSession(ctx, id)
}
func (a *TetherAdapter) Submit(ctx context.Context, id, text string) error {
	ctx, cancel := context.WithTimeout(ctx, a.submitTimeout)
	defer cancel()
	return a.client.SendTurn(ctx, id, text)
}

// History returns the provider's session event log, including turn events,
// without storing or reconstructing conversation state in Tachyon.
func (a *TetherAdapter) History(ctx context.Context, req HistoryRequest) (HistoryPage, error) {
	page, err := a.client.ListSessionEvents(ctx, req.ID, tether.EventListOptions{Limit: req.Limit, Cursor: req.Cursor, SinceSeq: req.SinceSeq})
	if err != nil {
		return HistoryPage{}, err
	}
	out := HistoryPage{Events: make([]HistoryEvent, 0, len(page.Events)), NextCursor: page.NextCursor}
	for _, e := range page.Events {
		out.Events = append(out.Events, HistoryEvent{Seq: e.Seq, Timestamp: e.At, Scope: e.Scope, Kind: e.Kind, SessionID: e.SessionID, PayloadJSON: e.PayloadJSON})
	}
	return out, nil
}

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// launchLifecycle shares durable intent handling between provider adapters.
// Provider calls happen outside store transactions so reads/cancels stay usable.
type launchLifecycle struct {
	store      *LaunchStore
	backend    string
	resolve    func(context.Context, PrepareRequest) (PrepareRequest, string, error)
	start      func(context.Context, *Launch) (string, LaunchState, error)
	stop       func(context.Context, *Launch) error
	refresh    func(context.Context, *Launch) (*Launch, error)
	replaySafe bool
	mu         sync.Mutex
	inFlight   map[string]bool
	cancelling map[string]bool
}

func (a *launchLifecycle) Prepare(ctx context.Context, req PrepareRequest) (*Launch, error) {
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	resolved, name, err := a.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	l := &Launch{ID: "launch-" + hex.EncodeToString(id[:]), AgentID: resolved.AgentID, AgentName: name, Backend: a.backend, Provider: resolved.Provider, Model: resolved.Model, ProjectID: resolved.ProjectID, Config: resolved.Config, State: LaunchStatePrepared, CreatedAt: now, UpdatedAt: now}
	if err = a.store.Create(ctx, l); err != nil {
		return nil, err
	}
	return a.store.Get(ctx, l.ID)
}

func (a *launchLifecycle) Execute(ctx context.Context, req ExecuteRequest) (*Launch, error) {
	a.mu.Lock()
	if a.inFlight == nil {
		a.inFlight = map[string]bool{}
	}
	if a.inFlight[req.LaunchID] || a.cancelling[req.LaunchID] {
		a.mu.Unlock()
		return nil, fmt.Errorf("launch %s is already executing", req.LaunchID)
	}
	a.inFlight[req.LaunchID] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.inFlight, req.LaunchID); a.mu.Unlock() }()
	l, err := a.store.Update(ctx, req.LaunchID, func(l *Launch) error {
		if l.State != LaunchStatePrepared && !(a.replaySafe && l.State == LaunchStateExecuting) {
			return fmt.Errorf("launch %s is in state %s, expected prepared", l.ID, l.State)
		}
		now := time.Now().UTC()
		l.State = LaunchStateExecuting
		l.UpdatedAt = now
		if l.StartedAt == nil {
			l.StartedAt = &now
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sid, state, startErr := a.start(ctx, l)
	// Persist the outcome even when the request was cancelled after its side effect.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, err := a.store.Update(saveCtx, l.ID, func(current *Launch) error {
		if sid != "" {
			current.SessionID = sid
		}
		if current.State != LaunchStateExecuting {
			return nil
		}
		current.UpdatedAt = time.Now().UTC()
		if startErr != nil {
			current.Error = startErr.Error()
			if !a.replaySafe {
				current.State = LaunchStateFailed
				now := current.UpdatedAt
				current.EndedAt = &now
			}
			// Tether's keyed create/launch can be resumed explicitly. Leave executing
			// on ambiguous transport failures rather than lose or duplicate the session.
			return nil
		}
		current.State = state
		current.Error = ""
		if state == LaunchStateFailed {
			current.Error = "provider session failed"
		}
		if terminalLaunch(state) {
			now := current.UpdatedAt
			current.EndedAt = &now
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func terminalLaunch(state LaunchState) bool {
	return state == LaunchStateCompleted || state == LaunchStateFailed || state == LaunchStateCancelled
}

// Cancel rejects active creation calls, but resolves interrupted executing
// intents honestly: stop a known session or record an unknown provider outcome.
func (a *launchLifecycle) Cancel(ctx context.Context, req CancelRequest) (*Launch, error) {
	// Reserve this launch against a concurrent Execute replay while cancellation
	// reads its checkpoint and calls the provider. Other launches remain usable.
	a.mu.Lock()
	if a.inFlight[req.LaunchID] || a.cancelling[req.LaunchID] {
		a.mu.Unlock()
		return nil, fmt.Errorf("launch %s has an operation in flight; wait for its result before stopping it", req.LaunchID)
	}
	if a.cancelling == nil {
		a.cancelling = map[string]bool{}
	}
	a.cancelling[req.LaunchID] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.cancelling, req.LaunchID); a.mu.Unlock() }()
	l, err := a.store.Get(ctx, req.LaunchID)
	if err != nil {
		return nil, err
	}
	if l.State == LaunchStatePrepared {
		return a.store.Update(ctx, l.ID, func(current *Launch) error {
			// Execute can win between Get and Update. Do not cancel its in-flight call.
			if current.State != LaunchStatePrepared {
				return fmt.Errorf("launch %s is in state %s; wait for session creation before stopping it", current.ID, current.State)
			}
			markCancelled(current, req.Reason)
			return nil
		})
	}
	if l.State == LaunchStateExecuting && l.SessionID == "" {
		return a.store.Update(ctx, l.ID, func(current *Launch) error {
			if current.State != LaunchStateExecuting || current.SessionID != "" {
				return fmt.Errorf("launch state changed while resolving interrupted execution")
			}
			markCancelled(current, "provider outcome unknown; a session may exist")
			return nil
		})
	}
	if l.State != LaunchStateRunning && l.State != LaunchStateExecuting {
		return nil, fmt.Errorf("launch %s is in state %s, cannot cancel", l.ID, l.State)
	}
	if a.stop == nil {
		return nil, fmt.Errorf("provider %s cannot stop a running launch; state unchanged", a.backend)
	}
	if l.SessionID == "" {
		return nil, fmt.Errorf("running launch has no provider session ID; state unchanged")
	}
	if err := a.stop(ctx, l); err != nil {
		return nil, err
	}
	// Once the provider accepted stop, persist its outcome even if the caller
	// disconnected. Failed provider calls leave the durable state untouched.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return a.store.Update(saveCtx, l.ID, func(current *Launch) error {
		// A concurrent provider status poll may have recorded completion already.
		if terminalLaunch(current.State) {
			return nil
		}
		if current.State != LaunchStateRunning && current.State != LaunchStateExecuting {
			return fmt.Errorf("launch state changed while stopping its provider session")
		}
		markCancelled(current, req.Reason)
		return nil
	})
}

func markCancelled(l *Launch, reason string) {
	now := time.Now().UTC()
	l.State = LaunchStateCancelled
	l.UpdatedAt = now
	l.EndedAt = &now
	l.Error = "cancelled"
	if reason != "" {
		l.Error += ": " + reason
	}
}

func (a *launchLifecycle) Read(ctx context.Context, req ReadRequest) (*Launch, error) {
	l, err := a.store.Get(ctx, req.LaunchID)
	if err != nil {
		return nil, err
	}
	if a.refresh != nil && l.SessionID != "" && !terminalLaunch(l.State) {
		return a.refresh(ctx, l)
	}
	return l, nil
}
func (a *launchLifecycle) List(ctx context.Context, req ListRequest) ([]Launch, error) {
	return a.store.List(ctx, req)
}
func (a *launchLifecycle) Status(ctx context.Context, req StatusRequest) (*LaunchStatus, error) {
	l, err := a.Read(ctx, ReadRequest{LaunchID: req.LaunchID})
	if err != nil {
		return nil, err
	}
	return &LaunchStatus{LaunchID: l.ID, State: l.State, SessionID: l.SessionID, Error: l.Error, UpdatedAt: l.UpdatedAt}, nil
}

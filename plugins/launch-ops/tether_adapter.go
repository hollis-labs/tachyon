package main

import (
	"context"
	"fmt"
	"time"

	tether "github.com/hollis-labs/substrate/mesh/tetherclient"
)

// TetherLaunchAdapter binds durable intents to catalog launches in Tether.
// A stable idempotency key and a saved session ID make Execute resumable.
type TetherLaunchAdapter struct {
	*launchLifecycle
	client *tether.Client
}

func NewTetherLaunchAdapter(addr string, store *LaunchStore) (*TetherLaunchAdapter, error) {
	httpClient, err := tetherHTTPClient(addr)
	if err != nil {
		return nil, safeProviderError(err)
	}
	client, err := tether.New(addr, tether.WithHTTPClient(httpClient))
	if err != nil {
		return nil, safeProviderError(err)
	}
	a := &TetherLaunchAdapter{client: client, launchLifecycle: &launchLifecycle{store: store, backend: "tether", replaySafe: true}}
	a.resolve = a.resolveLaunch
	a.start = a.startSession
	a.refresh = a.refreshSession
	a.stop = func(ctx context.Context, l *Launch) error {
		return safeProviderError(a.client.StopSession(ctx, l.SessionID))
	}
	return a, nil
}

func (a *TetherLaunchAdapter) resolveLaunch(ctx context.Context, req PrepareRequest) (PrepareRequest, string, error) {
	if req.Model != "" {
		return req, "", fmt.Errorf("Tether model is configured by the catalog launch; model overrides are not supported")
	}
	for key, val := range req.Config {
		if key != "launch_id" && key != "boot_prompt" {
			return req, "", fmt.Errorf("unsupported Tether launch config field %q", key)
		}
		if _, ok := val.(string); !ok {
			return req, "", fmt.Errorf("Tether config %s must be a string", key)
		}
	}
	launchID, _ := req.Config["launch_id"].(string)
	launches, err := a.client.ListLaunches(ctx)
	if err != nil {
		return req, "", safeProviderError(err)
	}
	matches := []tether.Launch{}
	for _, l := range launches {
		if launchID != "" && l.ID != launchID || l.Agent != req.AgentID || req.ProjectID != "" && l.Project != req.ProjectID {
			continue
		}
		if req.Provider != "" && req.Provider != "tether" && l.Provider != req.Provider {
			continue
		}
		matches = append(matches, l)
	}
	if len(matches) != 1 {
		return req, "", fmt.Errorf("expected one matching Tether catalog launch, found %d; specify config.launch_id", len(matches))
	}
	selected := matches[0]
	config := map[string]any{"launch_id": selected.ID}
	if prompt, ok := req.Config["boot_prompt"]; ok {
		config["boot_prompt"] = prompt
	}
	req.Config = config
	req.ProjectID = selected.Project
	if req.Provider == "" {
		req.Provider = "tether"
	}
	return req, selected.Agent, nil
}

func (a *TetherLaunchAdapter) startSession(ctx context.Context, l *Launch) (string, LaunchState, error) {
	sid := l.SessionID
	if sid == "" {
		launchID, _ := l.Config["launch_id"].(string)
		prompt, _ := l.Config["boot_prompt"].(string)
		result, err := a.client.CreateSessionWithInput(ctx, tether.LaunchRequest{Launch: launchID, BootPrompt: prompt, IdempotencyKey: "tachyon:launch:" + l.ID})
		if err != nil {
			return "", LaunchStateExecuting, safeProviderError(err)
		}
		sid = result.ID
		if sid == "" {
			return "", LaunchStateExecuting, fmt.Errorf("Tether returned an empty session ID")
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		saved, err := a.store.Update(saveCtx, l.ID, func(current *Launch) error { current.SessionID = sid; current.UpdatedAt = time.Now().UTC(); return nil })
		if err != nil {
			return sid, LaunchStateExecuting, safeProviderError(err)
		}
		if saved.State == LaunchStateCancelled {
			return sid, LaunchStateCancelled, nil
		}
	}
	// Read first: replaying a completed keyed session must never relaunch it.
	session, err := a.client.GetSession(ctx, sid)
	if err != nil {
		return sid, LaunchStateExecuting, safeProviderError(err)
	}
	if session.State == tether.SessionStateCreated {
		if _, err := a.client.LaunchSession(ctx, sid); err != nil {
			return sid, LaunchStateExecuting, safeProviderError(err)
		}
		session, err = a.client.GetSession(ctx, sid)
		if err != nil {
			return sid, LaunchStateExecuting, safeProviderError(err)
		}
	}
	state, err := launchStateFromSession(session.State)
	return sid, state, err
}

func launchStateFromSession(state string) (LaunchState, error) {
	switch state {
	case tether.SessionStateCreated, tether.SessionStateLaunching:
		return LaunchStateExecuting, nil
	case tether.SessionStateRunning:
		return LaunchStateRunning, nil
	case tether.SessionStateCompleted:
		return LaunchStateCompleted, nil
	case tether.SessionStateFailed:
		return LaunchStateFailed, nil
	case tether.SessionStateKilled:
		return LaunchStateCancelled, nil
	default:
		return LaunchStateExecuting, fmt.Errorf("unknown Tether session state")
	}
}

func (a *TetherLaunchAdapter) refreshSession(ctx context.Context, l *Launch) (*Launch, error) {
	session, err := a.client.GetSession(ctx, l.SessionID)
	if err != nil {
		return nil, safeProviderError(err)
	}
	state, err := launchStateFromSession(session.State)
	if err != nil {
		return nil, safeProviderError(err)
	}
	return a.store.Update(ctx, l.ID, func(current *Launch) error {
		if terminalLaunch(current.State) {
			return nil
		}
		current.State = state
		current.UpdatedAt = time.Now().UTC()
		if terminalLaunch(state) {
			now := current.UpdatedAt
			current.EndedAt = &now
			if state == LaunchStateFailed {
				current.Error = "Tether session failed"
			} else {
				current.Error = ""
			}
		} else if state == LaunchStateRunning {
			current.Error = ""
		}
		return nil
	})
}

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// HandleVerb dispatches a verb invocation to the appropriate adapter
// method and returns a ResultEnvelope. Implements the VerbPlugin
// interface from internal/pluginkit (CW-20261001-0469).
func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	switch verb {
	case "launch_prepare":
		return p.verbLaunchPrepare(ctx, payload)
	case "launch_execute":
		return p.verbLaunchExecute(ctx, payload)
	case "launch_cancel":
		return p.verbLaunchCancel(ctx, payload)
	case "launch_read":
		return p.verbLaunchRead(ctx, payload)
	case "launch_list":
		return p.verbLaunchList(ctx, payload)
	case "launch_status":
		return p.verbLaunchStatus(ctx, payload)
	default:
		return contract.Err("unknown_verb", fmt.Sprintf("verb %q is not implemented", verb)), nil
	}
}

func (p *plugin) verbLaunchPrepare(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req PrepareRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return contract.Err("validation", "invalid prepare request: "+err.Error()), nil
	}
	if req.AgentID == "" {
		return contract.Err("validation", "agent_id is required"), nil
	}
	launch, err := p.adapter.Prepare(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(launch)
}

func (p *plugin) verbLaunchExecute(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req ExecuteRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return contract.Err("validation", "invalid execute request: "+err.Error()), nil
	}
	if req.LaunchID == "" {
		return contract.Err("validation", "launch_id is required"), nil
	}

	// No ask in MVP. Confirmation for open_world verbs is host policy,
	// not plugin policy — the host or HITL bridge can intercept the
	// effect classification and gate execution before it reaches here.

	launch, err := p.adapter.Execute(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(launch)
}

func (p *plugin) verbLaunchCancel(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req CancelRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return contract.Err("validation", "invalid cancel request: "+err.Error()), nil
	}
	if req.LaunchID == "" {
		return contract.Err("validation", "launch_id is required"), nil
	}
	launch, err := p.adapter.Cancel(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(launch)
}

func (p *plugin) verbLaunchRead(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req ReadRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return contract.Err("validation", "invalid read request: "+err.Error()), nil
	}
	if req.LaunchID == "" {
		return contract.Err("validation", "launch_id is required"), nil
	}
	launch, err := p.adapter.Read(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(launch)
}

func (p *plugin) verbLaunchList(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req ListRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return contract.Err("validation", "invalid list request: "+err.Error()), nil
		}
	}
	launches, err := p.adapter.List(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(launches)
}

func (p *plugin) verbLaunchStatus(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req StatusRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return contract.Err("validation", "invalid status request: "+err.Error()), nil
	}
	if req.LaunchID == "" {
		return contract.Err("validation", "launch_id is required"), nil
	}
	status, err := p.adapter.Status(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(status)
}

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// handleVerb dispatches a verb/invoke call to the appropriate adapter
// method and returns a ResultEnvelope. Every observe_ verb is a read.
func (p *plugin) handleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	switch verb {
	case "observe_activity":
		return p.verbObserveActivity(ctx, payload)
	case "observe_logs":
		return p.verbObserveLogs(ctx, payload)
	case "observe_metrics":
		return p.verbObserveMetrics(ctx, payload)
	case "observe_events":
		return p.verbObserveEvents(ctx, payload)
	case "observe_status":
		return p.verbObserveStatus(ctx)
	case "observe_subscribe":
		return p.verbObserveSubscribe(ctx, payload)
	default:
		return contract.Err("unknown_verb", fmt.Sprintf("verb %q is not implemented", verb)), nil
	}
}

func (p *plugin) verbObserveActivity(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var filter ActivityFilter
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &filter); err != nil {
			return contract.Err("validation", "invalid activity filter: "+err.Error()), nil
		}
	}
	entries, err := p.adapter.ListActivity(ctx, filter)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(entries)
}

func (p *plugin) verbObserveLogs(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var filter LogFilter
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &filter); err != nil {
			return contract.Err("validation", "invalid log filter: "+err.Error()), nil
		}
	}
	entries, err := p.adapter.ListLogs(ctx, filter)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(entries)
}

func (p *plugin) verbObserveMetrics(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var filter MetricFilter
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &filter); err != nil {
			return contract.Err("validation", "invalid metric filter: "+err.Error()), nil
		}
	}
	points, err := p.adapter.ListMetrics(ctx, filter)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(points)
}

func (p *plugin) verbObserveEvents(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var filter EventFilter
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &filter); err != nil {
			return contract.Err("validation", "invalid event filter: "+err.Error()), nil
		}
	}
	events, err := p.adapter.ListEvents(ctx, filter)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(events)
}

func (p *plugin) verbObserveStatus(ctx context.Context) (contract.ResultEnvelope, error) {
	summary, err := p.adapter.Status(ctx)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(summary)
}

func (p *plugin) verbObserveSubscribe(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req SubscribeRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return contract.Err("validation", "invalid subscribe request: "+err.Error()), nil
		}
	}
	if req.Channel == "" {
		return contract.Err("validation", "channel is required (one of: activity, logs, events)"), nil
	}
	handle, err := p.adapter.Subscribe(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(handle)
}

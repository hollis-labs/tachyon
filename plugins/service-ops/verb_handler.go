package main

import (
	"context"
	"encoding/json"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	switch verb {
	case "service_list", "service_read", "service_status", "service_health":
	default:
		return contract.Err("unknown_verb", "unknown verb: "+verb), nil
	}
	var req struct {
		ServiceID string `json:"service_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return contract.Err("validation", "invalid JSON payload"), nil
		}
	}
	if (verb == "service_read" || verb == "service_status") && req.ServiceID == "" {
		return contract.Err("validation", "service_id is required"), nil
	}
	var data any
	var err error
	switch verb {
	case "service_list":
		data, err = p.adapter.List(ctx)
	case "service_read":
		data, err = p.adapter.Read(ctx, req.ServiceID)
	case "service_status":
		data, err = p.adapter.Status(ctx, req.ServiceID)
	case "service_health":
		data, err = p.adapter.Health(ctx)
	}
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(data)
}

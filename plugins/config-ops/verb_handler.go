package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	if _, declared := p.Capabilities().Verbs[verb]; !declared {
		return contract.Err("unknown_verb", "verb is not implemented"), nil
	}
	if p.adapter == nil {
		return contract.Err("provider_error", "config adapter is not initialized"), nil
	}
	if verb == "config_list" {
		targets, err := p.adapter.List(ctx)
		return configResult(targets, err)
	}
	var req struct {
		Plugin string         `json:"plugin"`
		Values map[string]any `json:"values,omitempty"`
	}
	if err := json.Unmarshal(payload, &req); err != nil || strings.TrimSpace(req.Plugin) == "" {
		return contract.Err("validation", "plugin is required"), nil
	}
	switch verb {
	case "config_schema":
		view, err := p.adapter.Read(ctx, req.Plugin)
		if err != nil {
			return configResult(nil, err)
		}
		return contract.OK(view.Target)
	case "config_get":
		view, err := p.adapter.Read(ctx, req.Plugin)
		return configResult(view, err)
	case "config_set":
		if req.Values == nil {
			return contract.Err("validation", "values object is required"), nil
		}
		validation, err := p.adapter.Validate(ctx, req.Plugin, req.Values)
		if err != nil {
			return configResult(nil, err)
		}
		if !validation.Valid {
			env := contract.Err("validation", "proposed configuration is invalid")
			env.Error.Detail, _ = json.Marshal(validation)
			return env, nil
		}
		// Declared settings apply on restart; adding a default-valued override
		// changes storage but not the desired value that the plugin will receive.
		before, err := p.adapter.Read(ctx, req.Plugin)
		if err != nil {
			return configResult(nil, err)
		}
		view, err := p.adapter.Update(ctx, req.Plugin, req.Values)
		if err != nil {
			return configResult(nil, err)
		}
		return contract.OK(map[string]any{"plugin": req.Plugin, "restart_required": !reflect.DeepEqual(before.Values, view.Values), "config": view})
	case "config_reset":
		before, err := p.adapter.Read(ctx, req.Plugin)
		if err != nil {
			return configResult(nil, err)
		}
		view, err := p.adapter.Reset(ctx, req.Plugin)
		if err != nil {
			return configResult(nil, err)
		}
		return contract.OK(map[string]any{"plugin": req.Plugin, "restart_required": !reflect.DeepEqual(before.Values, view.Values), "config": view})
	}
	return contract.Err("unknown_verb", "verb is not implemented"), nil
}
func configResult(value any, err error) (contract.ResultEnvelope, error) {
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(value)
}

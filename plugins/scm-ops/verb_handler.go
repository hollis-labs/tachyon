package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// HandleVerb uses {id} for a repo, {id,limit} for activity, and
// {id,base,head,staged} for diffs. All operations are local reads.
func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req struct {
		DiffRequest
		Limit int `json:"limit,omitempty"`
	}
	switch verb {
	case "scm_list", "scm_read", "scm_activity", "scm_status", "scm_diff":
	default:
		return contract.Err("unknown_verb", fmt.Sprintf("verb %q is not implemented", verb)), nil
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return contract.Err("validation", "invalid request: "+err.Error()), nil
		}
	}
	if verb != "scm_list" && req.ID == "" {
		return contract.Err("validation", "id is required (relative to repos_root)"), nil
	}
	var data any
	var err error
	switch verb {
	case "scm_list":
		data, err = p.adapter.List(ctx)
	case "scm_read":
		data, err = p.adapter.Read(ctx, req.ID)
	case "scm_activity":
		data, err = p.adapter.Activity(ctx, req.ID, req.Limit)
	case "scm_status":
		data, err = p.adapter.Status(ctx, req.ID)
	case "scm_diff":
		data, err = p.adapter.Diff(ctx, req.DiffRequest)
	}
	if err != nil {
		code := "provider_error"
		if errors.Is(err, ErrValidation) {
			code = "validation"
		}
		if errors.Is(err, ErrNotFound) {
			code = "not_found"
		}
		return contract.Err(code, err.Error()), nil
	}
	return contract.OK(data)
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func decodePayload(payload json.RawMessage, out any) error {
	if len(payload) == 0 {
		return nil
	}
	if string(payload) == "null" {
		return fmt.Errorf("payload must be an object")
	}
	return json.Unmarshal(payload, out)
}
func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var result any
	var err error
	invalid := func(message string) (contract.ResultEnvelope, error) { return contract.Err("validation", message), nil }
	switch verb {
	case "work_create":
		var req CreateWorkRequest
		if err := decodePayload(payload, &req); err != nil {
			return invalid(err.Error())
		}
		if strings.TrimSpace(req.Title) == "" {
			return invalid("title is required")
		}
		if req.ProjectID == "" {
			req.ProjectID = p.defaultProject
		}
		result, err = p.adapter.CreateWorkItem(ctx, req)
	case "work_list":
		var filters WorkFilters
		if err := decodePayload(payload, &filters); err != nil {
			return invalid(err.Error())
		}
		if filters.Limit < 0 || filters.Limit > 200 || filters.Offset < 0 {
			return invalid("limit must be 0..200 and offset nonnegative")
		}
		if filters.ProjectID == "" {
			filters.ProjectID = p.defaultProject
		}
		result, err = p.adapter.ListWorkItems(ctx, filters)
	case "work_search":
		var req struct {
			Query string `json:"query"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return invalid(err.Error())
		}
		if strings.TrimSpace(req.Query) == "" {
			return invalid("query is required")
		}
		result, err = p.adapter.SearchWorkItems(ctx, req.Query)
	case "work_read", "work_update", "work_assign", "work_transition", "work_comment":
		var req struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
			Status   string `json:"status"`
			UpdateWorkRequest
			CommentRequest
		}
		if err := decodePayload(payload, &req); err != nil {
			return invalid(err.Error())
		}
		if strings.TrimSpace(req.ID) == "" {
			return invalid("id is required")
		}
		switch verb {
		case "work_read":
			result, err = p.adapter.GetWorkItem(ctx, req.ID)
		case "work_update":
			if req.Status != "" {
				return invalid("use work_transition to change status")
			}
			result, err = p.adapter.UpdateWorkItem(ctx, req.ID, req.UpdateWorkRequest)
		// work_assign maps to Torque metadata.assignee, preserving other keys
		// through a provider read-merge-write; it does not launch an executor.
		case "work_assign":
			if strings.TrimSpace(req.Assignee) == "" {
				return invalid("assignee is required")
			}
			result, err = p.adapter.AssignWorkItem(ctx, req.ID, req.Assignee)
		case "work_transition":
			if strings.TrimSpace(req.Status) == "" {
				return invalid("status is required")
			}
			result, err = p.adapter.TransitionWorkItem(ctx, req.ID, req.Status)
		case "work_comment":
			if strings.TrimSpace(req.Content) == "" {
				return invalid("content is required")
			}
			result, err = p.adapter.AddComment(ctx, req.ID, req.CommentRequest)
		}
	default:
		return contract.Err("unknown_verb", fmt.Sprintf("verb %q is not implemented", verb)), nil
	}
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(result)
}

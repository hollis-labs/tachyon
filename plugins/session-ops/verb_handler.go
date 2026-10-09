package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	tether "github.com/hollis-labs/substrate/mesh/tetherclient"
	"strings"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	// Reject unsupported verbs before consulting the adapter.
	if _, ok := p.Capabilities().Verbs[verb]; !ok {
		return contract.Err("unknown_verb", "verb is not implemented"), nil
	}
	if p.adapter == nil {
		return contract.Err("provider_error", "session adapter is not initialized"), nil
	}
	switch verb {
	case "session_create":
		var req CreateSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil || strings.TrimSpace(req.LaunchID) == "" {
			return contract.Err("validation", "launch_id is required"), nil
		}
		s, err := p.adapter.Create(ctx, req)
		return sessionResult(s, err)
	case "session_list":
		var req ListSessionsRequest
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return contract.Err("validation", "invalid list request"), nil
			}
		}
		if req.Limit < 0 {
			return contract.Err("validation", "limit must be non-negative"), nil
		}
		page, err := p.adapter.List(ctx, req)
		return sessionResult(page, err)
	case "session_history":
		var req HistoryRequest
		if err := json.Unmarshal(payload, &req); err != nil || strings.TrimSpace(req.ID) == "" {
			return contract.Err("validation", "id is required"), nil
		}
		if req.Limit < 0 || req.Cursor < 0 || req.SinceSeq < 0 {
			return contract.Err("validation", "limit, cursor and since_seq must be non-negative"), nil
		}
		page, err := p.adapter.History(ctx, req)
		return sessionResult(page, err)
	default:
		var req struct {
			ID   string `json:"id"`
			Text string `json:"text,omitempty"`
		}
		if err := json.Unmarshal(payload, &req); err != nil || strings.TrimSpace(req.ID) == "" {
			return contract.Err("validation", "id is required"), nil
		}
		switch verb {
		case "session_read":
			s, err := p.adapter.Read(ctx, req.ID)
			return sessionResult(s, err)
		case "session_attach":
			// Returns validated server-side connection metadata, not a stream
			// or a provider URL for the browser. Tether owns attachment.
			info, err := p.adapter.Attach(ctx, req.ID)
			return sessionResult(info, err)
		case "session_stop":
			return sessionResult(map[string]any{"id": req.ID, "stopped": true}, p.adapter.Stop(ctx, req.ID))
		case "session_submit":
			if strings.TrimSpace(req.Text) == "" {
				return contract.Err("validation", "text is required"), nil
			}
			return sessionResult(map[string]any{"id": req.ID, "submitted": true}, p.adapter.Submit(ctx, req.ID, req.Text))
		}
	}
	return contract.Err("unknown_verb", "verb is not implemented"), nil
}

func sessionResult(data any, err error) (contract.ResultEnvelope, error) {
	if errors.Is(err, context.DeadlineExceeded) {
		return contract.Err("timeout", "session request timed out; completion is unknown"), nil
	}
	if errors.Is(err, errResponseTooLarge) {
		return contract.Err("response_too_large", "Tether response exceeded size limit"), nil
	}
	if errors.Is(err, errInactiveSession) {
		return contract.Err("invalid_state", "session is not running"), nil
	}
	var apiErr *tether.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 100 && apiErr.StatusCode <= 599 {
		return contract.Err("provider_error", fmt.Sprintf("Tether request failed (HTTP %d)", apiErr.StatusCode)), nil
	}
	if err != nil {
		return contract.Err("provider_error", "Tether request failed"), nil
	}
	return contract.OK(data)
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
)

const (
	maxLimit     = 200
	maxTurnLimit = 200
	maxQueryLen  = 500
)

var (
	modes = map[string]bool{"text": true, "summary": true, "semantic": true, "hybrid": true}
	sorts = map[string]bool{"started": true, "turns": true, "tokens": true, "tool_calls": true, "errors": true}
	kinds = map[string]bool{"main": true, "subagent": true}
)

// sessionsRequest mirrors GET /v1/transcripts/sessions. Zero values are omitted.
type sessionsRequest struct {
	Q             string `json:"q"`
	Mode          string `json:"mode"`
	Tool          string `json:"tool"`
	Project       string `json:"project"`
	Model         string `json:"model"`
	Kind          string `json:"kind"`
	Tag           string `json:"tag"`
	Since         string `json:"since"`
	Until         string `json:"until"`
	HasError      *bool  `json:"has_error"`
	HasEnrichment *bool  `json:"has_enrichment"`
	Sort          string `json:"sort"`
	Order         string `json:"order"`
	Limit         int    `json:"limit"`
	Cursor        string `json:"cursor"`
}

func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	if _, ok := p.Capabilities().Verbs[verb]; !ok {
		return contract.Err("unknown_verb", "verb is not implemented"), nil
	}
	if p.api == nil {
		return contract.Err("provider_error", "history adapter is not initialized"), nil
	}
	bad := func(msg string) (contract.ResultEnvelope, error) { return contract.Err("validation", msg), nil }

	switch verb {
	case "history_status":
		return result(p.api.get(ctx, "/v1/transcripts/status", nil))
	case "history_facets":
		return result(p.api.get(ctx, "/v1/transcripts/facets", nil))
	case "history_projects":
		var req struct {
			Q     string `json:"q"`
			Limit int    `json:"limit"`
		}
		if !decode(payload, &req) || req.Limit < 0 || req.Limit > maxLimit || len(req.Q) > maxQueryLen {
			return bad("q is at most 500 characters and limit 0-200")
		}
		q := url.Values{}
		setIf(q, "q", strings.TrimSpace(req.Q))
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
		return result(p.api.get(ctx, "/v1/transcripts/projects", q))
	case "history_session":
		var req struct {
			ID         string `json:"id"`
			TurnOffset int    `json:"turn_offset"`
			TurnLimit  int    `json:"turn_limit"`
		}
		if !decode(payload, &req) || !validSessionID(req.ID) {
			return bad("id must be a 64-character lowercase hex session id")
		}
		if req.TurnOffset < 0 || req.TurnLimit < 0 || req.TurnLimit > maxTurnLimit {
			return bad("turn_offset must be non-negative and turn_limit 0-200")
		}
		q := url.Values{}
		if req.TurnOffset > 0 {
			q.Set("turn_offset", strconv.Itoa(req.TurnOffset))
		}
		if req.TurnLimit > 0 {
			q.Set("turn_limit", strconv.Itoa(req.TurnLimit))
		}
		return result(p.api.get(ctx, "/v1/transcripts/sessions/"+req.ID, q))
	case "history_sessions":
		var req sessionsRequest
		if !decode(payload, &req) {
			return bad("invalid request")
		}
		q, msg := req.query()
		if msg != "" {
			return bad(msg)
		}
		return result(p.api.get(ctx, "/v1/transcripts/sessions", q))
	}
	return contract.Err("unknown_verb", "verb is not implemented"), nil
}

func (r sessionsRequest) query() (url.Values, string) {
	q := url.Values{}
	text := strings.TrimSpace(r.Q)
	switch {
	case len(text) > maxQueryLen:
		return nil, "q is at most 500 characters"
	case r.Mode != "" && !modes[r.Mode]:
		return nil, "mode must be text, summary, semantic or hybrid"
	case r.Mode != "" && text == "":
		return nil, "mode needs q"
	case r.Kind != "" && !kinds[r.Kind]:
		return nil, "kind must be main or subagent"
	case r.Sort != "" && !sorts[r.Sort]:
		return nil, "sort must be started, turns, tokens, tool_calls or errors"
	case r.Order != "" && r.Order != "asc" && r.Order != "desc":
		return nil, "order must be asc or desc"
	case r.Limit < 0 || r.Limit > maxLimit:
		return nil, "limit must be 0-200"
	case !validDate(r.Since) || !validDate(r.Until):
		return nil, "since and until must be RFC 3339 or YYYY-MM-DD"
	case len(r.Cursor) > 256 || len(r.Tool) > 64 || len(r.Model) > 128 || len(r.Project) > 256 || len(r.Tag) > 128:
		return nil, "a filter value is too long"
	}
	setIf(q, "q", text)
	setIf(q, "mode", r.Mode)
	setIf(q, "tool", r.Tool)
	setIf(q, "project", r.Project)
	setIf(q, "model", r.Model)
	setIf(q, "kind", r.Kind)
	setIf(q, "tag", r.Tag)
	setIf(q, "since", r.Since)
	setIf(q, "until", r.Until)
	setIf(q, "sort", r.Sort)
	setIf(q, "order", r.Order)
	setIf(q, "cursor", r.Cursor)
	if r.HasError != nil {
		q.Set("has_error", strconv.FormatBool(*r.HasError))
	}
	if r.HasEnrichment != nil {
		q.Set("has_enrichment", strconv.FormatBool(*r.HasEnrichment))
	}
	if r.Limit > 0 {
		q.Set("limit", strconv.Itoa(r.Limit))
	}
	return q, ""
}

func validDate(v string) bool {
	if v == "" {
		return true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if _, err := time.Parse(layout, v); err == nil {
			return true
		}
	}
	return false
}

func validSessionID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func setIf(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func decode(payload json.RawMessage, out any) bool {
	if len(payload) == 0 {
		return true
	}
	return json.Unmarshal(payload, out) == nil
}

// result maps a Fragments answer to a verb envelope. Upstream text never leaves
// the plugin: only a fixed local code and, for HTTP failures, the status.
func result(data json.RawMessage, err error) (contract.ResultEnvelope, error) {
	if err == nil {
		return contract.OK(data)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return contract.Err("timeout", "Fragments request timed out"), nil
	}
	if errors.Is(err, errResponseTooLarge) {
		return contract.Err("response_too_large", "Fragments response exceeded size limit"), nil
	}
	var up *upstreamError
	if errors.As(err, &up) {
		code := "provider_error"
		switch up.status {
		case http.StatusUnauthorized:
			code = "unauthenticated"
		case http.StatusForbidden:
			code = "forbidden"
		case http.StatusNotFound:
			// Also what an off-by-default transcript feature answers.
			code = "not_found"
		case http.StatusServiceUnavailable:
			code = "unavailable"
		}
		return contract.Err(code, fmt.Sprintf("Fragments request failed (HTTP %d)", up.status)), nil
	}
	return contract.Err("provider_error", "Fragments request failed"), nil
}

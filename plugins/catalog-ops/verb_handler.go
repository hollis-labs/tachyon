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

	"github.com/hollis-labs/tachyon/internal/contract"
)

// defaultUpdatedBy attributes registry edits that name no author. Tether
// requires last_updated_by; Tachyon cannot yet vouch for the operator behind a
// call, so the default says which component wrote it, not who.
const defaultUpdatedBy = "tachyon:catalog-ops"

type catalogFile struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
}

func (f catalogFile) valid() bool {
	p := strings.TrimSpace(f.Path)
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, "..") && !strings.Contains(p, "\\") && (f.Delete || f.Content != "")
}

func filesValid(files []catalogFile) bool {
	for _, f := range files {
		if !f.valid() {
			return false
		}
	}
	return true
}

func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	if _, ok := p.Capabilities().Verbs[verb]; !ok {
		return contract.Err("unknown_verb", "verb is not implemented"), nil
	}
	if p.api == nil {
		return contract.Err("provider_error", "catalog adapter is not initialized"), nil
	}
	bad := func(msg string) (contract.ResultEnvelope, error) { return contract.Err("validation", msg), nil }

	switch verb {
	case "catalog_agents", "catalog_projects", "catalog_providers", "catalog_launches":
		return result(p.api.do(ctx, http.MethodGet, "/catalog/"+strings.TrimPrefix(verb, "catalog_"), nil, nil, readTimeout))
	case "catalog_backups":
		return result(p.api.do(ctx, http.MethodGet, "/catalog/backups", nil, nil, readTimeout))
	case "catalog_files":
		var req struct {
			Path string `json:"path"`
		}
		if !decode(payload, &req) {
			return bad("invalid request")
		}
		q := url.Values{}
		if req.Path != "" {
			if !(catalogFile{Path: req.Path, Delete: true}).valid() {
				return bad("path must be catalog-relative")
			}
			q.Set("path", req.Path)
		}
		return result(p.api.do(ctx, http.MethodGet, "/catalog/files", q, nil, readTimeout))
	case "catalog_audit":
		var req struct {
			Limit int `json:"limit"`
		}
		if !decode(payload, &req) || req.Limit < 0 {
			return bad("limit must be non-negative")
		}
		q := url.Values{}
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
		return result(p.api.do(ctx, http.MethodGet, "/catalog/audit", q, nil, readTimeout))
	case "catalog_validate":
		var req struct {
			Files []catalogFile `json:"files"`
		}
		if !decode(payload, &req) || !filesValid(req.Files) {
			return bad("files need a catalog-relative path and content, or delete")
		}
		return result(p.api.do(ctx, http.MethodPost, "/catalog/validate", nil, map[string]any{"files": req.Files}, writeTimeout))
	case "catalog_plan":
		var req struct {
			Name       string        `json:"name"`
			TaskID     string        `json:"task_id"`
			Files      []catalogFile `json:"files"`
			RollbackOf string        `json:"rollback_of"`
		}
		if !decode(payload, &req) {
			return bad("invalid request")
		}
		switch {
		case req.RollbackOf != "" && len(req.Files) > 0:
			return bad("send files or rollback_of, not both")
		case req.RollbackOf == "" && (len(req.Files) == 0 || strings.TrimSpace(req.Name) == ""):
			return bad("name and at least one file are required")
		case !filesValid(req.Files):
			return bad("files need a catalog-relative path and content, or delete")
		}
		return result(p.api.do(ctx, http.MethodPost, "/catalog/plans", nil, req, writeTimeout))
	case "catalog_plan_get", "catalog_apply":
		var req struct {
			PlanID string `json:"plan_id"`
			ID     string `json:"id"`
		}
		if !decode(payload, &req) {
			return bad("invalid request")
		}
		id := strings.TrimSpace(firstNonEmpty(req.PlanID, req.ID))
		if id == "" || strings.ContainsAny(id, "/?#") {
			return bad("plan_id is required")
		}
		if verb == "catalog_plan_get" {
			return result(p.api.do(ctx, http.MethodGet, "/catalog/plans/"+url.PathEscape(id), nil, nil, readTimeout))
		}
		// Approval is a separate, human act that Tether refuses to any remote
		// caller. Apply only installs a plan that is already approved.
		return result(p.api.do(ctx, http.MethodPost, "/catalog/plans/"+url.PathEscape(id)+"/apply", nil, nil, writeTimeout))
	case "catalog_reload":
		return result(p.api.do(ctx, http.MethodPost, "/catalog/reload", nil, nil, writeTimeout))
	}

	// Registry verbs share a kind (agents|projects) and, mostly, a URN.
	var req struct {
		Kind       string          `json:"kind"`
		URN        string          `json:"urn"`
		Profile    json.RawMessage `json:"profile"`
		Patch      map[string]any  `json:"patch"`
		Role       string          `json:"role"`
		Title      string          `json:"title"`
		Project    string          `json:"project"`
		Capability string          `json:"capability"`
		SkillName  string          `json:"skill_name"`
		Status     string          `json:"status"`
		ExternalID string          `json:"external_id"`
		Substrate  string          `json:"substrate"`
	}
	if !decode(payload, &req) {
		return bad("invalid request")
	}
	if req.Kind != "agents" && req.Kind != "projects" {
		return bad("kind must be agents or projects")
	}
	base := "/registry/" + req.Kind
	if verb == "registry_search" {
		q := url.Values{}
		for k, v := range map[string]string{"role": req.Role, "title": req.Title, "project": req.Project,
			"capability": req.Capability, "skill_name": req.SkillName, "status": req.Status,
			"external_id": req.ExternalID, "substrate": req.Substrate} {
			if v != "" {
				q.Set(k, v)
			}
		}
		return result(p.api.do(ctx, http.MethodGet, base, q, nil, readTimeout))
	}
	if verb == "registry_register" {
		var profile map[string]any
		if len(req.Profile) == 0 || json.Unmarshal(req.Profile, &profile) != nil {
			return bad("profile with a display_name is required")
		}
		if name, _ := profile["display_name"].(string); strings.TrimSpace(name) == "" {
			return bad("profile with a display_name is required")
		}
		return result(p.api.do(ctx, http.MethodPost, base, nil, profile, writeTimeout))
	}
	if !strings.HasPrefix(req.URN, "msg://") || strings.ContainsAny(req.URN, " \t\n?#") {
		return bad("urn must be a msg:// URN")
	}
	item := base + "/" + url.PathEscape(req.URN)
	switch verb {
	case "registry_get":
		return result(p.api.do(ctx, http.MethodGet, item, nil, nil, readTimeout))
	case "registry_update":
		if len(req.Patch) == 0 {
			return bad("patch is required")
		}
		if s, _ := req.Patch["last_updated_by"].(string); strings.TrimSpace(s) == "" {
			req.Patch["last_updated_by"] = defaultUpdatedBy
		}
		return result(p.api.do(ctx, http.MethodPatch, item, nil, req.Patch, writeTimeout))
	case "registry_deregister":
		return result(p.api.do(ctx, http.MethodDelete, item, nil, nil, writeTimeout))
	case "registry_sync":
		return result(p.api.do(ctx, http.MethodPost, item+"/sync", nil, nil, writeTimeout))
	}
	return contract.Err("unknown_verb", "verb is not implemented"), nil
}

func decode(payload json.RawMessage, out any) bool {
	if len(payload) == 0 {
		return true
	}
	return json.Unmarshal(payload, out) == nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// result maps a Tether answer to a verb envelope. Transport details never
// leave the plugin; a 400/403/404/409 carries Tether's capped message because
// it describes the caller's own request (for example catalog validation).
func result(data json.RawMessage, err error) (contract.ResultEnvelope, error) {
	if err == nil {
		return contract.OK(data)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return contract.Err("timeout", "Tether request timed out; completion is unknown"), nil
	}
	if errors.Is(err, errResponseTooLarge) {
		return contract.Err("response_too_large", "Tether response exceeded size limit"), nil
	}
	var up *upstreamError
	if errors.As(err, &up) {
		code := "provider_error"
		switch up.status {
		case http.StatusBadRequest:
			code = "validation"
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusConflict:
			code = "conflict"
		case http.StatusForbidden:
			code = "forbidden"
		case http.StatusUnauthorized:
			code = "unauthenticated"
		}
		msg := fmt.Sprintf("Tether request failed (HTTP %d)", up.status)
		if up.message != "" {
			msg += ": " + up.message
		}
		return contract.Err(code, msg), nil
	}
	return contract.Err("provider_error", "Tether request failed"), nil
}

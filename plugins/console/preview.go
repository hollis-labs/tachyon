package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const previewPrefix = "/api/plugins/console/v1/preview"

// PreviewHandler serves the cite overlay's reads of Torque epics, projects,
// sprints and plans. The generic /v1 passthrough hands the whole Torque object to
// the browser with the hub bearer; these reads rebuild a short, bounded projection
// of what a preview shows instead: ids, status codes, counts and short truncated
// names (epic, project, sprint and plan titles, the sprint goal, phase names).
// Descriptions, filesystem paths and phase acceptance text are not named. The host mounts the prefix behind operator
// assurance on GET (RequireOperatorReads), like the Tether plan and agent reads.
type PreviewHandler struct{ route pluginkit.Route }

func NewPreviewHandler(route pluginkit.Route) *PreviewHandler { return &PreviewHandler{route: route} }

func (*PreviewHandler) Match(path string) bool { return strings.HasPrefix(path, previewPrefix+"/") }

type previewKind struct {
	id      *regexp.Regexp
	project func([]byte) ([]byte, error)
}

var previewKinds = map[string]previewKind{
	"epics":    {regexp.MustCompile(`^EP-\d{8}-\d{4}$`), projectEpic},
	"projects": {regexp.MustCompile(`^PRJ-\d{8}-\d{4}$`), projectProject},
	"sprints":  {regexp.MustCompile(`^SP-\d{8}-\d{4}$`), projectSprint},
	"plans":    {regexp.MustCompile(`^PL-\d{8}-\d{4}$`), projectTorquePlan},
}

func (h *PreviewHandler) Handle(ctx context.Context, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	parts := strings.Split(strings.TrimPrefix(req.Path, previewPrefix+"/"), "/")
	if len(parts) != 2 || req.Method != http.MethodGet {
		return forgeErr(404, "not_found", "Unknown preview")
	}
	kind, ok := previewKinds[parts[0]]
	if !ok || !kind.id.MatchString(parts[1]) {
		return forgeErr(404, "not_found", "Unknown preview")
	}
	if h.route.Upstream == "" {
		return forgeErr(503, "configuration", "Torque connection is not configured")
	}
	route := h.route
	route.Prefix, route.Target = "/v1/preview", strings.TrimRight(route.Target, "/")+"/"+parts[0]+"/"+parts[1]
	p := pluginkit.NewProxy("console", route)
	// Nothing from the browser is forwarded: no query, cookies or auth.
	req.Path, req.Query, req.RawQuery, req.Body = "/api/plugins/console/v1/preview", nil, "", nil
	req.Headers = map[string]string{"Accept": "application/json"}
	res, _ := p.HTTPHandle(ctx, req)
	if res.Status == 404 {
		return forgeErr(404, "not_found", "Not found")
	}
	if res.Status < 200 || res.Status >= 300 {
		return forgeErr(502, "upstream_error", "Torque request failed")
	}
	body, err := kind.project(res.Body)
	if err != nil {
		return forgeErr(502, "upstream_invalid", "Torque returned an unexpected response")
	}
	return subprocess.HTTPResponse{Status: 200, Body: body, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}}
}

func projectEpic(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pick(m, fieldSet{"id": fID, "name": fShort, "status": fCode, "priority": fNum, "project_id": fID, "created_at": fTime, "updated_at": fTime}))
}

func projectProject(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pick(m, fieldSet{"id": fID, "name": fShort, "status": fCode, "created_at": fTime, "updated_at": fTime}))
}

func projectSprint(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pick(m, fieldSet{"id": fID, "name": fShort, "goal": fShort, "status": fCode, "approval_mode": fCode, "project_id": fID, "epic_id": fID,
		"started_at": fTime, "ended_at": fTime, "created_at": fTime, "updated_at": fTime}))
}

// projectTorquePlan keeps the plan's task header, its phases and the progress
// counts. The task body (description, system prompt, working directory, files and
// the rest) is not named.
func projectTorquePlan(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"task": map[string]any{}, "plan": map[string]any{"phases": []any{}}, "progress": map[string]any{"by_phase": map[string]any{}}}
	if t, ok := m["task"].(map[string]any); ok {
		out["task"] = pick(t, fieldSet{"id": fID, "title": fShort, "status": fCode, "updated_at": fTime})
	}
	if pl, ok := m["plan"].(map[string]any); ok {
		phases := []any{}
		if raw, ok := pl["phases"].([]any); ok {
			for _, r := range raw {
				if ph, ok := r.(map[string]any); ok && len(phases) < 50 {
					phases = append(phases, pick(ph, fieldSet{"id": fID, "name": fShort, "order": fNum}))
				}
			}
		}
		out["plan"] = map[string]any{"phases": phases}
	}
	if pr, ok := m["progress"].(map[string]any); ok {
		progress := pick(pr, fieldSet{"total_children": fNum, "done": fNum, "blocked": fNum})
		byPhase := map[string]any{}
		if raw, ok := pr["by_phase"].(map[string]any); ok {
			for id, v := range raw {
				if r, ok := v.(map[string]any); ok && len(byPhase) < 50 && reID.MatchString(id) {
					byPhase[id] = pick(r, fieldSet{"total": fNum, "done": fNum, "blocked": fNum})
				}
			}
		}
		progress["by_phase"] = byPhase
		out["progress"] = progress
	}
	return json.Marshal(out)
}

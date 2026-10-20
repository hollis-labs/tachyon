package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const objectsPrefix = "/api/plugins/console/v1/tether"

var objectID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var projectURN = regexp.MustCompile(`^msg://(?:agent|project)/[a-zA-Z0-9_-]+/prj_[a-z0-9]+$`)

// ObjectHandler exposes the reviewed registry/workstream operations. The host
// checks operator assurance before forwarding mutations to this plugin.
type ObjectHandler struct{ tether *CatalogHandler }

func NewObjectHandler(addr, tokenFile string) *ObjectHandler {
	return &ObjectHandler{tether: NewCatalogHandler(addr, tokenFile)}
}

func (*ObjectHandler) Match(path string) bool { return strings.HasPrefix(path, objectsPrefix+"/") }

func (h *ObjectHandler) Handle(ctx context.Context, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	path := strings.TrimPrefix(req.Path, objectsPrefix)
	q, err := url.ParseQuery(req.RawQuery)
	if err != nil {
		return forgeErr(400, "invalid_request", "Invalid object query")
	}
	if req.RawQuery == "" {
		for k, v := range req.Query {
			q.Set(k, v)
		}
	}
	target := ""
	query := url.Values{}
	fields := ""
	switch {
	case path == "/events" && req.Method == "GET":
		target = "/events"
		query.Set("limit", "200")
		// Metadata only: never proxy raw session output or arbitrary event kinds.
		for _, kind := range activityEventKinds {
			query.Add("kind", kind)
		}
		for _, key := range []string{"since_seq", "cursor"} {
			if v := q.Get(key); v != "" {
				if n, e := strconv.ParseInt(v, 10, 64); e != nil || n < 0 {
					return forgeErr(400, "invalid_request", "Invalid event cursor")
				}
				query.Set(key, v)
			}
		}
		if id := q.Get("session_id"); id != "" {
			if !objectID.MatchString(id) {
				return forgeErr(400, "invalid_request", "Invalid session")
			}
			query.Set("session_id", id)
		}
	case path == "/binding" && req.Method == "GET":
		urn := q.Get("urn")
		if !activityRecipient.MatchString(urn) {
			return forgeErr(400, "invalid_request", "Invalid recipient")
		}
		target = "/registry/bindings"
		query.Set("target_urn", urn)
		query.Set("current", "true")
	case path == "/workers" && req.Method == "GET":
		target = "/workers"
	case path == "/sessions" && req.Method == "POST":
		target, fields = "/sessions", "launch worker prompt_append idempotency_key"
	case path == "/sessions" && req.Method == "GET":
		target = "/sessions"
		query.Set("state", "running")
		query.Set("limit", "1000")
		if cursor := q.Get("cursor"); cursor != "" {
			query.Set("cursor", cursor)
		}
	case path == "/projects" && req.Method == "GET":
		target = "/registry/projects"
		query.Set("include", "external_ids")
		status := q.Get("status")
		if status != "" && status != "active" && status != "deprecated" && status != "archived" && status != "merged" {
			return forgeErr(400, "invalid_request", "Invalid project status")
		}
		query.Set("status", status)
	case path == "/projects" && req.Method == "POST":
		target, fields = "/registry/projects", "display_name description tags guidelines entry_points"
	case path == "/projects/item" && (req.Method == "GET" || req.Method == "PATCH" || req.Method == "DELETE"):
		urn := q.Get("urn")
		if len(q["urn"]) != 1 || !projectURN.MatchString(urn) {
			return forgeErr(400, "invalid_request", "A registry project URN is required")
		}
		target = "/registry/projects/" + url.PathEscape(urn)
		if req.Method == "GET" {
			query.Set("include", "external_ids")
		}
		if req.Method == "PATCH" {
			fields = "display_name description tags guidelines entry_points status"
		}
	case path == "/workstreams" && req.Method == "GET":
		target = "/workstreams"
		status := q.Get("status")
		if status != "" && status != "active" && status != "closed" {
			return forgeErr(400, "invalid_request", "Invalid workstream status")
		}
		query.Set("status", status)
		if v := q.Get("workflow_id"); v != "" {
			query.Set("workflow_id", v)
		}
	case path == "/workstreams" && req.Method == "POST":
		target, fields = "/workstreams", "name workflow_id"
	default:
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) >= 2 && objectID.MatchString(parts[1]) {
			if len(parts) == 3 && req.Method == "GET" && ((parts[0] == "sessions" && parts[2] == "health") || (parts[0] == "messages" && parts[2] == "trace")) {
				target = path
			}
			if parts[0] == "sessions" && len(parts) == 2 && req.Method == "GET" {
				target = path
			}
			if parts[0] == "sessions" && len(parts) == 3 && parts[2] == "launch" && req.Method == "POST" {
				target, fields = path, " " // empty JSON object only
			}
			if parts[0] == "workstreams" && len(parts) == 2 && (req.Method == "GET" || req.Method == "PATCH") {
				target = path
				if req.Method == "PATCH" {
					fields = "name workflow_id status"
				}
			}
			if parts[0] == "sessions" && len(parts) == 3 && parts[2] == "workstream" && req.Method == "POST" {
				target, fields = path, "workstream_id ensure name workflow_id"
			}
		}
	}
	if target == "" {
		return forgeErr(404, "not_found", "Unknown project or workstream operation")
	}
	if h.tether.err != nil || h.tether.proxy == nil {
		return forgeErr(503, "configuration", "Tether connection or credential is not configured")
	}
	if fields != "" {
		if len(req.Body) > 1<<20 || !json.Valid(req.Body) {
			return forgeErr(400, "invalid_request", "A bounded JSON object is required")
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(req.Body, &body) != nil || body == nil {
			return forgeErr(400, "invalid_request", "A JSON object is required")
		}
		for k, v := range body {
			if !strings.Contains(" "+fields+" ", " "+k+" ") || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return forgeErr(400, "invalid_request", "Unsupported or null field: "+k)
			}
		}
		if path == "/projects/item" || path == "/projects" {
			body["last_updated_by"] = json.RawMessage(`"tachyon:operator"`)
		}
		req.Body, _ = json.Marshal(body)
	} else {
		req.Body = nil
	}
	// Build a fresh route per call: never mutate the shared proxy, and never
	// forward browser auth, callback selectors, or arbitrary upstream paths.
	p := *h.tether.proxy
	p.Routes = []pluginkit.Route{{Prefix: "/v1/tether", Upstream: p.Routes[0].Upstream, Target: target, Headers: map[string]string{"Tether-Protocol": "1"}}}
	req.Path, req.Query, req.RawQuery = objectsPrefix, nil, query.Encode()
	req.Headers = map[string]string{"Content-Type": "application/json"}
	res, _ := p.HTTPHandle(ctx, req)
	if res.Headers == nil {
		res.Headers = map[string]string{}
	}
	res.Headers["Cache-Control"] = "no-store"
	if res.Status >= 300 && res.Status < 400 {
		return forgeErr(502, "upstream_redirect", "Tether returned an unexpected redirect")
	}
	return res
}

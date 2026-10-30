package forgeapi

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// Routes are the forge routes the stub serves, relative to Prefix. StateRoutes are
// relative to StatePrefix. Mounted route by route, so an unknown path is a 404 here
// and never falls through to the unguarded /api/plugins/ catch-all.
var Routes = []string{
	"GET /pulls/pending", "GET /pulls", "GET /status", "GET /viewer", "GET /stream", "GET /audit", "GET /openapi.yaml",
	"GET /repos/{host}/{owner}/{name}", "GET /repos/{host}/{owner}/{name}/pulls",
	"GET /repos/{host}/{owner}/{name}/commits/{sha}",
	"GET /repos/{host}/{owner}/{name}/pulls/{num}", "GET /repos/{host}/{owner}/{name}/pulls/{num}/files",
	"GET /repos/{host}/{owner}/{name}/pulls/{num}/diff", "GET /repos/{host}/{owner}/{name}/pulls/{num}/commits",
	"GET /repos/{host}/{owner}/{name}/pulls/{num}/comments", "GET /repos/{host}/{owner}/{name}/pulls/{num}/checks",
	"GET /repos/{host}/{owner}/{name}/pulls/{num}/merge-preflight", "GET /repos/{host}/{owner}/{name}/pulls/{num}/review-draft",
	"POST /repos/{host}/{owner}/{name}/pulls/{num}/comments", "POST /repos/{host}/{owner}/{name}/pulls/{num}/reviews/validate",
	"POST /repos/{host}/{owner}/{name}/pulls/{num}/reviews", "PUT /repos/{host}/{owner}/{name}/pulls/{num}/merge",
	"PUT /repos/{host}/{owner}/{name}/pulls/{num}/assignees", "POST /repos/{host}/{owner}/{name}/pulls/{num}/assign-agent",
	"PUT /repos/{host}/{owner}/{name}/pulls/{num}/review-draft", "DELETE /repos/{host}/{owner}/{name}/pulls/{num}/review-draft",
}
var StateRoutes = []string{"GET ", "PUT /{key...}", "POST /bulk"}

// Stub serves the fixtures. Writes validate like the real routes will and change
// nothing durable; the only memory is the idempotency table.
type Stub struct{}

type memo struct {
	digest string
	status int
	body   []byte
}

var (
	idemMu sync.Mutex
	idem   = map[string]memo{}
)

func (Stub) Handler() http.Handler {
	m := http.NewServeMux()
	add := func(prefix string, routes []string) {
		for _, route := range routes {
			method, path, _ := strings.Cut(route, " ")
			m.HandleFunc(method+" "+prefix+path, wrap(path, method, handlerFor(prefix, route)))
		}
	}
	add(Prefix, Routes)
	add(StatePrefix, StateRoutes)
	return m
}

func handlerFor(prefix, route string) http.HandlerFunc {
	if prefix == StatePrefix {
		switch route {
		case "GET ":
			return stateList
		case "PUT /{key...}":
			return stateOp
		}
		return stateBulk
	}
	switch route {
	case "GET /pulls/pending":
		return pending
	case "GET /pulls":
		return func(w http.ResponseWriter, r *http.Request) { prList(w, r, "") }
	case "GET /status":
		return func(w http.ResponseWriter, r *http.Request) {
			name := "status"
			if r.Header.Get("X-Stub-Scenario") == "degraded" {
				name = "status_degraded"
			}
			ok(w, 200, raw(name))
		}
	case "GET /viewer":
		return func(w http.ResponseWriter, _ *http.Request) { ok(w, 200, raw("viewer")) }
	case "GET /stream":
		return stream
	case "GET /audit":
		return audit
	case "GET /openapi.yaml":
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(OpenAPI())
		}
	case "GET /repos/{host}/{owner}/{name}":
		return repoGet
	case "GET /repos/{host}/{owner}/{name}/pulls":
		return func(w http.ResponseWriter, r *http.Request) {
			if repo(w, r) {
				prList(w, r, r.PathValue("owner")+"/"+r.PathValue("name"))
			}
		}
	case "GET /repos/{host}/{owner}/{name}/commits/{sha}":
		return commitGet
	}
	switch {
	case strings.HasSuffix(route, "/pulls/{num}"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) { ok(w, 200, mapOf("pulls")[k]) })
	case strings.HasSuffix(route, "/files"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) {
			ok(w, 200, map[string]any{"items": mapOf("files")[k], "next_cursor": nil})
		})
	case strings.HasSuffix(route, "/diff"):
		return withPR(diff)
	case strings.HasSuffix(route, "/commits"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) {
			ok(w, 200, map[string]any{"items": mapOf("commits")[k]})
		})
	case strings.HasSuffix(route, "/comments") && strings.HasPrefix(route, "GET"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) {
			ok(w, 200, map[string]any{"items": mapOf("comments")[k]})
		})
	case strings.HasSuffix(route, "/checks"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) { ok(w, 200, mapOf("checks")[k]) })
	case strings.HasSuffix(route, "/merge-preflight"):
		return withPR(preflight)
	case strings.HasSuffix(route, "/review-draft") && strings.HasPrefix(route, "GET"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) {
			if k == "github:hollis-labs/tachyon#201" {
				ok(w, 200, raw("review_draft"))
				return
			}
			fail(w, 404, "not_found", "No saved review draft.")
		})
	case strings.HasSuffix(route, "/review-draft") && strings.HasPrefix(route, "PUT"):
		return withPR(draftPut)
	case strings.HasSuffix(route, "/review-draft"):
		return withPR(func(w http.ResponseWriter, r *http.Request, k string) { ok(w, 200, map[string]any{"deleted": true}) })
	case strings.HasSuffix(route, "/comments"):
		return withPR(write("comment", commentPost))
	case strings.HasSuffix(route, "/reviews/validate"):
		return withPR(reviewValidate)
	case strings.HasSuffix(route, "/reviews"):
		return withPR(write("review", reviewPost))
	case strings.HasSuffix(route, "/merge"):
		return withPR(write("merge", mergePut))
	case strings.HasSuffix(route, "/assignees"):
		return withPR(write("assign", assigneesPut))
	default:
		return withPR(write("assign_agent", assignAgent))
	}
}

func wrap(path, method string, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Tachyon-Forge-Stub", "1")
		stream := path == "/stream" || path == "/openapi.yaml"
		if s, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); s >= 400 && s < 600 && !stream {
			fail(w, s, errCode(s), "Forced by X-Stub-Status.")
			return
		}
		if !stream {
			switch r.Header.Get("X-Stub-Scenario") {
			case "rate_limited":
				w.Header().Set("Retry-After", "42")
				var e map[string]any
				load("rate_limited", &e)
				writeJSON(w, 429, map[string]any{"error": e})
				return
			case "credential_missing":
				fail(w, 401, "credential_missing", "GITHUB_TOKEN is not configured")
				return
			}
		}
		if method != "GET" {
			if r.Header.Get("Authorization") != "" {
				fail(w, 403, "requester_cannot_approve", "Use the operator browser session for forge writes.")
				return
			}
		}
		f(w, r)
	}
}

func errCode(s int) string {
	switch s {
	case 400:
		return "invalid_args"
	case 401:
		return "session_required"
	case 403:
		return "operator_access_required"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 429:
		return "rate_limited"
	case 501, 502, 503:
		return "unavailable"
	}
	return "internal"
}

func raw(name string) json.RawMessage {
	b, _ := assets.ReadFile("fixtures/" + name + ".json")
	return b
}

func load(name string, v any) bool {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	return err == nil && json.Unmarshal(b, v) == nil
}

func mapOf(name string) map[string]any {
	m := map[string]any{}
	load(name, &m)
	return m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}

func fail(w http.ResponseWriter, status int, code, msg string, extra ...any) {
	e := map[string]any{"code": code, "message": msg}
	for i := 0; i+1 < len(extra); i += 2 {
		e[extra[i].(string)] = extra[i+1]
	}
	writeJSON(w, status, map[string]any{"error": e})
}

func body(w http.ResponseWriter, r *http.Request, v any, max int64) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		fail(w, 400, "invalid_args", "Body is too large or unreadable.")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid_args", "Send a JSON object with the documented fields only.")
		return nil, false
	}
	return b, true
}

// repo validates the host and the repo allow-list (the fixtures' watched set).
func repo(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("host") != "github" {
		fail(w, 501, "unavailable", "unsupported forge host: "+r.PathValue("host"))
		return false
	}
	o, n := r.PathValue("owner"), r.PathValue("name")
	if !ValidRepoPart(o) || !ValidRepoPart(n) {
		fail(w, 400, "invalid_args", "owner and name may contain letters, digits, dot, dash and underscore only.")
		return false
	}
	if _, known := mapOf("repos")[o+"/"+n]; !known {
		fail(w, 403, "repo_not_allowed", "That repository is not in the watched set.")
		return false
	}
	return true
}

func withPR(f func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !repo(w, r) {
			return
		}
		num, err := strconv.Atoi(r.PathValue("num"))
		if err != nil || num <= 0 {
			fail(w, 400, "invalid_args", "invalid pull request number")
			return
		}
		k := PRKey(r.PathValue("owner"), r.PathValue("name"), num)
		if _, known := mapOf("pulls")[k]; !known {
			fail(w, 404, "not_found", "No such pull request.")
			return
		}
		f(w, r, k)
	}
}

func headOf(k string) string {
	var p struct {
		HeadSha string `json:"headSha"`
	}
	b, _ := json.Marshal(mapOf("pulls")[k])
	_ = json.Unmarshal(b, &p)
	return p.HeadSha
}

// write wraps a state-changing handler: Idempotency-Key is required, a repeat with
// the same body replays the stored response, and a different body is a conflict.
// The elevated-access scenario applies to ops whose WriteLevels entry says so.
func write(op string, f func(http.ResponseWriter, *http.Request, string)) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, k string) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || len(key) > 128 {
			fail(w, 400, "idempotency_key_required", "Send an Idempotency-Key header (1 to 128 characters).")
			return
		}
		if RequiredLevel(op) == "operator-token" && r.Header.Get("X-Stub-Scenario") == "not_elevated" {
			fail(w, 403, "operator_access_required", "Operator access is needed for this action.")
			return
		}
		b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxReviewBytes+4<<10))
		r.Body = io.NopCloser(bytes.NewReader(b))
		sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), b...))
		digest := hex.EncodeToString(sum[:])
		id := op + ":" + key
		idemMu.Lock()
		prev, seen := idem[id]
		idemMu.Unlock()
		if seen {
			if prev.digest != digest {
				fail(w, 409, "idempotency_conflict", "This Idempotency-Key was already used with a different request.")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Tachyon-Idempotent-Replay", "1")
			w.WriteHeader(prev.status)
			_, _ = w.Write(prev.body)
			return
		}
		rec := &recorder{ResponseWriter: w, status: 200}
		f(rec, r, k)
		if rec.status < 500 && rec.status != 429 {
			idemMu.Lock()
			if len(idem) < 512 {
				idem[id] = memo{digest, rec.status, rec.buf.Bytes()}
			}
			idemMu.Unlock()
		}
	}
}

type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(s int)           { r.status = s; r.ResponseWriter.WriteHeader(s) }
func (r *recorder) Write(b []byte) (int, error) { r.buf.Write(b); return r.ResponseWriter.Write(b) }

func pending(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if v := q.Get("reason"); v != "" && !slices.Contains(Reasons, v) {
		fail(w, 400, "invalid_args", "Unknown reason.")
		return
	}
	state := q.Get("state")
	switch state {
	case "", "active", "snoozed", "dismissed", "resolved":
	default:
		fail(w, 400, "invalid_args", "state must be active, snoozed, dismissed or resolved.")
		return
	}
	if state == "" {
		state = "active"
	}
	limit := PendingPage
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPendingDefault {
			fail(w, 400, "invalid_args", "limit must be 1 to 100.")
			return
		}
		limit = n
	}
	start := 0
	if c := q.Get("cursor"); c != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(c, "c"))
		if err != nil || !strings.HasPrefix(c, "c") || n < 0 {
			fail(w, 400, "invalid_args", "Unknown cursor.")
			return
		}
		start = n
	}
	var d struct {
		Items      []map[string]any `json:"items"`
		Total      int              `json:"total"`
		Cached     bool             `json:"cached"`
		FetchedMs  int64            `json:"fetched_ms"`
		Degraded   bool             `json:"degraded"`
		NextCursor *string          `json:"next_cursor"`
	}
	load("pending", &d)
	text := strings.ToLower(q.Get("q"))
	out := d.Items[:0:0]
	for _, it := range d.Items {
		tri, _ := it["triage"].(map[string]any)
		ts, _ := tri["status"].(string)
		if state != ts {
			continue
		}
		if v := q.Get("reason"); v != "" && !slices.Contains(anyStrings(it["reasons"]), v) {
			continue
		}
		if v := q.Get("repo"); v != "" {
			ref, _ := it["ref"].(map[string]any)
			rp, _ := ref["repo"].(map[string]any)
			if fmt.Sprint(rp["owner"], "/", rp["name"]) != v {
				continue
			}
		}
		if text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(it["title"], " ", it["pr_key"])), text) {
			continue
		}
		out = append(out, it)
	}
	d.Total = len(out)
	if start > len(out) {
		start = len(out)
	}
	end := min(start+limit, len(out))
	d.NextCursor = nil
	if end < len(out) {
		c := "c" + strconv.Itoa(end)
		d.NextCursor = &c
	}
	d.Items = out[start:end]
	if r.Header.Get("X-Stub-Scenario") == "degraded" {
		d.Cached, d.Degraded = true, true
	}
	ok(w, 200, d)
}

func anyStrings(v any) (out []string) {
	a, _ := v.([]any)
	for _, e := range a {
		out = append(out, fmt.Sprint(e))
	}
	return
}

func prList(w http.ResponseWriter, r *http.Request, repoFilter string) {
	items := []any{}
	pulls := mapOf("pulls")
	keys := make([]string, 0, len(pulls))
	for k := range pulls {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		o, n, _, _ := ParsePRKey(k)
		if repoFilter != "" && o+"/"+n != repoFilter {
			continue
		}
		if st := r.URL.Query().Get("state"); st != "" && pulls[k].(map[string]any)["state"] != st {
			continue
		}
		items = append(items, pulls[k])
	}
	ok(w, 200, map[string]any{"items": items, "nextCursor": nil})
}

func repoGet(w http.ResponseWriter, r *http.Request) {
	if repo(w, r) {
		ok(w, 200, mapOf("repos")[r.PathValue("owner")+"/"+r.PathValue("name")])
	}
}

func commitGet(w http.ResponseWriter, r *http.Request) {
	if !repo(w, r) {
		return
	}
	var c map[string]any
	load("commit_by_sha", &c)
	if r.PathValue("sha") != c["sha"] {
		fail(w, 404, "not_found", "No such commit.")
		return
	}
	ok(w, 200, c)
}

// diff serves all files, or one with ?path= (the lazy per-file patch).
func diff(w http.ResponseWriter, r *http.Request, k string) {
	files, _ := mapOf("diffs")[k].([]any)
	if p := r.URL.Query().Get("path"); p != "" {
		for _, f := range files {
			if f.(map[string]any)["path"] == p {
				ok(w, 200, map[string]any{"items": []any{f}})
				return
			}
		}
		fail(w, 404, "not_found", "No such file in this diff.")
		return
	}
	ok(w, 200, map[string]any{"items": files})
}

func preflight(w http.ResponseWriter, r *http.Request, k string) {
	if r.Header.Get("X-Stub-Scenario") == "mergeable_pending" {
		ok(w, 200, raw("preflight_pending"))
		return
	}
	ok(w, 200, mapOf("preflight")[k])
}

func commentPost(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		Body      string `json:"body"`
		Path      string `json:"path"`
		Line      *int   `json:"line"`
		Side      string `json:"side"`
		InReplyTo string `json:"inReplyTo"`
		CommitID  string `json:"commit_id"`
	}
	if _, good := body(w, r, &in, MaxCommentBytes+4<<10); !good {
		return
	}
	if strings.TrimSpace(in.Body) == "" || len(in.Body) > MaxCommentBytes {
		fail(w, 400, "invalid_args", "comment body is required (at most 64 KiB).")
		return
	}
	if in.Path != "" || in.Line != nil {
		if in.Path == "" || in.Line == nil || *in.Line < 1 || in.Side != "LEFT" && in.Side != "RIGHT" {
			fail(w, 400, "invalid_args", "An inline comment needs path, line and side (LEFT or RIGHT).")
			return
		}
		if !InlineAllowed(in.CommitID, headOf(k)) {
			fail(w, 409, "head_moved", "The pull request changed. Refresh and place the comment again.", "head_sha", headOf(k))
			return
		}
	}
	ok(w, 201, raw("comment_created"))
}

type reviewComment struct {
	Path string `json:"path"`
	Line *int   `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

func reviewValidate(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		HeadSha  string          `json:"head_sha"`
		Comments []reviewComment `json:"comments"`
	}
	if _, good := body(w, r, &in, MaxReviewBytes); !good {
		return
	}
	if !ValidHeadSHA(in.HeadSha) || len(in.Comments) > MaxInlineComments {
		fail(w, 400, "invalid_args", "head_sha must be a full commit id; at most 200 comments.")
		return
	}
	head := headOf(k)
	diffs, _ := mapOf("diffs")[k].([]any)
	res := make([]map[string]any, 0, len(in.Comments))
	for i, c := range in.Comments {
		st := "invalid_line"
		for _, f := range diffs {
			fm := f.(map[string]any)
			if fm["path"] != c.Path || c.Line == nil {
				continue
			}
			hunks, _ := fm["hunks"].([]any)
			for _, h := range hunks {
				for _, l := range h.(map[string]any)["lines"].([]any) {
					lm := l.(map[string]any)
					field := "newLine"
					if c.Side == "LEFT" {
						field = "oldLine"
					}
					if n, has := lm[field].(float64); has && int(n) == *c.Line {
						st = "ok"
					}
				}
			}
		}
		if in.HeadSha != head && st == "ok" {
			st = "outdated"
		}
		res = append(res, map[string]any{"index": i, "status": st})
	}
	ok(w, 200, map[string]any{"head_sha_current": head, "head_moved": in.HeadSha != head, "comments": res})
}

func reviewPost(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		Verdict  string          `json:"verdict"`
		Body     string          `json:"body"`
		Comments []reviewComment `json:"comments"`
		HeadSha  string          `json:"head_sha"`
	}
	if _, good := body(w, r, &in, MaxReviewBytes+4<<10); !good {
		return
	}
	if !slices.Contains(Verdicts, in.Verdict) || !ValidHeadSHA(in.HeadSha) || len(in.Comments) > MaxInlineComments || len(in.Body) > MaxReviewBytes {
		fail(w, 400, "invalid_args", "verdict, a full head_sha and at most 200 comments are required.")
		return
	}
	if (in.Verdict == "request_changes" || in.Verdict == "comment") && strings.TrimSpace(in.Body) == "" && len(in.Comments) == 0 {
		fail(w, 400, "invalid_args", "This verdict needs a body or comments.")
		return
	}
	if in.HeadSha != headOf(k) {
		fail(w, 409, "head_moved", "The pull request changed since you started this review.", "head_sha", headOf(k))
		return
	}
	var out map[string]any
	load("review_created", &out)
	out["state"] = map[string]string{"approve": "approved", "request_changes": "changes_requested", "comment": "commented"}[in.Verdict]
	ok(w, 201, out)
}

func mergePut(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		Method        string `json:"method"`
		HeadSha       string `json:"head_sha"`
		CommitTitle   string `json:"commit_title"`
		CommitMessage string `json:"commit_message"`
		DeleteBranch  bool   `json:"delete_branch"`
	}
	if _, good := body(w, r, &in, 64<<10); !good {
		return
	}
	if !slices.Contains(Methods, in.Method) || !ValidHeadSHA(in.HeadSha) || utf8.RuneCountInString(in.CommitTitle) > 256 {
		fail(w, 400, "invalid_args", "choose merge, squash or rebase, and send the full head_sha.")
		return
	}
	var pf struct {
		State    string `json:"state"`
		Blockers []any  `json:"blockers"`
	}
	b, _ := json.Marshal(mapOf("preflight")[k])
	_ = json.Unmarshal(b, &pf)
	if in.HeadSha != headOf(k) {
		fail(w, 409, "head_moved", "The pull request changed. Review it again before merging.", "head_sha", headOf(k))
		return
	}
	if pf.State != "ready" {
		fail(w, 409, "merge_blocked", "This pull request cannot be merged yet.", "blockers", pf.Blockers)
		return
	}
	var out map[string]any
	load("merge_ok", &out)
	out["method"] = in.Method
	ok(w, 200, out)
}

func assigneesPut(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		Assignees []string `json:"assignees"`
	}
	if _, good := body(w, r, &in, 16<<10); !good {
		return
	}
	if len(in.Assignees) == 0 || len(in.Assignees) > 10 {
		fail(w, 400, "invalid_args", "Send 1 to 10 assignee logins.")
		return
	}
	ok(w, 200, map[string]any{"assignees": in.Assignees})
}

// assignAgent: scenarios via X-Stub-Scenario: assign_failed (message failed after the
// assignee was set), assign_no_login (agent has no forge login). Agent ids:
// builder-reviewer, planner are known; others are 404; "offline-agent" has no URN (422).
func assignAgent(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		AgentID string   `json:"agent_id"`
		Task    string   `json:"task"`
		Note    string   `json:"note"`
		Checks  []string `json:"checks"`
		Force   bool     `json:"force"`
	}
	if _, good := body(w, r, &in, 16<<10); !good {
		return
	}
	if !slices.Contains(Tasks, in.Task) || in.AgentID == "" || utf8.RuneCountInString(in.Note) > 500 {
		fail(w, 400, "invalid_args", "agent_id and a task (review, fix_checks, address_comments, resolve_conflicts) are required.")
		return
	}
	switch in.AgentID {
	case "builder-reviewer", "planner":
	case "offline-agent":
		fail(w, 422, "agent_not_addressable", "That agent has no deliverable Tether address.")
		return
	default:
		fail(w, 404, "agent_not_found", "No such agent.")
		return
	}
	if st, _ := mapOf("pulls")[k].(map[string]any)["state"].(string); st != "open" {
		fail(w, 409, "pr_not_open", "Only open pull requests can be assigned.")
		return
	}
	name := "assign_sent"
	switch {
	case r.Header.Get("X-Stub-Scenario") == "assign_failed":
		name = "assign_failed"
	case r.Header.Get("X-Stub-Scenario") == "assign_no_login":
		name = "assign_no_login"
	case in.Task == "fix_checks":
		name = "assign_fix_checks"
	case k == "github:hollis-labs/tachyon#203" && in.AgentID == "builder-reviewer" && in.Task == "review":
		name = "assign_replayed"
		if in.Force {
			name = "assign_reminded"
		}
	}
	var out map[string]any
	load(name, &out)
	lk := out["link"].(map[string]any)
	lk["pr_key"], lk["agent_id"], lk["task"] = k, in.AgentID, in.Task
	lk["dedupe_key"] = DedupeKey(k, in.AgentID, in.Task)
	lk["agent_urn"] = "msg://agent/agent-mux/" + in.AgentID
	if in.Note != "" {
		lk["note"] = in.Note
	}
	ok(w, 200, out)
}

func draftPut(w http.ResponseWriter, r *http.Request, k string) {
	var in struct {
		HeadSha string          `json:"head_sha"`
		Draft   json.RawMessage `json:"draft"`
	}
	if _, good := body(w, r, &in, MaxReviewBytes); !good {
		return
	}
	if !ValidHeadSHA(in.HeadSha) || len(in.Draft) == 0 {
		fail(w, 400, "invalid_args", "head_sha and draft are required.")
		return
	}
	ok(w, 200, map[string]any{"pr_key": k, "head_sha": in.HeadSha, "updated_ms": time.Now().UnixMilli()})
}

func audit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(w, 400, "invalid_args", "limit must be 1 to 200.")
			return
		}
		limit = n
	}
	var d struct {
		Entries []any `json:"entries"`
	}
	load("audit", &d)
	if len(d.Entries) > limit {
		d.Entries = d.Entries[:limit]
	}
	ok(w, 200, d)
}

// ---- triage state (the console StateStore) ----

func stateList(w http.ResponseWriter, r *http.Request) {
	var rows []map[string]any
	load("state", &rows)
	p := r.URL.Query().Get("prefix")
	out := rows[:0:0]
	for _, s := range rows {
		if strings.HasPrefix(fmt.Sprint(s["key"]), p) {
			out = append(out, s)
		}
	}
	ok(w, 200, out)
}

type stateBody struct {
	Type            string          `json:"type"`
	Note            string          `json:"note"`
	Until           json.RawMessage `json:"until"`
	Preset          string          `json:"preset"`
	Resolution      string          `json:"resolution"`
	ExpectedVersion *int            `json:"expected_version"`
}

func applyOp(key string, b stateBody, now time.Time) (map[string]any, string) {
	if !slices.Contains(TriageOps, b.Type) {
		return nil, "type must be one of read, dismiss, snooze, settle, reopen."
	}
	if !strings.HasPrefix(key, "pr-inbox:") && !strings.HasPrefix(key, "pr-assign:") || len(key) > 200 {
		return nil, "key must start with pr-inbox: or pr-assign:."
	}
	s := map[string]any{"key": key, "by": "chrispian", "updatedAt": now.UnixMilli(), "version": 2}
	switch b.Type {
	case "read":
		s["status"] = "read"
	case "dismiss":
		s["status"] = "dismissed"
	case "reopen":
		s["status"] = "unread"
	case "settle":
		s["status"] = "settled"
		if b.Resolution != "" {
			s["resolution"] = b.Resolution
		}
	case "snooze":
		var until time.Time
		switch b.Preset {
		case "1h":
			until = now.Add(time.Hour)
		case "4h":
			until = now.Add(4 * time.Hour)
		case "tomorrow":
			until = now.Add(24 * time.Hour)
		case "":
			t, err := parseUntil(b.Until)
			if err != nil || !t.After(now) || t.After(now.Add(90*24*time.Hour)) {
				return nil, "snooze needs a preset (1h, 4h, tomorrow) or a future until within 90 days."
			}
			until = t
		default:
			return nil, "unknown snooze preset."
		}
		s["status"] = "snoozed"
		s["snoozeUntil"] = until.UnixMilli()
	}
	if b.Note != "" {
		s["note"] = b.Note
	}
	return s, ""
}

func stateOp(w http.ResponseWriter, r *http.Request) {
	var b stateBody
	if _, good := body(w, r, &b, 16<<10); !good {
		return
	}
	if b.ExpectedVersion != nil && *b.ExpectedVersion != 1 {
		fail(w, 409, "conflict", "The entry changed. Reload it.", "version", 1)
		return
	}
	s, msg := applyOp(r.PathValue("key"), b, time.Now())
	if msg != "" {
		fail(w, 400, "invalid_args", msg)
		return
	}
	ok(w, 200, s)
}

func stateBulk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ops []struct {
			Key string `json:"key"`
			stateBody
		} `json:"ops"`
	}
	if _, good := body(w, r, &in, 128<<10); !good {
		return
	}
	if len(in.Ops) == 0 || len(in.Ops) > 100 {
		fail(w, 400, "invalid_args", "Send 1 to 100 ops.")
		return
	}
	results := make([]map[string]any, 0, len(in.Ops))
	for _, o := range in.Ops {
		s, msg := applyOp(o.Key, o.stateBody, time.Now())
		if msg != "" {
			results = append(results, map[string]any{"key": o.Key, "ok": false, "error": map[string]string{"code": "invalid_args", "message": msg}})
			continue
		}
		results = append(results, map[string]any{"key": o.Key, "ok": true, "state": s})
	}
	ok(w, 200, map[string]any{"results": results})
}

// ---- SSE ----

type scripted struct {
	ID   int64          `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func stream(w http.ResponseWriter, r *http.Request) {
	var evs []scripted
	load("events", &evs)
	var last int64
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid_args", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last = n
	}
	fl, can := w.(http.Flusher)
	if !can {
		fail(w, 500, "internal", "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Tachyon-Forge-Stub", "1")
	w.WriteHeader(200)
	base := time.Now().UnixMilli()
	if r.Header.Get("X-Stub-Scenario") == "gap" || last > evs[len(evs)-1].ID {
		body, _ := json.Marshal(map[string]any{"type": "resync", "at": base, "reason": "gap"})
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", evs[len(evs)-1].ID, body)
	} else {
		for i, e := range evs {
			if e.ID <= last {
				continue
			}
			env := map[string]any{"type": e.Type, "at": base + int64(i)}
			for k, v := range e.Data {
				env[k] = v
			}
			body, _ := json.Marshal(env)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.ID, body)
		}
	}
	fl.Flush()
	if r.URL.Query().Get("once") == "1" {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

// parseUntil reads a snooze deadline: epoch milliseconds (what the client sends) or an RFC 3339 string.
func parseUntil(raw json.RawMessage) (time.Time, error) {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return time.UnixMilli(n), nil
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return time.Parse(time.RFC3339, str)
	}
	return time.Time{}, errors.New("until must be epoch ms or RFC 3339")
}

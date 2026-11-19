// Package forgereview is the review side of the Pull requests backend (PR6): checking queued inline comments
// against the current diff, submitting a review as ONE GitHub review pinned to the head the reviewer saw, and
// keeping a draft per pull request. GitHub text is untrusted and only ever returned as JSON strings. Design:
// design_tachyon_pull_requests_backend_20261030, sections 3 (routes 6, 7, 11).
package forgereview

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const base = forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}"

// Routes are the patterns Handler serves, each mounted explicitly by the server.
var Routes = []string{
	"POST " + base + "/reviews/validate",
	"POST " + base + "/reviews",
	"GET " + base + "/review-draft",
	"PUT " + base + "/review-draft",
	"DELETE " + base + "/review-draft",
}

const (
	maxPages    = 30
	maxKey      = 128
	principal   = "operator" // one operator; the draft is per pull request
	markerOpen  = "<!-- tachyon:idem="
	markerClose = " -->"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Handler talks to GitHub with one token.
type Handler struct {
	Token string
	Base  string // default https://api.github.com
	HTTP  *http.Client
	DB    *localstore.Store
	Now   func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Mux serves the five routes.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	fs := []http.HandlerFunc{h.validate, h.submit, h.draftGet, h.draftPut, h.draftDelete}
	for i, r := range Routes {
		m.HandleFunc(r, fs[i])
	}
	return m
}

// ---- wire ----

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiErr struct {
	status    int
	code, msg string
	extra     map[string]any
}

func (e *apiErr) Error() string { return e.code }

func fail(w http.ResponseWriter, e *apiErr) {
	b := map[string]any{"code": e.code, "message": e.msg}
	for k, v := range e.extra {
		b[k] = v
	}
	send(w, e.status, map[string]any{"error": b})
}

func bad(msg string) *apiErr { return &apiErr{status: 400, code: "invalid_args", msg: msg} }

func headMoved(sha string) *apiErr {
	return &apiErr{status: 409, code: "head_moved", msg: "The pull request changed since you started this review.", extra: map[string]any{"head_sha": sha}}
}

func readBody(w http.ResponseWriter, r *http.Request, max int64, v any) *apiErr {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err != nil || dec.Decode(v) != nil {
		return bad("Send a JSON object with the documented fields only.")
	}
	return nil
}

func target(r *http.Request) (owner, name string, num int, e *apiErr) {
	if r.PathValue("host") != "github" {
		return "", "", 0, &apiErr{status: 501, code: "unavailable", msg: "Only GitHub is supported."}
	}
	owner, name = r.PathValue("owner"), r.PathValue("name")
	if !forgeapi.ValidRepoPart(owner) || !forgeapi.ValidRepoPart(name) {
		return "", "", 0, bad("Invalid repository.")
	}
	n, err := strconv.Atoi(r.PathValue("num"))
	if err != nil || n < 1 {
		return "", "", 0, bad("Invalid pull request number.")
	}
	return owner, name, n, nil
}

// ---- GitHub ----

func (h *Handler) do(ctx context.Context, method, path string, in, out any) (int, *apiErr) {
	if h.Token == "" {
		return 0, &apiErr{status: 401, code: "credential_missing", msg: "No GitHub token is configured."}
	}
	root := strings.TrimRight(h.Base, "/")
	if root == "" {
		root = "https://api.github.com"
	}
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, root+path, rd)
	if err != nil {
		return 0, bad("Bad request.")
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := h.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, &apiErr{status: 502, code: "unavailable", msg: "GitHub could not be reached."}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	switch {
	case resp.StatusCode == 401:
		return resp.StatusCode, &apiErr{status: 401, code: "credential_missing", msg: "GitHub refused the configured token."}
	case resp.StatusCode == 429 || (resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0"):
		e := &apiErr{status: 429, code: "rate_limited", msg: "GitHub rate limit reached."}
		if s, _ := strconv.Atoi(resp.Header.Get("Retry-After")); s > 0 {
			e.extra = map[string]any{"retry_after_s": s}
		}
		return resp.StatusCode, e
	case resp.StatusCode == 404:
		return resp.StatusCode, &apiErr{status: 404, code: "not_found", msg: "GitHub does not have that."}
	case resp.StatusCode == 422 || resp.StatusCode == 403:
		// GitHub refused the content or the actor (an inline comment off the diff, approving one's own PR).
		return resp.StatusCode, &apiErr{status: 422, code: "invalid_args", msg: ghMessage(raw, "GitHub refused the review.")}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return resp.StatusCode, &apiErr{status: 502, code: "unavailable", msg: "GitHub answered with an error."}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, &apiErr{status: 502, code: "unavailable", msg: "GitHub sent something unreadable."}
		}
	}
	return resp.StatusCode, nil
}

func ghMessage(raw []byte, fallback string) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &m) != nil || strings.TrimSpace(m.Message) == "" {
		return fallback
	}
	s := strings.TrimSpace(m.Message)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (h *Handler) head(ctx context.Context, owner, name string, num int) (string, *apiErr) {
	var p struct {
		State string `json:"state"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if _, e := h.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(owner, name), num), nil, &p); e != nil {
		return "", e
	}
	return p.Head.SHA, nil
}

// ---- validate ----

// Comment is one inline review comment.
type Comment struct {
	Path string `json:"path"`
	Line *int   `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

func validComment(c Comment) bool {
	return c.Path != "" && len(c.Path) <= 1024 && c.Line != nil && *c.Line > 0 && (c.Side == "LEFT" || c.Side == "RIGHT") && c.Body != ""
}

type ghFile struct {
	Filename string `json:"filename"`
	Patch    string `json:"patch"`
}

// diffLines maps each file to the lines a comment may sit on: new-file lines (RIGHT) and old-file lines
// (LEFT) that appear in a hunk.
type diffLines map[string]struct{ left, right map[int]bool }

func (h *Handler) diff(ctx context.Context, owner, name string, num int) (diffLines, *apiErr) {
	out := diffLines{}
	for page := 1; page <= maxPages; page++ {
		var files []ghFile
		if _, e := h.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/files?per_page=100&page=%d", repoPath(owner, name), num, page), nil, &files); e != nil {
			return nil, e
		}
		for _, f := range files {
			out[f.Filename] = parsePatch(f.Patch)
		}
		if len(files) < 100 {
			break
		}
	}
	return out, nil
}

func parsePatch(patch string) struct{ left, right map[int]bool } {
	res := struct{ left, right map[int]bool }{map[int]bool{}, map[int]bool{}}
	var oldN, newN int
	in := false
	for _, ln := range strings.Split(patch, "\n") {
		if m := hunkHeader.FindStringSubmatch(ln); m != nil {
			oldN, _ = strconv.Atoi(m[1])
			newN, _ = strconv.Atoi(m[2])
			in = true
			continue
		}
		if !in {
			continue
		}
		switch {
		case strings.HasPrefix(ln, "+"):
			res.right[newN] = true
			newN++
		case strings.HasPrefix(ln, "-"):
			res.left[oldN] = true
			oldN++
		case strings.HasPrefix(ln, `\`):
		default:
			res.left[oldN], res.right[newN] = true, true
			oldN++
			newN++
		}
	}
	return res
}

func (h *Handler) validate(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := target(r)
	if e != nil {
		fail(w, e)
		return
	}
	var in struct {
		HeadSHA  string    `json:"head_sha"`
		Comments []Comment `json:"comments"`
	}
	if e := readBody(w, r, forgeapi.MaxReviewBytes, &in); e != nil {
		fail(w, e)
		return
	}
	if !forgeapi.ValidHeadSHA(in.HeadSHA) || len(in.Comments) > forgeapi.MaxInlineComments {
		fail(w, bad("head_sha must be a full commit id; at most 200 comments."))
		return
	}
	cur, e := h.head(r.Context(), owner, name, num)
	if e != nil {
		fail(w, e)
		return
	}
	lines, e := h.diff(r.Context(), owner, name, num)
	if e != nil {
		fail(w, e)
		return
	}
	moved := cur != in.HeadSHA
	res := make([]map[string]any, 0, len(in.Comments))
	for i, c := range in.Comments {
		st := "invalid_line"
		if f, ok := lines[c.Path]; ok && validComment(c) {
			set := f.right
			if c.Side == "LEFT" {
				set = f.left
			}
			if set[*c.Line] {
				st = "ok"
				if moved {
					// The line exists now, but the comment was written against another head: the reviewer decides.
					st = "outdated"
				}
			}
		}
		res = append(res, map[string]any{"index": i, "status": st})
	}
	send(w, 200, map[string]any{"data": map[string]any{"head_sha_current": cur, "head_moved": moved, "comments": res}})
}

// ---- submit ----

var events = map[string]string{"approve": "APPROVE", "request_changes": "REQUEST_CHANGES", "comment": "COMMENT"}
var states = map[string]string{"approve": "approved", "request_changes": "changes_requested", "comment": "commented"}

type ghReview struct {
	ID    int64  `json:"id"`
	Body  string `json:"body"`
	State string `json:"state"`
}

func marker(key string) string { return markerOpen + key + markerClose }

func (h *Handler) commentIDs(ctx context.Context, repo string, num int, id int64) []string {
	var cs []struct {
		ID int64 `json:"id"`
	}
	ids := []string{}
	if _, e := h.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews/%d/comments?per_page=100", repo, num, id), nil, &cs); e == nil {
		for _, c := range cs {
			ids = append(ids, strconv.FormatInt(c.ID, 10))
		}
	}
	return ids
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := target(r)
	if e != nil {
		fail(w, e)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		fail(w, &apiErr{status: 400, code: "idempotency_key_required", msg: "Send an Idempotency-Key header (1 to 128 characters)."})
		return
	}
	if len(key) > maxKey || strings.ContainsAny(key, "\r\n>") {
		fail(w, bad("Idempotency-Key is at most 128 characters, without line breaks or '>'."))
		return
	}
	var in struct {
		Verdict  string    `json:"verdict"`
		Body     string    `json:"body"`
		Comments []Comment `json:"comments"`
		HeadSHA  string    `json:"head_sha"`
	}
	if e := readBody(w, r, forgeapi.MaxReviewBytes+4<<10, &in); e != nil {
		fail(w, e)
		return
	}
	if !slices.Contains(forgeapi.Verdicts, in.Verdict) || !forgeapi.ValidHeadSHA(in.HeadSHA) || len(in.Comments) > forgeapi.MaxInlineComments || len(in.Body) > forgeapi.MaxReviewBytes {
		fail(w, bad("verdict, a full head_sha and at most 200 comments are required."))
		return
	}
	if in.Verdict != "approve" && strings.TrimSpace(in.Body) == "" && len(in.Comments) == 0 {
		fail(w, bad("This verdict needs a body or comments."))
		return
	}
	for _, c := range in.Comments {
		if !validComment(c) {
			fail(w, bad("Every inline comment needs a path, a line, a side (LEFT or RIGHT) and a body."))
			return
		}
	}
	ctx := r.Context()
	repo := repoPath(owner, name)
	cur, e := h.head(ctx, owner, name, num)
	if e != nil {
		fail(w, e)
		return
	}
	// A retry finds its own earlier review by the marker instead of posting a second one.
	var prior []ghReview
	if _, e := h.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", repo, num), nil, &prior); e != nil {
		fail(w, e)
		return
	}
	for _, p := range prior {
		if strings.Contains(p.Body, marker(key)) {
			send(w, 200, map[string]any{"data": map[string]any{"review_id": strconv.FormatInt(p.ID, 10), "comment_ids": h.commentIDs(ctx, repo, num, p.ID),
				"state": states[in.Verdict], "replayed": true}})
			return
		}
	}
	if cur != in.HeadSHA {
		fail(w, headMoved(cur))
		return
	}
	text := in.Body
	if text != "" {
		text += "\n\n"
	}
	payload := map[string]any{"commit_id": in.HeadSHA, "event": events[in.Verdict], "body": text + marker(key)}
	if len(in.Comments) > 0 {
		cs := make([]map[string]any, 0, len(in.Comments))
		for _, c := range in.Comments {
			cs = append(cs, map[string]any{"path": c.Path, "line": *c.Line, "side": c.Side, "body": c.Body})
		}
		payload["comments"] = cs
	}
	var created ghReview
	if _, e := h.do(ctx, http.MethodPost, fmt.Sprintf("%s/pulls/%d/reviews", repo, num), payload, &created); e != nil {
		fail(w, e)
		return
	}
	send(w, 201, map[string]any{"data": map[string]any{"review_id": strconv.FormatInt(created.ID, 10), "comment_ids": h.commentIDs(ctx, repo, num, created.ID), "state": states[in.Verdict]}})
}

// ---- drafts ----

func (h *Handler) draftKey(r *http.Request) (string, *apiErr) {
	owner, name, num, e := target(r)
	if e != nil {
		return "", e
	}
	if h.DB == nil {
		return "", &apiErr{status: 503, code: "unavailable", msg: "Drafts need the local store."}
	}
	return forgeapi.PRKey(owner, name, num), nil
}

func (h *Handler) draftGet(w http.ResponseWriter, r *http.Request) {
	k, e := h.draftKey(r)
	if e != nil {
		fail(w, e)
		return
	}
	var sha, body string
	var updated int64
	err := h.DB.Reader.QueryRowContext(r.Context(), `SELECT head_sha, body_json, updated_ms FROM forge_review_drafts WHERE pr_key = ? AND principal = ?`, k, principal).Scan(&sha, &body, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, &apiErr{status: 404, code: "not_found", msg: "No saved review draft."})
		return
	}
	if err != nil {
		fail(w, &apiErr{status: 503, code: "unavailable", msg: "The draft could not be read."})
		return
	}
	send(w, 200, map[string]any{"data": map[string]any{"pr_key": k, "head_sha": sha, "updated_ms": updated, "draft": json.RawMessage(body)}})
}

func (h *Handler) draftPut(w http.ResponseWriter, r *http.Request) {
	k, e := h.draftKey(r)
	if e != nil {
		fail(w, e)
		return
	}
	var in struct {
		HeadSHA string          `json:"head_sha"`
		Draft   json.RawMessage `json:"draft"`
	}
	if e := readBody(w, r, forgeapi.MaxReviewBytes, &in); e != nil {
		fail(w, e)
		return
	}
	var obj map[string]any
	if !forgeapi.ValidHeadSHA(in.HeadSHA) || json.Unmarshal(in.Draft, &obj) != nil || obj == nil {
		fail(w, bad("head_sha and draft (an object) are required."))
		return
	}
	now := h.now().UnixMilli()
	err := h.DB.Writer.Submit(r.Context(), "forge_review_draft_save", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO forge_review_drafts(pr_key, principal, head_sha, body_json, updated_ms) VALUES (?,?,?,?,?)
 ON CONFLICT(pr_key, principal) DO UPDATE SET head_sha = excluded.head_sha, body_json = excluded.body_json, updated_ms = excluded.updated_ms`,
			k, principal, in.HeadSHA, string(in.Draft), now)
		return err
	})
	if err != nil {
		fail(w, &apiErr{status: 503, code: "unavailable", msg: "The draft could not be saved."})
		return
	}
	send(w, 200, map[string]any{"data": map[string]any{"pr_key": k, "head_sha": in.HeadSHA, "updated_ms": now}})
}

func (h *Handler) draftDelete(w http.ResponseWriter, r *http.Request) {
	k, e := h.draftKey(r)
	if e != nil {
		fail(w, e)
		return
	}
	err := h.DB.Writer.Submit(r.Context(), "forge_review_draft_delete", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM forge_review_drafts WHERE pr_key = ? AND principal = ?`, k, principal)
		return err
	})
	if err != nil {
		fail(w, &apiErr{status: 503, code: "unavailable", msg: "The draft could not be deleted."})
		return
	}
	send(w, 200, map[string]any{"data": map[string]any{"deleted": true}})
}

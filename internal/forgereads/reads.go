// Package forgereads serves the Pull requests read endpoints that need GitHub (PR4): changed files,
// the diff (all files, or one file lazily with ?path=), checks at the head sha, a commit ref resolver
// and the token owner. Reads only; the token stays on the server. GitHub text is untrusted data and
// is returned as JSON strings, never as HTML. Design: design_tachyon_pull_requests_backend_20261030.
package forgereads

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
)

// Routes are the patterns Handler serves, each mounted explicitly by the server.
var Routes = []string{
	"GET " + forgeapi.Prefix + "/viewer",
	"GET " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/commits/{sha}",
	"GET " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}/files",
	"GET " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}/diff",
	"GET " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}/checks",
}

const (
	maxPages     = 30 // GitHub lists at most 3000 files per PR
	perPage      = 100
	summaryLimit = 500
	filesTTL     = 30 * time.Second
	maxCached    = 64
)

var (
	refShape     = regexp.MustCompile(`^[A-Za-z0-9._-]{4,100}$`)
	hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?:\s?(.*))?$`)
)

// Handler reads from GitHub with one token.
type Handler struct {
	Token string
	Base  string // default https://api.github.com
	HTTP  *http.Client
	Now   func() time.Time

	mu    sync.Mutex
	files map[string]cachedFiles
}

type cachedFiles struct {
	at    time.Time
	files []ghFile
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	hs := []http.HandlerFunc{h.viewer, h.commit, h.filesList, h.diff, h.checks}
	for i, r := range Routes {
		m.HandleFunc(r, hs[i])
	}
	return m
}

// ---- wire ----

func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

type apiErr struct {
	status     int
	code, msg  string
	retryAfter int
}

func (e *apiErr) Error() string { return e.code }

func fail(w http.ResponseWriter, e *apiErr) {
	body := map[string]any{"code": e.code, "message": e.msg}
	if e.retryAfter > 0 {
		body["retry_after_s"] = e.retryAfter
		w.Header().Set("Retry-After", strconv.Itoa(e.retryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

func bad(msg string) *apiErr { return &apiErr{status: 400, code: "invalid_args", msg: msg} }

// ---- GitHub ----

func (h *Handler) get(ctx context.Context, path string, v any) *apiErr {
	if h.Token == "" {
		return &apiErr{status: 401, code: "credential_missing", msg: "No GitHub token is configured."}
	}
	base := strings.TrimRight(h.Base, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return bad("Bad request.")
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	c := h.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return &apiErr{status: 502, code: "unavailable", msg: "GitHub could not be reached."}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return &apiErr{status: 404, code: "not_found", msg: "GitHub does not have that."}
	case resp.StatusCode == 401:
		return &apiErr{status: 401, code: "credential_missing", msg: "GitHub refused the configured token."}
	case resp.StatusCode == 429 || (resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0"):
		return &apiErr{status: 429, code: "rate_limited", msg: "GitHub rate limit reached.", retryAfter: h.retryAfter(resp)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return &apiErr{status: 502, code: "unavailable", msg: "GitHub answered with an error."}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(v); err != nil {
		return &apiErr{status: 502, code: "unavailable", msg: "GitHub sent something unreadable."}
	}
	return nil
}

func (h *Handler) retryAfter(resp *http.Response) int {
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		return s
	}
	if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		if d := int(reset - h.now().Unix()); d > 0 {
			return d
		}
	}
	return 60
}

type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Type      string `json:"type"`
}

type user struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
	IsAgent   bool   `json:"isAgent,omitempty"`
}

func toUser(u ghUser) user {
	return user{Login: u.Login, AvatarURL: u.AvatarURL, IsAgent: strings.EqualFold(u.Type, "Bot") || strings.HasSuffix(u.Login, "[bot]")}
}

// target validates the path variables shared by the repo routes.
func target(r *http.Request) (owner, name string, e *apiErr) {
	if r.PathValue("host") != "github" {
		return "", "", &apiErr{status: 501, code: "unavailable", msg: "Only GitHub is supported."}
	}
	owner, name = r.PathValue("owner"), r.PathValue("name")
	if !forgeapi.ValidRepoPart(owner) || !forgeapi.ValidRepoPart(name) {
		return "", "", bad("Invalid repository.")
	}
	return owner, name, nil
}

func prTarget(r *http.Request) (owner, name string, num int, e *apiErr) {
	owner, name, e = target(r)
	if e != nil {
		return
	}
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil || num < 1 {
		return "", "", 0, bad("Invalid pull request number.")
	}
	return owner, name, num, nil
}

// ---- viewer ----

func (h *Handler) viewer(w http.ResponseWriter, r *http.Request) {
	var u ghUser
	if e := h.get(r.Context(), "/user", &u); e != nil {
		fail(w, e)
		return
	}
	send(w, map[string]any{"login": u.Login, "avatarUrl": u.AvatarURL})
}

// ---- commit ref resolver ----

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) {
	owner, name, e := target(r)
	if e != nil {
		fail(w, e)
		return
	}
	ref := r.PathValue("sha")
	if !refShape.MatchString(ref) {
		fail(w, bad("Invalid commit ref."))
		return
	}
	var c struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Author  *ghUser
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
		Files []json.RawMessage `json:"files"`
	}
	if e := h.get(r.Context(), "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/commits/"+url.PathEscape(ref), &c); e != nil {
		fail(w, e)
		return
	}
	author := user{Login: c.Commit.Author.Name}
	if c.Author != nil && c.Author.Login != "" {
		author = toUser(*c.Author)
	}
	parents := make([]string, 0, len(c.Parents))
	for _, p := range c.Parents {
		parents = append(parents, p.SHA)
	}
	send(w, map[string]any{"sha": c.SHA, "message": c.Commit.Message, "author": author, "authoredAt": c.Commit.Author.Date,
		"parents": parents, "url": c.HTMLURL, "files": len(c.Files)})
}

// ---- files and diff ----

type ghFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

// page reads one page of a PR's files.
func (h *Handler) page(ctx context.Context, owner, name string, num, pg int) ([]ghFile, *apiErr) {
	var fs []ghFile
	p := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=%d&page=%d", url.PathEscape(owner), url.PathEscape(name), num, perPage, pg)
	return fs, h.get(ctx, p, &fs)
}

// allFiles reads every file (up to GitHub's 3000) and keeps the list for 30 s, so opening several
// files of one PR does not re-fetch it each time.
func (h *Handler) allFiles(ctx context.Context, owner, name string, num int) ([]ghFile, *apiErr) {
	key := fmt.Sprintf("%s/%s#%d", owner, name, num)
	h.mu.Lock()
	if c, ok := h.files[key]; ok && h.now().Sub(c.at) < filesTTL {
		h.mu.Unlock()
		return c.files, nil
	}
	h.mu.Unlock()
	var all []ghFile
	for pg := 1; pg <= maxPages; pg++ {
		fs, e := h.page(ctx, owner, name, num, pg)
		if e != nil {
			return nil, e
		}
		all = append(all, fs...)
		if len(fs) < perPage {
			break
		}
	}
	h.mu.Lock()
	if h.files == nil || len(h.files) >= maxCached {
		h.files = map[string]cachedFiles{}
	}
	h.files[key] = cachedFiles{at: h.now(), files: all}
	h.mu.Unlock()
	return all, nil
}

type fileMeta struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

// classify mirrors the legacy diff rules: no patch with changed lines is a patch GitHub withheld
// (truncated), no patch and no changes is binary, and a patch over 200 KiB is cut.
func classify(f ghFile) (m fileMeta, usePatch bool) {
	m = fileMeta{Path: f.Filename, OldPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions}
	switch {
	case f.Patch == "" && (f.Additions > 0 || f.Deletions > 0):
		m.Truncated = true
	case f.Patch == "":
		m.Binary = true
	case len(f.Patch) > forgeapi.DiffTruncateBytes:
		m.Truncated = true
	default:
		usePatch = true
	}
	return m, usePatch
}

func (h *Handler) filesList(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := prTarget(r)
	if e != nil {
		fail(w, e)
		return
	}
	pg := 1
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 || n > maxPages {
			fail(w, bad("Invalid cursor."))
			return
		}
		pg = n
	}
	fs, e := h.page(r.Context(), owner, name, num, pg)
	if e != nil {
		fail(w, e)
		return
	}
	items := make([]fileMeta, 0, len(fs))
	for _, f := range fs {
		m, _ := classify(f)
		items = append(items, m)
	}
	var next any
	if len(fs) == perPage && pg < maxPages {
		next = strconv.Itoa(pg + 1)
	}
	send(w, map[string]any{"items": items, "next_cursor": next})
}

type diffLine struct {
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	OldLine *int   `json:"oldLine,omitempty"`
	NewLine *int   `json:"newLine,omitempty"`
}

type diffHunk struct {
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Header   string     `json:"header,omitempty"`
	Lines    []diffLine `json:"lines"`
}

type fileDiff struct {
	fileMeta
	Hunks []diffHunk `json:"hunks"`
}

func (h *Handler) diff(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := prTarget(r)
	if e != nil {
		fail(w, e)
		return
	}
	want := r.URL.Query().Get("path")
	files, e := h.allFiles(r.Context(), owner, name, num)
	if e != nil {
		fail(w, e)
		return
	}
	out := []fileDiff{}
	for _, f := range files {
		if want != "" && f.Filename != want {
			continue
		}
		m, use := classify(f)
		fd := fileDiff{fileMeta: m, Hunks: []diffHunk{}}
		if use {
			fd.Hunks = parsePatch(f.Patch)
		}
		out = append(out, fd)
	}
	if want != "" && len(out) == 0 {
		fail(w, &apiErr{status: 404, code: "not_found", msg: "No such file in this diff."})
		return
	}
	send(w, map[string]any{"items": out})
}

func parsePatch(patch string) []diffHunk {
	hunks := []diffHunk{}
	var cur *diffHunk
	var oldLine, newLine int
	for _, raw := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if m := hunkHeaderRe.FindStringSubmatch(raw); m != nil {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			oStart, _ := strconv.Atoi(m[1])
			oLines := 1
			if m[2] != "" {
				oLines, _ = strconv.Atoi(m[2])
			}
			nStart, _ := strconv.Atoi(m[3])
			nLines := 1
			if m[4] != "" {
				nLines, _ = strconv.Atoi(m[4])
			}
			cur = &diffHunk{OldStart: oStart, OldLines: oLines, NewStart: nStart, NewLines: nLines, Header: m[5], Lines: []diffLine{}}
			oldLine, newLine = oStart, nStart
			continue
		}
		if cur == nil || strings.HasPrefix(raw, `\ No newline`) {
			continue
		}
		switch {
		case strings.HasPrefix(raw, "+"):
			nl := newLine
			newLine++
			cur.Lines = append(cur.Lines, diffLine{Kind: "add", Text: raw[1:], NewLine: &nl})
		case strings.HasPrefix(raw, "-"):
			ol := oldLine
			oldLine++
			cur.Lines = append(cur.Lines, diffLine{Kind: "del", Text: raw[1:], OldLine: &ol})
		case strings.HasPrefix(raw, " ") || raw == "":
			txt := raw
			if strings.HasPrefix(raw, " ") {
				txt = raw[1:]
			}
			ol, nl := oldLine, newLine
			oldLine++
			newLine++
			cur.Lines = append(cur.Lines, diffLine{Kind: "context", Text: txt, OldLine: &ol, NewLine: &nl})
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

// ---- checks ----

type check struct {
	Name             string  `json:"name"`
	Status           string  `json:"status"`
	Conclusion       *string `json:"conclusion"`
	StartedMS        int64   `json:"started_ms"`
	CompletedMS      *int64  `json:"completed_ms"`
	URL              string  `json:"url"`
	Summary          string  `json:"summary"`
	AnnotationsCount int     `json:"annotations_count"`
}

type status struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func ms(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 { // do not split a UTF-8 sequence
		n--
	}
	return s[:n]
}

// rank orders failures first, then in-flight, then the rest.
func rank(c check) int {
	switch {
	case c.Conclusion != nil && (*c.Conclusion == "failure" || *c.Conclusion == "timed_out" || *c.Conclusion == "cancelled" || *c.Conclusion == "action_required" || *c.Conclusion == "startup_failure"):
		return 0
	case c.Status != "completed":
		return 1
	}
	return 2
}

func (h *Handler) checks(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := prTarget(r)
	if e != nil {
		fail(w, e)
		return
	}
	repo := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if e := h.get(r.Context(), fmt.Sprintf("%s/pulls/%d", repo, num), &pr); e != nil {
		fail(w, e)
		return
	}
	sha := pr.Head.SHA
	if !forgeapi.ValidHeadSHA(sha) {
		fail(w, &apiErr{status: 502, code: "unavailable", msg: "GitHub returned no head commit."})
		return
	}
	var runs struct {
		CheckRuns []struct {
			Name        string  `json:"name"`
			Status      string  `json:"status"`
			Conclusion  *string `json:"conclusion"`
			StartedAt   string  `json:"started_at"`
			CompletedAt string  `json:"completed_at"`
			HTMLURL     string  `json:"html_url"`
			Output      struct {
				Summary          string `json:"summary"`
				AnnotationsCount int    `json:"annotations_count"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	if e := h.get(r.Context(), fmt.Sprintf("%s/commits/%s/check-runs?per_page=100", repo, sha), &runs); e != nil {
		fail(w, e)
		return
	}
	var combined struct {
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			TargetURL   string `json:"target_url"`
			Description string `json:"description"`
		} `json:"statuses"`
	}
	if e := h.get(r.Context(), fmt.Sprintf("%s/commits/%s/status?per_page=100", repo, sha), &combined); e != nil {
		fail(w, e)
		return
	}
	checks := make([]check, 0, len(runs.CheckRuns))
	for _, c := range runs.CheckRuns {
		ck := check{Name: c.Name, Status: c.Status, Conclusion: c.Conclusion, StartedMS: ms(c.StartedAt), URL: c.HTMLURL,
			Summary: cut(c.Output.Summary, summaryLimit), AnnotationsCount: c.Output.AnnotationsCount}
		if c.CompletedAt != "" {
			v := ms(c.CompletedAt)
			ck.CompletedMS = &v
		}
		checks = append(checks, ck)
	}
	sort.SliceStable(checks, func(i, j int) bool { return rank(checks[i]) < rank(checks[j]) })
	statuses := make([]status, 0, len(combined.Statuses))
	for _, s := range combined.Statuses {
		statuses = append(statuses, status{Name: s.Context, State: s.State, URL: s.TargetURL, Description: cut(s.Description, summaryLimit)})
	}
	sort.SliceStable(statuses, func(i, j int) bool { return statuses[i].State != "success" && statuses[j].State == "success" })
	send(w, map[string]any{"head_sha": sha, "checks": checks, "statuses": statuses})
}

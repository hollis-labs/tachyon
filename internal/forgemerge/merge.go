// Package forgemerge is the merge preflight and the guarded merge for the Pull requests backend (PR5).
// Preflight reads one PR from GitHub and says whether and how it can merge; the merge re-runs it, pins the
// head sha, passes the sha to GitHub (which still enforces the repository's rules) and is idempotent by key.
// GitHub text is untrusted and is only ever returned as JSON strings. Design:
// design_tachyon_pull_requests_backend_20261030, section 4.
package forgemerge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Routes are the patterns Handler serves, each mounted explicitly by the server.
var Routes = []string{
	"GET " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}/merge-preflight",
	"PUT " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}/merge",
}

const (
	maxBody        = 64 << 10
	maxTitle       = 256
	maxKey         = 128
	idempotencyTTL = 24 * time.Hour
	mergeRetries   = 2
)

var willRun = []string{
	"Merge into the base branch with the chosen method",
	"Refresh the pull request list",
	"Settle the triage entry",
	"Mark agent assignments done",
}

// After is what happens once a merge is confirmed: refresh the index, settle triage, close agent links.
type After func(ctx context.Context, prKey, mergeSHA string) Settled

// Settled reports what After did.
type Settled struct {
	Settled   bool
	LinksDone int
}

// Handler talks to GitHub with one token.
type Handler struct {
	Token string
	Base  string // default https://api.github.com
	HTTP  *http.Client
	DB    *localstore.Store
	After After
	Now   func() time.Time
	// Wait pauses between mergeability retries; tests replace it.
	Wait func(time.Duration)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) wait(d time.Duration) {
	if h.Wait != nil {
		h.Wait(d)
		return
	}
	time.Sleep(d)
}

// Mux serves both routes.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc(Routes[0], h.preflight)
	m.HandleFunc(Routes[1], h.merge)
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

func (e *apiErr) body() map[string]any {
	b := map[string]any{"code": e.code, "message": e.msg}
	for k, v := range e.extra {
		b[k] = v
	}
	return map[string]any{"error": b}
}

func fail(w http.ResponseWriter, e *apiErr) { send(w, e.status, e.body()) }

func bad(msg string) *apiErr { return &apiErr{status: 400, code: "invalid_args", msg: msg} }

func blocked(bl []Blocker) *apiErr {
	return &apiErr{status: 409, code: "merge_blocked", msg: "This pull request cannot be merged yet.", extra: map[string]any{"blockers": bl}}
}

func headMoved(sha string) *apiErr {
	return &apiErr{status: 409, code: "head_moved", msg: "The pull request changed. Review it again before merging.", extra: map[string]any{"head_sha": sha}}
}

// ---- GitHub ----

func (h *Handler) do(ctx context.Context, method, path string, in, out any) (int, *apiErr) {
	if h.Token == "" {
		return 0, &apiErr{status: 401, code: "credential_missing", msg: "No GitHub token is configured."}
	}
	base := strings.TrimRight(h.Base, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
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
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return resp.StatusCode, &apiErr{status: 502, code: "unavailable", msg: ghMessage(raw, "GitHub answered with an error.")}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, &apiErr{status: 502, code: "unavailable", msg: "GitHub sent something unreadable."}
		}
	}
	return resp.StatusCode, nil
}

// ghMessage is GitHub's own message for a refusal, cut to a sentence-sized length.
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

type ghPull struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	Head           struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

// ---- preflight ----

// Blocker says why a merge cannot happen.
type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Counts are check results at the head commit.
type Counts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

// Preflight is the merge-preflight answer.
type Preflight struct {
	HeadSHA             string    `json:"head_sha"`
	BaseRef             string    `json:"base_ref"`
	State               string    `json:"state"`
	Blockers            []Blocker `json:"blockers"`
	MethodsAllowed      []string  `json:"methods_allowed"`
	DefaultMethod       string    `json:"default_method"`
	CommitTitle         string    `json:"commit_title"`
	CommitMessage       string    `json:"commit_message"`
	BehindBy            int       `json:"behind_by"`
	RequiredChecks      Counts    `json:"required_checks"`
	DeleteBranchDefault bool      `json:"delete_branch_default"`
	WillRun             []string  `json:"will_run"`
}

func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
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

// pull reads the PR. GitHub computes mergeability lazily, so a null answer is retried before it is reported
// as still pending; nothing is guessed.
func (h *Handler) pull(ctx context.Context, owner, name string, num int) (ghPull, *apiErr) {
	var p ghPull
	path := fmt.Sprintf("%s/pulls/%d", repoPath(owner, name), num)
	for i := 0; ; i++ {
		p = ghPull{}
		if _, e := h.do(ctx, http.MethodGet, path, nil, &p); e != nil {
			return p, e
		}
		if p.Mergeable != nil || p.State != "open" || p.Draft || i >= mergeRetries {
			return p, nil
		}
		h.wait(1500 * time.Millisecond)
	}
}

func (h *Handler) preflightFor(ctx context.Context, owner, name string, num int) (Preflight, ghPull, *apiErr) {
	p, e := h.pull(ctx, owner, name, num)
	if e != nil {
		return Preflight{}, p, e
	}
	repo := repoPath(owner, name)
	pf := Preflight{HeadSHA: p.Head.SHA, BaseRef: p.Base.Ref, Blockers: []Blocker{}, WillRun: willRun,
		CommitTitle: fmt.Sprintf("%s (#%d)", p.Title, p.Number)}

	var settings struct {
		Merge  *bool `json:"allow_merge_commit"`
		Squash *bool `json:"allow_squash_merge"`
		Rebase *bool `json:"allow_rebase_merge"`
	}
	pf.MethodsAllowed = []string{}
	if _, e := h.do(ctx, http.MethodGet, repo, nil, &settings); e != nil {
		return pf, p, e
	}
	for _, m := range []struct {
		name string
		on   *bool
	}{{"merge", settings.Merge}, {"squash", settings.Squash}, {"rebase", settings.Rebase}} {
		if m.on == nil || *m.on {
			pf.MethodsAllowed = append(pf.MethodsAllowed, m.name)
		}
	}
	switch {
	case slices.Contains(pf.MethodsAllowed, "squash"):
		pf.DefaultMethod = "squash"
	case len(pf.MethodsAllowed) > 0:
		pf.DefaultMethod = pf.MethodsAllowed[0]
	}

	add := func(code, msg string) { pf.Blockers = append(pf.Blockers, Blocker{code, msg}) }
	if p.State != "open" || p.Merged {
		add("closed", "This pull request is closed.")
		pf.State = "blocked"
		return pf, p, nil
	}
	if p.Draft {
		add("draft", "This is a draft pull request.")
	}
	pf.RequiredChecks = h.checkCounts(ctx, repo, p.Head.SHA)
	if pf.RequiredChecks.Failed > 0 {
		add("checks_failing", plural(pf.RequiredChecks.Failed, "check is", "checks are")+" failing.")
	}
	if pf.RequiredChecks.Pending > 0 {
		add("checks_pending", plural(pf.RequiredChecks.Pending, "check is", "checks are")+" still running.")
	}
	changes, approvals := h.reviewState(ctx, repo, num)
	for _, who := range changes {
		add("changes_requested", who+" requested changes.")
	}
	switch p.MergeableState {
	case "dirty":
		add("conflicts", "The branch has conflicts with "+p.Base.Ref+".")
	case "behind":
		pf.BehindBy = h.behindBy(ctx, repo, p)
		msg := "The branch is behind " + p.Base.Ref + "."
		if pf.BehindBy > 0 {
			msg = plural(pf.BehindBy, "commit", "commits") + " behind " + p.Base.Ref + "."
			msg = "The branch is " + msg
		}
		add("behind_base", msg)
	}
	if p.Mergeable != nil && !*p.Mergeable && p.MergeableState != "dirty" && len(pf.Blockers) == 0 {
		add("conflicts", "The branch has conflicts with "+p.Base.Ref+".")
	}
	// GitHub says blocked but nothing above explains it: a repository rule. The exact rule is only readable with
	// admin rights, so the reason stays generic unless the protection can be read.
	if p.MergeableState == "blocked" && len(pf.Blockers) == 0 {
		if need := h.requiredApprovals(ctx, repo, p.Base.Ref); need > approvals {
			add("review_required", "An approving review is required.")
		} else {
			add("branch_protection", "GitHub reports the branch is protected; the exact rule is not readable with this token.")
		}
	}
	switch {
	case len(pf.Blockers) > 0:
		pf.State = "blocked"
	case p.Mergeable == nil:
		pf.State = "pending"
	default:
		pf.State = "ready"
	}
	return pf, p, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// checkCounts folds check runs and commit statuses at the head into passed, failed and pending.
func (h *Handler) checkCounts(ctx context.Context, repo, sha string) Counts {
	var c Counts
	var runs struct {
		CheckRuns []struct {
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if _, e := h.do(ctx, http.MethodGet, repo+"/commits/"+sha+"/check-runs?per_page=100", nil, &runs); e == nil {
		for _, r := range runs.CheckRuns {
			switch {
			case r.Status != "completed":
				c.Pending++
			case r.Conclusion != nil && slices.Contains([]string{"success", "neutral", "skipped"}, *r.Conclusion):
				c.Passed++
			default:
				c.Failed++
			}
		}
	}
	var combined struct {
		Statuses []struct {
			State string `json:"state"`
		} `json:"statuses"`
	}
	if _, e := h.do(ctx, http.MethodGet, repo+"/commits/"+sha+"/status?per_page=100", nil, &combined); e == nil {
		for _, s := range combined.Statuses {
			switch s.State {
			case "success":
				c.Passed++
			case "pending":
				c.Pending++
			default:
				c.Failed++
			}
		}
	}
	return c
}

// reviewState returns who currently requests changes and how many approvals stand (each reviewer's latest
// verdict counts).
func (h *Handler) reviewState(ctx context.Context, repo string, num int) (changes []string, approvals int) {
	var reviews []struct {
		User  struct{ Login string } `json:"user"`
		State string                 `json:"state"`
	}
	if _, e := h.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", repo, num), nil, &reviews); e != nil {
		return nil, 0
	}
	latest := map[string]string{}
	order := []string{}
	for _, r := range reviews {
		if r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" && r.State != "DISMISSED" {
			continue
		}
		if _, seen := latest[r.User.Login]; !seen {
			order = append(order, r.User.Login)
		}
		latest[r.User.Login] = r.State
	}
	for _, who := range order {
		switch latest[who] {
		case "CHANGES_REQUESTED":
			changes = append(changes, who)
		case "APPROVED":
			approvals++
		}
	}
	return changes, approvals
}

func (h *Handler) behindBy(ctx context.Context, repo string, p ghPull) int {
	var cmp struct {
		BehindBy int `json:"behind_by"`
	}
	if _, e := h.do(ctx, http.MethodGet, repo+"/compare/"+url.PathEscape(p.Base.Ref)+"..."+p.Head.SHA, nil, &cmp); e != nil {
		return 0
	}
	return cmp.BehindBy
}

// requiredApprovals is the branch protection's approval count, or 0 when it cannot be read.
func (h *Handler) requiredApprovals(ctx context.Context, repo, branch string) int {
	var prot struct {
		Reviews *struct {
			Count int `json:"required_approving_review_count"`
		} `json:"required_pull_request_reviews"`
	}
	if _, e := h.do(ctx, http.MethodGet, repo+"/branches/"+url.PathEscape(branch)+"/protection", nil, &prot); e != nil || prot.Reviews == nil {
		return 0
	}
	return prot.Reviews.Count
}

func (h *Handler) preflight(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := target(r)
	if e != nil {
		fail(w, e)
		return
	}
	pf, _, e := h.preflightFor(r.Context(), owner, name, num)
	if e != nil {
		fail(w, e)
		return
	}
	send(w, 200, map[string]any{"data": pf})
}

// ---- merge ----

type mergeRequest struct {
	Method        string `json:"method"`
	HeadSHA       string `json:"head_sha"`
	CommitTitle   string `json:"commit_title"`
	CommitMessage string `json:"commit_message"`
	DeleteBranch  bool   `json:"delete_branch"`
}

// Result is the merge answer.
type Result struct {
	Merged     bool   `json:"merged"`
	SHA        string `json:"sha"`
	Message    string `json:"message"`
	Method     string `json:"method"`
	AuditClass string `json:"audit_class"`
	Settled    bool   `json:"settled"`
	LinksDone  int    `json:"links_done"`
	Replayed   bool   `json:"replayed,omitempty"`
}

func (h *Handler) merge(w http.ResponseWriter, r *http.Request) {
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
	if len(key) > maxKey {
		fail(w, bad("Idempotency-Key is at most 128 characters."))
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var in mergeRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err != nil || dec.Decode(&in) != nil {
		fail(w, bad("Send a JSON object with the documented fields only."))
		return
	}
	if !slices.Contains(forgeapi.Methods, in.Method) || !forgeapi.ValidHeadSHA(in.HeadSHA) || utf8.RuneCountInString(in.CommitTitle) > maxTitle {
		fail(w, bad("Choose merge, squash or rebase, and send the full head_sha."))
		return
	}
	prKey := forgeapi.PRKey(owner, name, num)
	digest := digestOf(prKey, in)
	if status, body, found, e := h.replay(r.Context(), key, digest); e != nil {
		fail(w, e)
		return
	} else if found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	status, out := h.mergeOnce(r.Context(), owner, name, num, prKey, in)
	b, _ := json.Marshal(out)
	// Only a settled outcome is remembered: a refusal or an outage can be retried under the same key.
	if status == 200 || (status == 409 && out["error"] != nil && codeOf(out) == "head_moved") {
		h.remember(r.Context(), key, digest, status, b)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func codeOf(out map[string]any) string {
	if m, ok := out["error"].(map[string]any); ok {
		c, _ := m["code"].(string)
		return c
	}
	return ""
}

func digestOf(prKey string, in mergeRequest) string {
	b, _ := json.Marshal(struct {
		K string
		R mergeRequest
	}{prKey, in})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mergeOnce does the preflight, the GitHub merge and the follow-up, returning the HTTP status and body.
func (h *Handler) mergeOnce(ctx context.Context, owner, name string, num int, prKey string, in mergeRequest) (int, map[string]any) {
	failed := func(e *apiErr) (int, map[string]any) { return e.status, e.body() }
	pf, p, e := h.preflightFor(ctx, owner, name, num)
	if e != nil {
		return failed(e)
	}
	if p.Merged {
		return h.alreadyMerged(ctx, prKey, p, in.Method)
	}
	if pf.HeadSHA != in.HeadSHA {
		return failed(headMoved(pf.HeadSHA))
	}
	if pf.State == "pending" {
		return failed(blocked([]Blocker{{"unknown_mergeable", "GitHub has not computed mergeability yet."}}))
	}
	if pf.State != "ready" {
		return failed(blocked(pf.Blockers))
	}
	if !slices.Contains(pf.MethodsAllowed, in.Method) {
		return failed(bad("This repository does not allow " + in.Method + " merges."))
	}
	body := map[string]string{"sha": in.HeadSHA, "merge_method": in.Method}
	if in.CommitTitle != "" {
		body["commit_title"] = in.CommitTitle
	}
	if in.CommitMessage != "" {
		body["commit_message"] = in.CommitMessage
	}
	var gh struct {
		Merged  bool   `json:"merged"`
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	code, e := h.do(ctx, http.MethodPut, fmt.Sprintf("%s/pulls/%d/merge", repoPath(owner, name), num), body, &gh)
	switch {
	case e == nil && gh.Merged:
	case code == http.StatusConflict:
		// GitHub saw a different head than the pin.
		if cur, _, ce := h.preflightFor(ctx, owner, name, num); ce == nil {
			return failed(headMoved(cur.HeadSHA))
		}
		return failed(&apiErr{status: 409, code: "conflict", msg: "GitHub declined the merge."})
	case code == http.StatusMethodNotAllowed:
		// Not mergeable now, or already merged by someone else: ask GitHub which.
		if cur, ce := h.pull(ctx, owner, name, num); ce == nil && cur.Merged {
			return h.alreadyMerged(ctx, prKey, cur, in.Method)
		}
		msg := "GitHub declined the merge."
		if e != nil {
			msg = e.msg
		}
		return failed(&apiErr{status: 409, code: "conflict", msg: msg})
	case code == http.StatusForbidden:
		return failed(&apiErr{status: 403, code: "operator_access_required", msg: "The GitHub token may not merge this pull request."})
	default:
		// The outcome is unknown (outage, timeout, an unexpected answer): read the PR before saying anything.
		if cur, ce := h.pull(ctx, owner, name, num); ce == nil && cur.Merged {
			return h.alreadyMerged(ctx, prKey, cur, in.Method)
		}
		if e == nil {
			e = &apiErr{status: 502, code: "unavailable", msg: "GitHub did not confirm the merge."}
		}
		return failed(e)
	}
	if in.DeleteBranch && p.Head.Repo != nil && p.Head.Repo.FullName == p.Base.Repo.FullName {
		// Best effort and after the merge: a branch that cannot be deleted does not undo a merge.
		_, _ = h.do(ctx, http.MethodDelete, repoPath(owner, name)+"/git/refs/heads/"+url.PathEscape(p.Head.Ref), nil, nil)
	}
	return 200, map[string]any{"data": h.finish(ctx, prKey, gh.SHA, gh.Message, in.Method, false)}
}

func (h *Handler) alreadyMerged(ctx context.Context, prKey string, p ghPull, method string) (int, map[string]any) {
	return 200, map[string]any{"data": h.finish(ctx, prKey, p.MergeCommitSHA, "Pull Request already merged", method, true)}
}

func (h *Handler) finish(ctx context.Context, prKey, sha, msg, method string, replayed bool) Result {
	res := Result{Merged: true, SHA: sha, Message: msg, Method: method, AuditClass: "lifecycle", Replayed: replayed}
	if h.After != nil {
		s := h.After(ctx, prKey, sha)
		res.Settled, res.LinksDone = s.Settled, s.LinksDone
	}
	return res
}

// ---- idempotency ----

func (h *Handler) replay(ctx context.Context, key, digest string) (int, []byte, bool, *apiErr) {
	if h.DB == nil {
		return 0, nil, false, nil
	}
	var d, body string
	var status int
	var created int64
	err := h.DB.Reader.QueryRowContext(ctx, `SELECT request_digest, response_status, response_json, created_ms FROM forge_idempotency WHERE key = ? AND op = 'merge'`, key).
		Scan(&d, &status, &body, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, nil
	}
	if h.now().Sub(time.UnixMilli(created)) > idempotencyTTL {
		return 0, nil, false, nil
	}
	if d != digest {
		return 0, nil, false, &apiErr{status: 409, code: "idempotency_conflict", msg: "That Idempotency-Key was used for a different merge."}
	}
	return status, []byte(body), true, nil
}

func (h *Handler) remember(ctx context.Context, key, digest string, status int, body []byte) {
	if h.DB == nil {
		return
	}
	now := h.now().UnixMilli()
	_ = h.DB.Writer.Submit(ctx, "forge_idempotency_save", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM forge_idempotency WHERE created_ms < ?`, now-idempotencyTTL.Milliseconds()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO forge_idempotency(key, op, request_digest, response_status, response_json, created_ms) VALUES (?, 'merge', ?, ?, ?, ?)
 ON CONFLICT(key, op) DO UPDATE SET request_digest = excluded.request_digest, response_status = excluded.response_status, response_json = excluded.response_json, created_ms = excluded.created_ms`,
			key, digest, status, string(body), now)
		return err
	})
}

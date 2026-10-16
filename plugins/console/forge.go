package main

import (
	"bytes"
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

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

const (
	githubAPIBase   = "https://api.github.com"
	forgePathPrefix = "/api/plugins/console/v1/forge"
	pendingCacheTTL = 30 * time.Second
)

var (
	hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?:\s?(.*))?$`)
	cwRefRe      = regexp.MustCompile(`\bCW-\d{8}-\d{4}\b`)
)

// ForgeHandler serves /api/plugins/console/v1/forge/* directly against GitHub
// with zero dependency on Torque.
type ForgeHandler struct {
	token   string
	org     string
	baseURL string
	client  *http.Client

	mu           sync.Mutex
	pendingCache []pendingPrSummary
	pendingAt    time.Time
}

func NewForgeHandler(token, org string) *ForgeHandler {
	if org == "" {
		org = "hollis-labs"
	}
	return &ForgeHandler{
		token:   strings.TrimSpace(token),
		org:     org,
		baseURL: githubAPIBase,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *ForgeHandler) Match(path string) bool {
	return path == forgePathPrefix || strings.HasPrefix(path, forgePathPrefix+"/")
}

func (f *ForgeHandler) Handle(ctx context.Context, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	if f.token == "" {
		return forgeErr(http.StatusUnauthorized, "credential_missing", "GITHUB_TOKEN is not configured")
	}
	sub := strings.TrimPrefix(req.Path, forgePathPrefix)
	if sub == "" {
		sub = "/"
	}
	q := parseQuery(req)

	switch {
	case req.Method == http.MethodGet && sub == "/pulls/pending":
		return f.handlePendingPrs(ctx)
	case req.Method == http.MethodGet && sub == "/pulls":
		return f.handleListOrgPrs(ctx, q)
	}

	parts := strings.Split(strings.Trim(sub, "/"), "/")
	// /repos/{host}/{owner}/{name}[/pulls[/{number}[/{sub}]]]
	if len(parts) >= 4 && parts[0] == "repos" {
		host, owner, name := parts[1], parts[2], parts[3]
		if host != "github" {
			return forgeErr(http.StatusNotImplemented, "unavailable", "unsupported forge host: "+host)
		}
		if len(parts) == 4 && req.Method == http.MethodGet {
			return f.handleGetRepo(ctx, owner, name)
		}
		if len(parts) == 5 && parts[4] == "pulls" && req.Method == http.MethodGet {
			return f.handleListRepoPrs(ctx, owner, name, q)
		}
		if len(parts) >= 6 && parts[4] == "pulls" {
			num, err := strconv.Atoi(parts[5])
			if err != nil || num <= 0 {
				return forgeErr(http.StatusBadRequest, "invalid_args", "invalid pull request number")
			}
			if len(parts) == 6 && req.Method == http.MethodGet {
				return f.handleGetPr(ctx, owner, name, num)
			}
			if len(parts) == 7 {
				switch {
				case parts[6] == "diff" && req.Method == http.MethodGet:
					return f.handleGetDiff(ctx, owner, name, num)
				case parts[6] == "commits" && req.Method == http.MethodGet:
					return f.handleListCommits(ctx, owner, name, num)
				case parts[6] == "comments" && req.Method == http.MethodGet:
					return f.handleListComments(ctx, owner, name, num)
				case parts[6] == "comments" && req.Method == http.MethodPost:
					return f.handleAddComment(ctx, owner, name, num, req)
				case parts[6] == "reviews" && req.Method == http.MethodPost:
					return f.handleReviewPr(ctx, owner, name, num, req)
				case parts[6] == "assignees" && req.Method == http.MethodPut:
					return f.handleAssignPr(ctx, owner, name, num, req)
				}
			}
		}
	}

	return forgeErr(http.StatusNotFound, "not_found", "unknown forge route: "+sub)
}

// ---- Wire DTOs (matching plugins/console/ui/src/forge/types.ts) ------------

type repoRef struct {
	Host  string `json:"host"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type prRef struct {
	Repo   repoRef `json:"repo"`
	Number int     `json:"number"`
}

type forgeRepo struct {
	Host          string `json:"host"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	DefaultBranch string `json:"defaultBranch"`
	Private       bool   `json:"private"`
	Stars         int    `json:"stars"`
	OpenIssues    int    `json:"openIssues"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	URL           string `json:"url,omitempty"`
}

type forgeUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	IsAgent   bool   `json:"isAgent,omitempty"`
}

type checkRun struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	URL         string `json:"url,omitempty"`
}

type reviewDTO struct {
	ID          string    `json:"id"`
	Author      forgeUser `json:"author"`
	State       string    `json:"state"`
	Body        string    `json:"body,omitempty"`
	SubmittedAt string    `json:"submittedAt,omitempty"`
	CommitSha   string    `json:"commitSha,omitempty"`
}

type pullRequestDTO struct {
	Ref          prRef       `json:"ref"`
	Title        string      `json:"title"`
	Body         string      `json:"body,omitempty"`
	State        string      `json:"state"`
	Draft        bool        `json:"draft,omitempty"`
	Author       forgeUser   `json:"author"`
	Assignees    []forgeUser `json:"assignees"`
	Reviewers    []forgeUser `json:"reviewers,omitempty"`
	Labels       []string    `json:"labels,omitempty"`
	BaseBranch   string      `json:"baseBranch"`
	HeadBranch   string      `json:"headBranch"`
	HeadSha      string      `json:"headSha,omitempty"`
	Mergeable    string      `json:"mergeable"`
	Checks       []checkRun  `json:"checks"`
	Reviews      []reviewDTO `json:"reviews"`
	Additions    int         `json:"additions"`
	Deletions    int         `json:"deletions"`
	ChangedFiles int         `json:"changedFiles"`
	CommentCount int         `json:"commentCount"`
	CreatedAt    string      `json:"createdAt"`
	UpdatedAt    string      `json:"updatedAt"`
	MergedAt     string      `json:"mergedAt,omitempty"`
	URL          string      `json:"url,omitempty"`
	TaskID       string      `json:"taskId,omitempty"`
}

type checksSummaryDTO struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

type reviewSummaryDTO struct {
	Approved         int `json:"approved"`
	ChangesRequested int `json:"changesRequested"`
}

type pendingPrSummary struct {
	Ref           prRef             `json:"ref"`
	Title         string            `json:"title"`
	Author        forgeUser         `json:"author"`
	Assignees     []forgeUser       `json:"assignees"`
	State         string            `json:"state"`
	Reasons       []string          `json:"reasons"`
	ChecksSummary *checksSummaryDTO `json:"checksSummary,omitempty"`
	ReviewSummary *reviewSummaryDTO `json:"reviewSummary,omitempty"`
	Mergeable     string            `json:"mergeable"`
	UpdatedAt     string            `json:"updatedAt"`
	Additions     int               `json:"additions,omitempty"`
	Deletions     int               `json:"deletions,omitempty"`
	URL           string            `json:"url,omitempty"`
}

type commitDTO struct {
	Sha        string    `json:"sha"`
	Message    string    `json:"message"`
	Author     forgeUser `json:"author"`
	AuthoredAt string    `json:"authoredAt"`
	Parents    []string  `json:"parents,omitempty"`
	URL        string    `json:"url,omitempty"`
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
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"`
	Status    string     `json:"status"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Binary    bool       `json:"binary,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Hunks     []diffHunk `json:"hunks"`
}

type forgeComment struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Author    forgeUser `json:"author"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt,omitempty"`
	URL       string    `json:"url,omitempty"`
	Path      string    `json:"path,omitempty"`
	Line      *int      `json:"line,omitempty"`
	Side      string    `json:"side,omitempty"`
	CommitSha string    `json:"commitSha,omitempty"`
	InReplyTo string    `json:"inReplyTo,omitempty"`
	ReviewID  string    `json:"reviewId,omitempty"`
}

// ---- GitHub API shapes -----------------------------------------------------

type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Type      string `json:"type"`
}

func toForgeUser(u ghUser) forgeUser {
	return forgeUser{
		Login:     u.Login,
		AvatarURL: u.AvatarURL,
		IsAgent:   strings.EqualFold(u.Type, "Bot") || strings.HasSuffix(u.Login, "[bot]"),
	}
}

type ghRepo struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	DefaultBranch   string `json:"default_branch"`
	Private         bool   `json:"private"`
	StargazersCount int    `json:"stargazers_count"`
	OpenIssuesCount int    `json:"open_issues_count"`
	UpdatedAt       string `json:"updated_at"`
	HTMLURL         string `json:"html_url"`
	Owner           ghUser `json:"owner"`
}

type ghPull struct {
	Number             int      `json:"number"`
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	State              string   `json:"state"`
	Draft              bool     `json:"draft"`
	User               ghUser   `json:"user"`
	Assignees          []ghUser `json:"assignees"`
	RequestedReviewers []ghUser `json:"requested_reviewers"`
	Labels             []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Mergeable       *bool   `json:"mergeable"`
	MergeableState  string  `json:"mergeable_state"`
	Additions       int     `json:"additions"`
	Deletions       int     `json:"deletions"`
	ChangedFiles    int     `json:"changed_files"`
	Comments        int     `json:"comments"`
	ReviewComments  int     `json:"review_comments"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	MergedAt        *string `json:"merged_at"`
	HTMLURL         string  `json:"html_url"`
}

type ghCheckRunsResp struct {
	CheckRuns []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		Conclusion  string `json:"conclusion"`
		StartedAt   string `json:"started_at"`
		CompletedAt string `json:"completed_at"`
		HTMLURL     string `json:"html_url"`
	} `json:"check_runs"`
}

type ghReview struct {
	ID          int64  `json:"id"`
	User        ghUser `json:"user"`
	State       string `json:"state"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
	CommitID    string `json:"commit_id"`
}

type ghFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

type ghCommit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Author  *ghUser `json:"author"`
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
}

type ghIssueComment struct {
	ID        int64  `json:"id"`
	User      ghUser `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	HTMLURL   string `json:"html_url"`
}

type ghReviewComment struct {
	ID                  int64  `json:"id"`
	User                ghUser `json:"user"`
	Body                string `json:"body"`
	Path                string `json:"path"`
	Line                *int   `json:"line"`
	OriginalLine        *int   `json:"original_line"`
	Side                string `json:"side"`
	CommitID            string `json:"commit_id"`
	InReplyToID         *int64 `json:"in_reply_to_id"`
	PullRequestReviewID *int64 `json:"pull_request_review_id"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	HTMLURL             string `json:"html_url"`
}

// ---- Route Handlers --------------------------------------------------------

func (f *ForgeHandler) handleGetRepo(ctx context.Context, owner, name string) subprocess.HTTPResponse {
	var r ghRepo
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(name)), nil, &r); status != 0 {
		return forgeErr(status, code, msg)
	}
	return forgeOK(http.StatusOK, forgeRepo{
		Host:          "github",
		Owner:         owner,
		Name:          name,
		Description:   r.Description,
		DefaultBranch: r.DefaultBranch,
		Private:       r.Private,
		Stars:         r.StargazersCount,
		OpenIssues:    r.OpenIssuesCount,
		UpdatedAt:     r.UpdatedAt,
		URL:           r.HTMLURL,
	})
}

func (f *ForgeHandler) handleListRepoPrs(ctx context.Context, owner, name string, q url.Values) subprocess.HTTPResponse {
	state := q.Get("state")
	if state == "" {
		state = "open"
	}
	ghState := state
	if state == "merged" {
		ghState = "closed"
	}
	limit := clampLimit(q.Get("limit"), 30, 100)
	var pulls []ghPull
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&sort=updated&direction=desc&per_page=%d",
		url.PathEscape(owner), url.PathEscape(name), url.QueryEscape(ghState), limit)
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, path, nil, &pulls); status != 0 {
		return forgeErr(status, code, msg)
	}
	authorFilter := strings.ToLower(strings.TrimSpace(q.Get("author")))
	assigneeFilter := strings.ToLower(strings.TrimSpace(q.Get("assignee")))
	textFilter := strings.ToLower(strings.TrimSpace(q.Get("q")))

	items := make([]pullRequestDTO, 0, len(pulls))
	for _, p := range pulls {
		if state == "merged" && p.MergedAt == nil {
			continue
		}
		if state == "closed" && p.MergedAt != nil {
			continue
		}
		if authorFilter != "" && strings.ToLower(p.User.Login) != authorFilter {
			continue
		}
		if assigneeFilter != "" && !hasAssignee(p.Assignees, assigneeFilter) {
			continue
		}
		if textFilter != "" && !strings.Contains(strings.ToLower(p.Title+" "+p.Body), textFilter) {
			continue
		}
		items = append(items, toPullRequestDTO(owner, name, p, nil, nil))
	}
	return forgeOK(http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (f *ForgeHandler) handleListOrgPrs(ctx context.Context, q url.Values) subprocess.HTTPResponse {
	state := q.Get("state")
	if state == "" {
		state = "open"
	}
	terms := []string{"is:pr", "org:" + f.org}
	switch state {
	case "open":
		terms = append(terms, "is:open")
	case "closed":
		terms = append(terms, "is:closed", "is:unmerged")
	case "merged":
		terms = append(terms, "is:merged")
	}
	if a := strings.TrimSpace(q.Get("author")); a != "" {
		terms = append(terms, "author:"+a)
	}
	if a := strings.TrimSpace(q.Get("assignee")); a != "" {
		terms = append(terms, "assignee:"+a)
	}
	if text := strings.TrimSpace(q.Get("q")); text != "" {
		terms = append(terms, text)
	}
	limit := clampLimit(q.Get("limit"), 30, 100)
	var search struct {
		Items []struct {
			Number        int      `json:"number"`
			Title         string   `json:"title"`
			Body          string   `json:"body"`
			State         string   `json:"state"`
			Draft         bool     `json:"draft"`
			User          ghUser   `json:"user"`
			Assignees     []ghUser `json:"assignees"`
			CreatedAt     string   `json:"created_at"`
			UpdatedAt     string   `json:"updated_at"`
			HTMLURL       string   `json:"html_url"`
			RepositoryURL string   `json:"repository_url"`
		} `json:"items"`
	}
	path := fmt.Sprintf("/search/issues?q=%s&sort=updated&order=desc&per_page=%d", url.QueryEscape(strings.Join(terms, " ")), limit)
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, path, nil, &search); status != 0 {
		return forgeErr(status, code, msg)
	}
	items := make([]pullRequestDTO, 0, len(search.Items))
	for _, it := range search.Items {
		owner, name := parseRepoURL(it.RepositoryURL)
		if owner == "" || name == "" {
			continue
		}
		p := ghPull{
			Number:    it.Number,
			Title:     it.Title,
			Body:      it.Body,
			State:     it.State,
			Draft:     it.Draft,
			User:      it.User,
			Assignees: it.Assignees,
			CreatedAt: it.CreatedAt,
			UpdatedAt: it.UpdatedAt,
			HTMLURL:   it.HTMLURL,
		}
		items = append(items, toPullRequestDTO(owner, name, p, nil, nil))
	}
	return forgeOK(http.StatusOK, map[string]any{"items": items, "nextCursor": nil})
}

func (f *ForgeHandler) handleGetPr(ctx context.Context, owner, name string, number int) subprocess.HTTPResponse {
	pr, status, code, msg := f.fetchFullPr(ctx, owner, name, number)
	if status != 0 {
		return forgeErr(status, code, msg)
	}
	return forgeOK(http.StatusOK, pr)
}

func (f *ForgeHandler) fetchFullPr(ctx context.Context, owner, name string, number int) (pullRequestDTO, int, string, string) {
	var p ghPull
	basePath := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), number)
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, basePath, nil, &p); status != 0 {
		return pullRequestDTO{}, status, code, msg
	}
	var (
		reviews []reviewDTO
		checks  []checkRun
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var ghRevs []ghReview
		if st, _, _ := f.ghJSON(ctx, http.MethodGet, basePath+"/reviews?per_page=100", nil, &ghRevs); st == 0 {
			reviews = mapReviews(ghRevs)
		}
	}()
	go func() {
		defer wg.Done()
		if p.Head.SHA == "" {
			return
		}
		var cr ghCheckRunsResp
		checkPath := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100",
			url.PathEscape(owner), url.PathEscape(name), url.PathEscape(p.Head.SHA))
		if st, _, _ := f.ghJSON(ctx, http.MethodGet, checkPath, nil, &cr); st == 0 {
			checks = mapCheckRuns(cr)
		}
	}()
	wg.Wait()
	return toPullRequestDTO(owner, name, p, checks, reviews), 0, "", ""
}

func (f *ForgeHandler) handlePendingPrs(ctx context.Context) subprocess.HTTPResponse {
	f.mu.Lock()
	if f.pendingCache != nil && time.Since(f.pendingAt) < pendingCacheTTL {
		cached := f.pendingCache
		f.mu.Unlock()
		return forgeOK(http.StatusOK, map[string]any{"items": cached})
	}
	f.mu.Unlock()

	var search struct {
		Items []struct {
			Number        int    `json:"number"`
			RepositoryURL string `json:"repository_url"`
		} `json:"items"`
	}
	q := fmt.Sprintf("is:pr is:open org:%s", f.org)
	path := fmt.Sprintf("/search/issues?q=%s&sort=updated&order=desc&per_page=25", url.QueryEscape(q))
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, path, nil, &search); status != 0 {
		return forgeErr(status, code, msg)
	}

	type target struct {
		idx   int
		owner string
		name  string
		num   int
	}
	targets := make([]target, 0, len(search.Items))
	for i, it := range search.Items {
		owner, name := parseRepoURL(it.RepositoryURL)
		if owner != "" && name != "" {
			targets = append(targets, target{idx: i, owner: owner, name: name, num: it.Number})
		}
	}

	results := make([]*pendingPrSummary, len(targets))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	now := time.Now()
	for i, t := range targets {
		wg.Add(1)
		go func(slot int, tgt target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pr, st, _, _ := f.fetchFullPr(ctx, tgt.owner, tgt.name, tgt.num)
			if st != 0 {
				return
			}
			s := summarizePendingPr(pr, now)
			if len(s.Reasons) > 0 {
				results[slot] = &s
			}
		}(i, t)
	}
	wg.Wait()

	items := make([]pendingPrSummary, 0, len(results))
	for _, r := range results {
		if r != nil {
			items = append(items, *r)
		}
	}

	f.mu.Lock()
	f.pendingCache = items
	f.pendingAt = time.Now()
	f.mu.Unlock()

	return forgeOK(http.StatusOK, map[string]any{"items": items})
}

func (f *ForgeHandler) invalidatePendingCache() {
	f.mu.Lock()
	f.pendingAt = time.Time{}
	f.mu.Unlock()
}

func (f *ForgeHandler) handleGetDiff(ctx context.Context, owner, name string, number int) subprocess.HTTPResponse {
	var files []ghFile
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=100", url.PathEscape(owner), url.PathEscape(name), number)
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, path, nil, &files); status != 0 {
		return forgeErr(status, code, msg)
	}
	out := make([]fileDiff, 0, len(files))
	for _, gf := range files {
		fd := fileDiff{
			Path:      gf.Filename,
			OldPath:   gf.PreviousFilename,
			Status:    gf.Status,
			Additions: gf.Additions,
			Deletions: gf.Deletions,
			Hunks:     []diffHunk{},
		}
		if gf.Patch == "" {
			if gf.Additions > 0 || gf.Deletions > 0 {
				fd.Truncated = true
			} else {
				fd.Binary = true
			}
		} else if len(gf.Patch) > 200*1024 {
			fd.Truncated = true
		} else {
			fd.Hunks = parseUnifiedPatch(gf.Patch)
		}
		out = append(out, fd)
	}
	return forgeOK(http.StatusOK, map[string]any{"items": out})
}

func (f *ForgeHandler) handleListCommits(ctx context.Context, owner, name string, number int) subprocess.HTTPResponse {
	var commits []ghCommit
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/commits?per_page=100", url.PathEscape(owner), url.PathEscape(name), number)
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, path, nil, &commits); status != 0 {
		return forgeErr(status, code, msg)
	}
	out := make([]commitDTO, 0, len(commits))
	for _, c := range commits {
		author := forgeUser{Login: c.Commit.Author.Name}
		if c.Author != nil && c.Author.Login != "" {
			author = toForgeUser(*c.Author)
		}
		parents := make([]string, 0, len(c.Parents))
		for _, p := range c.Parents {
			parents = append(parents, p.SHA)
		}
		out = append(out, commitDTO{
			Sha:        c.SHA,
			Message:    c.Commit.Message,
			Author:     author,
			AuthoredAt: c.Commit.Author.Date,
			Parents:    parents,
			URL:        c.HTMLURL,
		})
	}
	return forgeOK(http.StatusOK, map[string]any{"items": out})
}

func (f *ForgeHandler) handleListComments(ctx context.Context, owner, name string, number int) subprocess.HTTPResponse {
	var (
		issueComments  []ghIssueComment
		reviewComments []ghReviewComment
		wg             sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		p := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", url.PathEscape(owner), url.PathEscape(name), number)
		_, _, _ = f.ghJSON(ctx, http.MethodGet, p, nil, &issueComments)
	}()
	go func() {
		defer wg.Done()
		p := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?per_page=100", url.PathEscape(owner), url.PathEscape(name), number)
		_, _, _ = f.ghJSON(ctx, http.MethodGet, p, nil, &reviewComments)
	}()
	wg.Wait()

	out := make([]forgeComment, 0, len(issueComments)+len(reviewComments))
	for _, ic := range issueComments {
		out = append(out, forgeComment{
			Kind:      "issue",
			ID:        strconv.FormatInt(ic.ID, 10),
			Author:    toForgeUser(ic.User),
			Body:      ic.Body,
			CreatedAt: ic.CreatedAt,
			UpdatedAt: ic.UpdatedAt,
			URL:       ic.HTMLURL,
		})
	}
	for _, rc := range reviewComments {
		out = append(out, mapReviewComment(rc))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return forgeOK(http.StatusOK, map[string]any{"items": out})
}

func (f *ForgeHandler) handleAddComment(ctx context.Context, owner, name string, number int, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	var in struct {
		Body      string `json:"body"`
		Path      string `json:"path"`
		Line      *int   `json:"line"`
		Side      string `json:"side"`
		InReplyTo string `json:"inReplyTo"`
	}
	if err := json.Unmarshal(req.Body, &in); err != nil || strings.TrimSpace(in.Body) == "" {
		return forgeErr(http.StatusBadRequest, "invalid_args", "comment body is required")
	}
	if in.Path == "" {
		var created ghIssueComment
		p := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", url.PathEscape(owner), url.PathEscape(name), number)
		if status, code, msg := f.ghJSON(ctx, http.MethodPost, p, map[string]any{"body": in.Body}, &created); status != 0 {
			return forgeErr(status, code, msg)
		}
		return forgeOK(http.StatusCreated, forgeComment{
			Kind:      "issue",
			ID:        strconv.FormatInt(created.ID, 10),
			Author:    toForgeUser(created.User),
			Body:      created.Body,
			CreatedAt: created.CreatedAt,
			UpdatedAt: created.UpdatedAt,
			URL:       created.HTMLURL,
		})
	}

	var p ghPull
	prPath := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), number)
	if status, code, msg := f.ghJSON(ctx, http.MethodGet, prPath, nil, &p); status != 0 {
		return forgeErr(status, code, msg)
	}
	payload := map[string]any{
		"body":      in.Body,
		"commit_id": p.Head.SHA,
		"path":      in.Path,
	}
	if in.Line != nil {
		payload["line"] = *in.Line
	}
	if strings.EqualFold(in.Side, "left") {
		payload["side"] = "LEFT"
	} else {
		payload["side"] = "RIGHT"
	}
	if in.InReplyTo != "" {
		if id, err := strconv.ParseInt(in.InReplyTo, 10, 64); err == nil {
			payload["in_reply_to"] = id
		}
	}
	var created ghReviewComment
	if status, code, msg := f.ghJSON(ctx, http.MethodPost, prPath+"/comments", payload, &created); status != 0 {
		return forgeErr(status, code, msg)
	}
	return forgeOK(http.StatusCreated, mapReviewComment(created))
}

func (f *ForgeHandler) handleReviewPr(ctx context.Context, owner, name string, number int, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	var in struct {
		Verdict  string `json:"verdict"`
		Body     string `json:"body"`
		Comments []struct {
			Body string `json:"body"`
			Path string `json:"path"`
			Line *int   `json:"line"`
			Side string `json:"side"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(req.Body, &in); err != nil {
		return forgeErr(http.StatusBadRequest, "invalid_args", "invalid review payload")
	}
	event := "COMMENT"
	switch in.Verdict {
	case "approve":
		event = "APPROVE"
	case "request_changes":
		event = "REQUEST_CHANGES"
	case "comment":
		event = "COMMENT"
	default:
		return forgeErr(http.StatusBadRequest, "invalid_args", "invalid review verdict: "+in.Verdict)
	}
	ghComments := make([]map[string]any, 0, len(in.Comments))
	for _, c := range in.Comments {
		if c.Path == "" || c.Line == nil {
			continue
		}
		side := "RIGHT"
		if strings.EqualFold(c.Side, "left") {
			side = "LEFT"
		}
		ghComments = append(ghComments, map[string]any{
			"path": c.Path,
			"line": *c.Line,
			"side": side,
			"body": c.Body,
		})
	}
	payload := map[string]any{"event": event}
	if in.Body != "" {
		payload["body"] = in.Body
	}
	if len(ghComments) > 0 {
		payload["comments"] = ghComments
	}
	var created ghReview
	p := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", url.PathEscape(owner), url.PathEscape(name), number)
	if status, code, msg := f.ghJSON(ctx, http.MethodPost, p, payload, &created); status != 0 {
		return forgeErr(status, code, msg)
	}
	f.invalidatePendingCache()
	mapped := mapReviews([]ghReview{created})
	if len(mapped) == 0 {
		return forgeOK(http.StatusCreated, reviewDTO{ID: strconv.FormatInt(created.ID, 10), Author: toForgeUser(created.User), State: strings.ToLower(created.State), Body: created.Body})
	}
	return forgeOK(http.StatusCreated, mapped[0])
}

func (f *ForgeHandler) handleAssignPr(ctx context.Context, owner, name string, number int, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	var in struct {
		Assignees []string `json:"assignees"`
	}
	if err := json.Unmarshal(req.Body, &in); err != nil {
		return forgeErr(http.StatusBadRequest, "invalid_args", "invalid assignees payload")
	}
	issuePath := fmt.Sprintf("/repos/%s/%s/issues/%d/assignees", url.PathEscape(owner), url.PathEscape(name), number)
	if len(in.Assignees) > 0 {
		var ignored map[string]any
		if status, code, msg := f.ghJSON(ctx, http.MethodPost, issuePath, map[string]any{"assignees": in.Assignees}, &ignored); status != 0 {
			return forgeErr(status, code, msg)
		}
	}
	f.invalidatePendingCache()
	return f.handleGetPr(ctx, owner, name, number)
}

// ---- Mapping & Helpers -----------------------------------------------------

func toPullRequestDTO(owner, name string, p ghPull, checks []checkRun, reviews []reviewDTO) pullRequestDTO {
	if checks == nil {
		checks = []checkRun{}
	}
	if reviews == nil {
		reviews = []reviewDTO{}
	}
	state := p.State
	if p.MergedAt != nil && *p.MergedAt != "" {
		state = "merged"
	} else if p.Draft {
		state = "draft"
	}
	assignees := make([]forgeUser, 0, len(p.Assignees))
	for _, a := range p.Assignees {
		assignees = append(assignees, toForgeUser(a))
	}
	reviewers := make([]forgeUser, 0, len(p.RequestedReviewers))
	for _, r := range p.RequestedReviewers {
		reviewers = append(reviewers, toForgeUser(r))
	}
	labels := make([]string, 0, len(p.Labels))
	for _, l := range p.Labels {
		labels = append(labels, l.Name)
	}
	mergeable := "unknown"
	switch p.MergeableState {
	case "clean", "has_hooks", "unstable":
		mergeable = "mergeable"
	case "dirty":
		mergeable = "conflicting"
	case "blocked", "behind":
		mergeable = "blocked"
	default:
		if p.Mergeable != nil {
			if *p.Mergeable {
				mergeable = "mergeable"
			} else {
				mergeable = "conflicting"
			}
		}
	}
	var mergedAt string
	if p.MergedAt != nil {
		mergedAt = *p.MergedAt
	}
	taskID := cwRefRe.FindString(p.Title + " " + p.Head.Ref + " " + p.Body)
	return pullRequestDTO{
		Ref: prRef{
			Repo:   repoRef{Host: "github", Owner: owner, Name: name},
			Number: p.Number,
		},
		Title:        p.Title,
		Body:         p.Body,
		State:        state,
		Draft:        p.Draft,
		Author:       toForgeUser(p.User),
		Assignees:    assignees,
		Reviewers:    reviewers,
		Labels:       labels,
		BaseBranch:   p.Base.Ref,
		HeadBranch:   p.Head.Ref,
		HeadSha:      p.Head.SHA,
		Mergeable:    mergeable,
		Checks:       checks,
		Reviews:      reviews,
		Additions:    p.Additions,
		Deletions:    p.Deletions,
		ChangedFiles: p.ChangedFiles,
		CommentCount: p.Comments + p.ReviewComments,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
		MergedAt:     mergedAt,
		URL:          p.HTMLURL,
		TaskID:       taskID,
	}
}

func summarizePendingPr(pr pullRequestDTO, now time.Time) pendingPrSummary {
	var passed, failed, pending int
	for _, c := range pr.Checks {
		if c.Status != "completed" {
			pending++
			continue
		}
		switch c.Conclusion {
		case "failure", "timed_out", "action_required", "cancelled":
			failed++
		default:
			passed++
		}
	}
	latestByAuthor := map[string]string{}
	for _, r := range pr.Reviews {
		switch r.State {
		case "approved", "changes_requested":
			latestByAuthor[r.Author.Login] = r.State
		}
	}
	var approved, changesRequested int
	for _, st := range latestByAuthor {
		switch st {
		case "approved":
			approved++
		case "changes_requested":
			changesRequested++
		}
	}
	var reasons []string
	if failed > 0 {
		reasons = append(reasons, "checks_failed")
	}
	if changesRequested > 0 {
		reasons = append(reasons, "changes_requested")
	}
	if pr.Mergeable == "conflicting" {
		reasons = append(reasons, "conflicting")
	}
	if len(pr.Reviewers) > 0 || (approved == 0 && changesRequested == 0 && !pr.Draft) {
		reasons = append(reasons, "review_requested")
	}
	if len(pr.Assignees) == 0 {
		reasons = append(reasons, "unassigned")
	}
	if t, err := time.Parse(time.RFC3339, pr.UpdatedAt); err == nil && now.Sub(t) > 7*24*time.Hour {
		reasons = append(reasons, "stale")
	}
	var cs *checksSummaryDTO
	if len(pr.Checks) > 0 {
		cs = &checksSummaryDTO{Passed: passed, Failed: failed, Pending: pending}
	}
	var rs *reviewSummaryDTO
	if approved > 0 || changesRequested > 0 {
		rs = &reviewSummaryDTO{Approved: approved, ChangesRequested: changesRequested}
	}
	return pendingPrSummary{
		Ref:           pr.Ref,
		Title:         pr.Title,
		Author:        pr.Author,
		Assignees:     pr.Assignees,
		State:         pr.State,
		Reasons:       reasons,
		ChecksSummary: cs,
		ReviewSummary: rs,
		Mergeable:     pr.Mergeable,
		UpdatedAt:     pr.UpdatedAt,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		URL:           pr.URL,
	}
}

func mapReviews(in []ghReview) []reviewDTO {
	out := make([]reviewDTO, 0, len(in))
	for _, r := range in {
		st := strings.ToLower(r.State)
		out = append(out, reviewDTO{
			ID:          strconv.FormatInt(r.ID, 10),
			Author:      toForgeUser(r.User),
			State:       st,
			Body:        r.Body,
			SubmittedAt: r.SubmittedAt,
			CommitSha:   r.CommitID,
		})
	}
	return out
}

func mapCheckRuns(in ghCheckRunsResp) []checkRun {
	out := make([]checkRun, 0, len(in.CheckRuns))
	for _, c := range in.CheckRuns {
		out = append(out, checkRun{
			ID:          strconv.FormatInt(c.ID, 10),
			Name:        c.Name,
			Status:      c.Status,
			Conclusion:  c.Conclusion,
			StartedAt:   c.StartedAt,
			CompletedAt: c.CompletedAt,
			URL:         c.HTMLURL,
		})
	}
	return out
}

func mapReviewComment(rc ghReviewComment) forgeComment {
	line := rc.Line
	if line == nil {
		line = rc.OriginalLine
	}
	side := "right"
	if strings.EqualFold(rc.Side, "LEFT") {
		side = "left"
	}
	var inReplyTo, reviewID string
	if rc.InReplyToID != nil {
		inReplyTo = strconv.FormatInt(*rc.InReplyToID, 10)
	}
	if rc.PullRequestReviewID != nil {
		reviewID = strconv.FormatInt(*rc.PullRequestReviewID, 10)
	}
	return forgeComment{
		Kind:      "review",
		ID:        strconv.FormatInt(rc.ID, 10),
		Author:    toForgeUser(rc.User),
		Body:      rc.Body,
		CreatedAt: rc.CreatedAt,
		UpdatedAt: rc.UpdatedAt,
		URL:       rc.HTMLURL,
		Path:      rc.Path,
		Line:      line,
		Side:      side,
		CommitSha: rc.CommitID,
		InReplyTo: inReplyTo,
		ReviewID:  reviewID,
	}
}

func parseUnifiedPatch(patch string) []diffHunk {
	var hunks []diffHunk
	var cur *diffHunk
	var oldLine, newLine int

	for _, raw := range strings.Split(patch, "\n") {
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
			cur = &diffHunk{
				OldStart: oStart,
				OldLines: oLines,
				NewStart: nStart,
				NewLines: nLines,
				Header:   m[5],
				Lines:    []diffLine{},
			}
			oldLine = oStart
			newLine = nStart
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
	if hunks == nil {
		return []diffHunk{}
	}
	return hunks
}

func (f *ForgeHandler) ghJSON(ctx context.Context, method, path string, body any, out any) (int, string, string) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return http.StatusBadRequest, "invalid_args", "invalid request body"
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(f.baseURL, "/")+path, rdr)
	if err != nil {
		return http.StatusBadRequest, "invalid_args", "bad github request"
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "tachyon-console/0.1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return http.StatusBadGateway, "unavailable", "github API unreachable"
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return http.StatusBadGateway, "unavailable", "github response unreadable"
	}
	if resp.StatusCode >= 400 {
		var ghErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &ghErr)
		msg := ghErr.Message
		if msg == "" {
			msg = fmt.Sprintf("github API HTTP %d", resp.StatusCode)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return http.StatusUnauthorized, "unauthorized", msg
		case http.StatusForbidden:
			if resp.Header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(strings.ToLower(msg), "rate limit") {
				return http.StatusTooManyRequests, "rate_limited", msg
			}
			return http.StatusForbidden, "unauthorized", msg
		case http.StatusNotFound:
			return http.StatusNotFound, "not_found", msg
		case http.StatusConflict:
			return http.StatusConflict, "conflict", msg
		case http.StatusUnprocessableEntity, http.StatusBadRequest:
			return http.StatusUnprocessableEntity, "invalid_args", msg
		case http.StatusTooManyRequests:
			return http.StatusTooManyRequests, "rate_limited", msg
		default:
			return http.StatusBadGateway, "unavailable", msg
		}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return http.StatusBadGateway, "unavailable", "invalid JSON from github API"
		}
	}
	return 0, "", ""
}

func forgeOK(status int, data any) subprocess.HTTPResponse {
	b, _ := json.Marshal(map[string]any{"data": data})
	return subprocess.HTTPResponse{
		Status:  status,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    b,
	}
}

func forgeErr(status int, code, msg string) subprocess.HTTPResponse {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": msg,
		},
	})
	return subprocess.HTTPResponse{
		Status:  status,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    b,
	}
}

func parseQuery(req subprocess.HTTPRequest) url.Values {
	if req.RawQuery != "" {
		if v, err := url.ParseQuery(req.RawQuery); err == nil {
			return v
		}
	}
	v := url.Values{}
	for k, val := range req.Query {
		v.Set(k, val)
	}
	return v
}

func clampLimit(s string, def, max int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func hasAssignee(list []ghUser, loginLower string) bool {
	for _, u := range list {
		if strings.ToLower(u.Login) == loginLower {
			return true
		}
	}
	return false
}

func parseRepoURL(repoURL string) (string, string) {
	// e.g. https://api.github.com/repos/hollis-labs/tether
	const marker = "/repos/"
	idx := strings.Index(repoURL, marker)
	if idx < 0 {
		return "", ""
	}
	parts := strings.Split(strings.Trim(repoURL[idx+len(marker):], "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

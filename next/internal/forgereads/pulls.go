package forgereads

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The PR workspace reads the old console plugin served by proxying GitHub (plugins/console/forge.go):
// the PR detail, its commits and its comments, in the plugin's camelCase shapes (the console UI's
// forge/types.ts). They now share this package's GitHub client and its error mapping. The plugin's
// writes (comments, reviews, merge, assignees) are slice B11b.

var cwRefRe = regexp.MustCompile(`\bCW-\d{8}-\d{4}\b`)

type repoRef struct {
	Host  string `json:"host"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type prRef struct {
	Repo   repoRef `json:"repo"`
	Number int     `json:"number"`
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
	ID          string `json:"id"`
	Author      user   `json:"author"`
	State       string `json:"state"`
	Body        string `json:"body,omitempty"`
	SubmittedAt string `json:"submittedAt,omitempty"`
	CommitSha   string `json:"commitSha,omitempty"`
}

type pullRequestDTO struct {
	Ref          prRef       `json:"ref"`
	Title        string      `json:"title"`
	Body         string      `json:"body,omitempty"`
	State        string      `json:"state"`
	Draft        bool        `json:"draft,omitempty"`
	Author       user        `json:"author"`
	Assignees    []user      `json:"assignees"`
	Reviewers    []user      `json:"reviewers,omitempty"`
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

type commitDTO struct {
	Sha        string   `json:"sha"`
	Message    string   `json:"message"`
	Author     user     `json:"author"`
	AuthoredAt string   `json:"authoredAt"`
	Parents    []string `json:"parents,omitempty"`
	URL        string   `json:"url,omitempty"`
}

type forgeComment struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Author    user   `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	URL       string `json:"url,omitempty"`
	Path      string `json:"path,omitempty"`
	Line      *int   `json:"line,omitempty"`
	Side      string `json:"side,omitempty"`
	CommitSha string `json:"commitSha,omitempty"`
	InReplyTo string `json:"inReplyTo,omitempty"`
	ReviewID  string `json:"reviewId,omitempty"`
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
	Mergeable      *bool   `json:"mergeable"`
	MergeableState string  `json:"mergeable_state"`
	Additions      int     `json:"additions"`
	Deletions      int     `json:"deletions"`
	ChangedFiles   int     `json:"changed_files"`
	Comments       int     `json:"comments"`
	ReviewComments int     `json:"review_comments"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	MergedAt       *string `json:"merged_at"`
	HTMLURL        string  `json:"html_url"`
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

type ghCommit struct {
	SHA     string  `json:"sha"`
	HTMLURL string  `json:"html_url"`
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

func pullPath(owner, name string, num int) string {
	return fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), num)
}

// pull is GET …/pulls/{num}: the PR with its reviews and the check runs at its head. The reviews and
// checks are read alongside; either failing leaves its list empty, as the plugin did.
func (h *Handler) pull(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := prTarget(r)
	if e != nil {
		fail(w, e)
		return
	}
	pr, e := h.fullPull(r.Context(), owner, name, num)
	if e != nil {
		fail(w, e)
		return
	}
	send(w, pr)
}

func (h *Handler) fullPull(ctx context.Context, owner, name string, num int) (pullRequestDTO, *apiErr) {
	var p ghPull
	base := pullPath(owner, name, num)
	if e := h.get(ctx, base, &p); e != nil {
		return pullRequestDTO{}, e
	}
	var (
		reviews []reviewDTO
		checks  []checkRun
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var revs []ghReview
		if h.get(ctx, base+"/reviews?per_page=100", &revs) == nil {
			reviews = mapReviews(revs)
		}
	}()
	go func() {
		defer wg.Done()
		if p.Head.SHA == "" {
			return
		}
		var cr ghCheckRunsResp
		path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(p.Head.SHA))
		if h.get(ctx, path, &cr) == nil {
			checks = mapCheckRuns(cr)
		}
	}()
	wg.Wait()
	return toPullRequestDTO(owner, name, p, checks, reviews), nil
}

// commits is GET …/pulls/{num}/commits (one page of 100, as the plugin read).
func (h *Handler) commits(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := prTarget(r)
	if e != nil {
		fail(w, e)
		return
	}
	var cs []ghCommit
	if e := h.get(r.Context(), pullPath(owner, name, num)+"/commits?per_page=100", &cs); e != nil {
		fail(w, e)
		return
	}
	out := make([]commitDTO, 0, len(cs))
	for _, c := range cs {
		author := user{Login: c.Commit.Author.Name}
		if c.Author != nil && c.Author.Login != "" {
			author = toUser(*c.Author)
		}
		parents := make([]string, 0, len(c.Parents))
		for _, p := range c.Parents {
			parents = append(parents, p.SHA)
		}
		out = append(out, commitDTO{Sha: c.SHA, Message: c.Commit.Message, Author: author, AuthoredAt: c.Commit.Author.Date, Parents: parents, URL: c.HTMLURL})
	}
	send(w, map[string]any{"items": out})
}

// comments is GET …/pulls/{num}/comments: the conversation (issue) comments and the review (inline)
// comments, oldest first. The two lists are read alongside; the plugin answered whatever it got, so
// does this, except that it answers the error when both reads fail.
func (h *Handler) comments(w http.ResponseWriter, r *http.Request) {
	owner, name, num, e := prTarget(r)
	if e != nil {
		fail(w, e)
		return
	}
	var (
		issue           []ghIssueComment
		review          []ghReviewComment
		issueE, reviewE *apiErr
		wg              sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		issueE = h.get(r.Context(), fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", url.PathEscape(owner), url.PathEscape(name), num), &issue)
	}()
	go func() {
		defer wg.Done()
		reviewE = h.get(r.Context(), pullPath(owner, name, num)+"/comments?per_page=100", &review)
	}()
	wg.Wait()
	if issueE != nil && reviewE != nil {
		fail(w, issueE)
		return
	}
	out := make([]forgeComment, 0, len(issue)+len(review))
	for _, ic := range issue {
		out = append(out, forgeComment{Kind: "issue", ID: strconv.FormatInt(ic.ID, 10), Author: toUser(ic.User), Body: ic.Body,
			CreatedAt: ic.CreatedAt, UpdatedAt: ic.UpdatedAt, URL: ic.HTMLURL})
	}
	for _, rc := range review {
		out = append(out, mapReviewComment(rc))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	send(w, map[string]any{"items": out})
}

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
	assignees := make([]user, 0, len(p.Assignees))
	for _, a := range p.Assignees {
		assignees = append(assignees, toUser(a))
	}
	reviewers := make([]user, 0, len(p.RequestedReviewers))
	for _, rv := range p.RequestedReviewers {
		reviewers = append(reviewers, toUser(rv))
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
			mergeable = map[bool]string{true: "mergeable", false: "conflicting"}[*p.Mergeable]
		}
	}
	var mergedAt string
	if p.MergedAt != nil {
		mergedAt = *p.MergedAt
	}
	return pullRequestDTO{
		Ref:   prRef{Repo: repoRef{Host: "github", Owner: owner, Name: name}, Number: p.Number},
		Title: p.Title, Body: p.Body, State: state, Draft: p.Draft, Author: toUser(p.User),
		Assignees: assignees, Reviewers: reviewers, Labels: labels,
		BaseBranch: p.Base.Ref, HeadBranch: p.Head.Ref, HeadSha: p.Head.SHA, Mergeable: mergeable,
		Checks: checks, Reviews: reviews, Additions: p.Additions, Deletions: p.Deletions, ChangedFiles: p.ChangedFiles,
		CommentCount: p.Comments + p.ReviewComments, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, MergedAt: mergedAt,
		URL: p.HTMLURL, TaskID: cwRefRe.FindString(p.Title + " " + p.Head.Ref + " " + p.Body),
	}
}

func mapReviews(in []ghReview) []reviewDTO {
	out := make([]reviewDTO, 0, len(in))
	for _, rv := range in {
		out = append(out, reviewDTO{ID: strconv.FormatInt(rv.ID, 10), Author: toUser(rv.User), State: strings.ToLower(rv.State),
			Body: rv.Body, SubmittedAt: rv.SubmittedAt, CommitSha: rv.CommitID})
	}
	return out
}

func mapCheckRuns(in ghCheckRunsResp) []checkRun {
	out := make([]checkRun, 0, len(in.CheckRuns))
	for _, c := range in.CheckRuns {
		out = append(out, checkRun{ID: strconv.FormatInt(c.ID, 10), Name: c.Name, Status: c.Status, Conclusion: c.Conclusion,
			StartedAt: c.StartedAt, CompletedAt: c.CompletedAt, URL: c.HTMLURL})
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
	return forgeComment{Kind: "review", ID: strconv.FormatInt(rc.ID, 10), Author: toUser(rc.User), Body: rc.Body,
		CreatedAt: rc.CreatedAt, UpdatedAt: rc.UpdatedAt, URL: rc.HTMLURL, Path: rc.Path, Line: line, Side: side,
		CommitSha: rc.CommitID, InReplyTo: inReplyTo, ReviewID: reviewID}
}

package forgereads

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/next/internal/forgeapi"
)

// The repository and PR-list reads the old console plugin proxied (plugins/console/forge.go): a
// repository, its pull requests, and the pull requests of the configured org. Same camelCase shapes
// as the plugin; same GitHub client and error mapping as the other reads here.

// loginShape is a GitHub login (or an app's "name[bot]"), the only thing an author or assignee filter
// may be: the org search puts it in the query as a qualifier.
var loginShape = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:\[bot\])?$`)

// listStates are the states a PR list may ask for.
var listStates = []string{"open", "closed", "merged", "all"}

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

// repo is GET …/repos/{host}/{owner}/{name}.
func (h *Handler) repo(w http.ResponseWriter, r *http.Request) {
	owner, name, e := target(r)
	if e != nil {
		fail(w, e)
		return
	}
	var g struct {
		Description     string `json:"description"`
		DefaultBranch   string `json:"default_branch"`
		Private         bool   `json:"private"`
		StargazersCount int    `json:"stargazers_count"`
		OpenIssuesCount int    `json:"open_issues_count"`
		UpdatedAt       string `json:"updated_at"`
		HTMLURL         string `json:"html_url"`
	}
	if e := h.get(r.Context(), "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), &g); e != nil {
		fail(w, e)
		return
	}
	send(w, forgeRepo{Host: "github", Owner: owner, Name: name, Description: g.Description, DefaultBranch: g.DefaultBranch,
		Private: g.Private, Stars: g.StargazersCount, OpenIssues: g.OpenIssuesCount, UpdatedAt: g.UpdatedAt, URL: g.HTMLURL})
}

// listQuery is the PR list's query: state (default open), author, assignee, q, limit (1 to 100,
// default 30; out of range is clamped, as the plugin did). cursor is accepted and ignored: the plugin
// read one page and answered nextCursor null.
type listQuery struct {
	state, author, assignee, text string
	limit                         int
}

func parseList(r *http.Request) (listQuery, *apiErr) {
	q := r.URL.Query()
	for k := range q {
		if !slices.Contains([]string{"state", "author", "assignee", "q", "limit", "cursor"}, k) {
			return listQuery{}, bad("Unknown parameter " + strconv.Quote(k) + ".")
		}
	}
	l := listQuery{state: q.Get("state"), author: strings.TrimSpace(q.Get("author")), assignee: strings.TrimSpace(q.Get("assignee")),
		text: strings.TrimSpace(q.Get("q")), limit: 30}
	if l.state == "" {
		l.state = "open"
	}
	if !slices.Contains(listStates, l.state) {
		return listQuery{}, bad("state must be open, closed, merged or all.")
	}
	for _, v := range []string{l.author, l.assignee} {
		if v != "" && !loginShape.MatchString(v) {
			return listQuery{}, bad("author and assignee must be GitHub logins.")
		}
	}
	if len(l.text) > 256 {
		return listQuery{}, bad("q is too long.")
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		l.limit = min(n, 100)
	}
	return l, nil
}

// repoPulls is GET …/repos/{host}/{owner}/{name}/pulls: GitHub's PR list, newest update first,
// filtered here by state (merged vs closed), author, assignee and q (title and body).
func (h *Handler) repoPulls(w http.ResponseWriter, r *http.Request) {
	owner, name, e := target(r)
	if e != nil {
		fail(w, e)
		return
	}
	l, e := parseList(r)
	if e != nil {
		fail(w, e)
		return
	}
	ghState := l.state
	if ghState == "merged" {
		ghState = "closed"
	}
	var pulls []ghPull
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&sort=updated&direction=desc&per_page=%d", url.PathEscape(owner), url.PathEscape(name), url.QueryEscape(ghState), l.limit)
	if e := h.get(r.Context(), path, &pulls); e != nil {
		fail(w, e)
		return
	}
	author, assignee, text := strings.ToLower(l.author), strings.ToLower(l.assignee), strings.ToLower(l.text)
	items := make([]pullRequestDTO, 0, len(pulls))
	for _, p := range pulls {
		merged := p.MergedAt != nil && *p.MergedAt != ""
		switch {
		case l.state == "merged" && !merged, l.state == "closed" && merged:
			continue
		case author != "" && strings.ToLower(p.User.Login) != author:
			continue
		case assignee != "" && !slices.ContainsFunc(p.Assignees, func(u ghUser) bool { return strings.ToLower(u.Login) == assignee }):
			continue
		case text != "" && !strings.Contains(strings.ToLower(p.Title+" "+p.Body), text):
			continue
		}
		items = append(items, toPullRequestDTO(owner, name, p, nil, nil))
	}
	send(w, map[string]any{"items": items, "nextCursor": nil})
}

// orgPulls is GET /pulls: an issue search for the configured org's pull requests, newest update first.
func (h *Handler) orgPulls(w http.ResponseWriter, r *http.Request) {
	l, e := parseList(r)
	if e != nil {
		fail(w, e)
		return
	}
	if !validOrg(h.Org) {
		fail(w, &apiErr{status: http.StatusServiceUnavailable, code: "configuration", msg: "No GitHub org is configured (tachyon.upstreams.github.org)."})
		return
	}
	terms := []string{"is:pr", "org:" + h.Org}
	switch l.state {
	case "open":
		terms = append(terms, "is:open")
	case "closed":
		terms = append(terms, "is:closed", "is:unmerged")
	case "merged":
		terms = append(terms, "is:merged")
	}
	if l.author != "" {
		terms = append(terms, "author:"+l.author)
	}
	if l.assignee != "" {
		terms = append(terms, "assignee:"+l.assignee)
	}
	if l.text != "" {
		terms = append(terms, l.text)
	}
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
	path := fmt.Sprintf("/search/issues?q=%s&sort=updated&order=desc&per_page=%d", url.QueryEscape(strings.Join(terms, " ")), l.limit)
	if e := h.get(r.Context(), path, &search); e != nil {
		fail(w, e)
		return
	}
	items := make([]pullRequestDTO, 0, len(search.Items))
	for _, it := range search.Items {
		owner, name := parseRepoURL(it.RepositoryURL)
		if owner == "" || name == "" {
			continue
		}
		p := ghPull{Number: it.Number, Title: it.Title, Body: it.Body, State: it.State, Draft: it.Draft, User: it.User,
			Assignees: it.Assignees, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt, HTMLURL: it.HTMLURL}
		items = append(items, toPullRequestDTO(owner, name, p, nil, nil))
	}
	send(w, map[string]any{"items": items, "nextCursor": nil})
}

func validOrg(s string) bool { return s != "" && loginShape.MatchString(s) }

// parseRepoURL takes owner and name from a search item's repository_url (…/repos/{owner}/{name}); a
// part that is not a safe repository name is dropped.
func parseRepoURL(repoURL string) (string, string) {
	const marker = "/repos/"
	i := strings.Index(repoURL, marker)
	if i < 0 {
		return "", ""
	}
	parts := strings.Split(strings.Trim(repoURL[i+len(marker):], "/"), "/")
	if len(parts) < 2 || !forgeapi.ValidRepoPart(parts[0]) || !forgeapi.ValidRepoPart(parts[1]) {
		return "", ""
	}
	return parts[0], parts[1]
}

// Package forgeindex is the Pull requests index (PR3): a poller keeps a rebuildable index of open PRs
// from GitHub, computes why each is pending, tracks the rate limit, and serves the pending inbox, the
// status and the event stream. GitHub stays the system of record; the token never leaves the server.
// Design: design_tachyon_pull_requests_backend_20261030, sections 3 and 6.
package forgeindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrRateLimited means GitHub refused for rate reasons; the index keeps serving what it has.
var ErrRateLimited = errors.New("github rate limited")

// ErrNoCredential means no token is configured.
var ErrNoCredential = errors.New("no github token")

// GitHub is the small read-only client the poller needs.
type GitHub struct {
	Token string
	Base  string // default https://api.github.com
	HTTP  *http.Client

	mu sync.Mutex
	// rates is the last seen limit per GitHub bucket (X-RateLimit-Resource: core, search, ...). The search
	// bucket is only 30 a minute, so it must never be read as the budget the rate floor guards.
	rates  map[string]rateState
	scopes []string
	etags  map[string]string
	bodies map[string][]byte
}

func (g *GitHub) base() string {
	if g.Base != "" {
		return strings.TrimRight(g.Base, "/")
	}
	return "https://api.github.com"
}

type rateState struct {
	remaining int
	resetMS   int64
}

// Rate reports the last seen rate limit of the core API bucket, which is what the rate floor guards and the
// status shows; ok is false before a core response. Search has its own small bucket and is tracked apart.
func (g *GitHub) Rate() (remaining int, resetMS int64, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rates["core"]
	return r.remaining, r.resetMS, ok
}

// Scopes are the OAuth scopes GitHub reported for the token (classic tokens only).
func (g *GitHub) Scopes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.scopes...)
}

// get fetches a path and decodes JSON into v. GET responses are cached by ETag, so an unchanged list
// answers 304 and does not count against the rate limit.
func (g *GitHub) get(ctx context.Context, path string, v any) error {
	if g.Token == "" {
		return ErrNoCredential
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	g.mu.Lock()
	if g.etags == nil {
		g.etags, g.bodies = map[string]string{}, map[string][]byte{}
	}
	etag := g.etags[path]
	g.mu.Unlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	c := g.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	g.mu.Lock()
	rateOut := false
	if n, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining")); err == nil {
		res := resp.Header.Get("X-RateLimit-Resource")
		if res == "" {
			res = "core"
		}
		if g.rates == nil {
			g.rates = map[string]rateState{}
		}
		st := rateState{remaining: n}
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			st.resetMS = reset * 1000
		}
		g.rates[res] = st
		rateOut = n == 0 // this response's own bucket is empty
	}
	if s := resp.Header.Get("X-OAuth-Scopes"); s != "" {
		g.scopes = nil
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				g.scopes = append(g.scopes, p)
			}
		}
	}
	g.mu.Unlock()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		g.mu.Lock()
		body := g.bodies[path]
		g.mu.Unlock()
		if body == nil {
			return fmt.Errorf("github: 304 without a cached body for %s", path)
		}
		return json.Unmarshal(body, v)
	case resp.StatusCode == 429 || (resp.StatusCode == 403 && rateOut):
		return ErrRateLimited
	case resp.StatusCode == 401:
		return ErrNoCredential
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("github: %s: status %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if et := resp.Header.Get("ETag"); et != "" {
		g.mu.Lock()
		g.etags[path], g.bodies[path] = et, body
		if len(g.bodies) > 600 { // bound the memory; the next fetch is just unconditional
			g.etags, g.bodies = map[string]string{}, map[string][]byte{}
		}
		g.mu.Unlock()
	}
	return json.Unmarshal(body, v)
}

type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Type      string `json:"type"`
}

type ghSearchItem struct {
	Number        int      `json:"number"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	Draft         bool     `json:"draft"`
	HTMLURL       string   `json:"html_url"`
	RepositoryURL string   `json:"repository_url"`
	UpdatedAt     string   `json:"updated_at"`
	User          ghUser   `json:"user"`
	Assignees     []ghUser `json:"assignees"`
}

// repo returns owner and name from the repository_url.
func (s ghSearchItem) repo() (owner, name string, ok bool) {
	parts := strings.Split(strings.TrimRight(s.RepositoryURL, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[len(parts)-2], parts[len(parts)-1], true
}

type ghPull struct {
	Number         int      `json:"number"`
	Title          string   `json:"title"`
	State          string   `json:"state"`
	Draft          bool     `json:"draft"`
	Merged         bool     `json:"merged"`
	MergedAt       *string  `json:"merged_at"`
	Mergeable      *bool    `json:"mergeable"`
	MergeableState string   `json:"mergeable_state"`
	Additions      int      `json:"additions"`
	Deletions      int      `json:"deletions"`
	HTMLURL        string   `json:"html_url"`
	UpdatedAt      string   `json:"updated_at"`
	User           ghUser   `json:"user"`
	Assignees      []ghUser `json:"assignees"`
	Requested      []ghUser `json:"requested_reviewers"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type ghReview struct {
	User  ghUser `json:"user"`
	State string `json:"state"`
}

type ghCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

// Viewer is the token owner.
func (g *GitHub) Viewer(ctx context.Context) (string, error) {
	var u ghUser
	if err := g.get(ctx, "/user", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// Search runs an issue search and returns up to limit results (one or more pages of 100).
func (g *GitHub) Search(ctx context.Context, q string, limit int) ([]ghSearchItem, error) {
	var out []ghSearchItem
	for page := 1; len(out) < limit; page++ {
		var res struct {
			Items []ghSearchItem `json:"items"`
		}
		path := fmt.Sprintf("/search/issues?q=%s&sort=updated&order=desc&per_page=100&page=%d", url.QueryEscape(q), page)
		if err := g.get(ctx, path, &res); err != nil {
			return out, err
		}
		out = append(out, res.Items...)
		if len(res.Items) < 100 {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (g *GitHub) Pull(ctx context.Context, owner, name string, n int) (ghPull, error) {
	var p ghPull
	err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), n), &p)
	return p, err
}

func (g *GitHub) Reviews(ctx context.Context, owner, name string, n int) ([]ghReview, error) {
	var r []ghReview
	err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", url.PathEscape(owner), url.PathEscape(name), n), &r)
	return r, err
}

func (g *GitHub) Checks(ctx context.Context, owner, name, sha string) ([]ghCheck, error) {
	var res struct {
		CheckRuns []ghCheck `json:"check_runs"`
	}
	err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(sha)), &res)
	return res.CheckRuns, err
}

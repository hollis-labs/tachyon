package forgeindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

type fakePR struct {
	Number    int
	Title     string
	Updated   string
	SHA       string
	State     string // mergeable_state
	Draft     bool
	Assignees []string
	Requested []string
	Reviews   []string    // CHANGES_REQUESTED / APPROVED by reviewer-N
	Checks    [][2]string // status, conclusion
	Mentioned bool
	Merged    bool
	Closed    bool
}

type fakeGH struct {
	mu        sync.Mutex
	PRs       map[int]*fakePR
	Remaining int
	Calls     map[string]int
}

func newFakeGH() *fakeGH {
	return &fakeGH{PRs: map[int]*fakePR{}, Remaining: 4000, Calls: map[string]int{}}
}

func (f *fakeGH) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(f.Remaining))
	w.Header().Set("X-RateLimit-Reset", "1793331000")
	w.Header().Set("X-OAuth-Scopes", "repo")
	p := r.URL.Path
	f.Calls[p]++
	js := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	user := func(l string) map[string]any { return map[string]any{"login": l, "avatar_url": "https://a/" + l} }
	switch {
	case p == "/user":
		js(user("chrispian"))
	case p == "/search/issues":
		q := r.URL.Query().Get("q")
		items := []any{}
		for _, pr := range f.PRs {
			if pr.Closed || pr.Merged || (strings.Contains(q, "mentions:") && !pr.Mentioned) {
				continue
			}
			items = append(items, map[string]any{"number": pr.Number, "title": pr.Title, "state": "open", "updated_at": pr.Updated,
				"repository_url": "https://api.github.com/repos/hollis-labs/tachyon", "html_url": fmt.Sprintf("https://github.com/hollis-labs/tachyon/pull/%d", pr.Number), "user": user("octo")})
		}
		js(map[string]any{"items": items})
	case strings.HasSuffix(p, "/reviews"):
		var n int
		fmt.Sscanf(p, "/repos/hollis-labs/tachyon/pulls/%d/reviews", &n)
		out := []any{}
		for i, st := range f.PRs[n].Reviews {
			out = append(out, map[string]any{"user": user(fmt.Sprint("reviewer-", i)), "state": st})
		}
		js(out)
	case strings.HasSuffix(p, "/check-runs"):
		var sha string
		fmt.Sscanf(p, "/repos/hollis-labs/tachyon/commits/%s", &sha)
		sha = strings.TrimSuffix(sha, "/check-runs")
		runs := []any{}
		for _, pr := range f.PRs {
			if pr.SHA == sha {
				for i, c := range pr.Checks {
					runs = append(runs, map[string]any{"name": fmt.Sprint("job-", i), "status": c[0], "conclusion": c[1], "html_url": "https://ci/" + sha})
				}
			}
		}
		js(map[string]any{"check_runs": runs})
	case strings.HasPrefix(p, "/repos/hollis-labs/tachyon/pulls/"):
		var n int
		fmt.Sscanf(p, "/repos/hollis-labs/tachyon/pulls/%d", &n)
		pr, ok := f.PRs[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		as, rq := []any{}, []any{}
		for _, a := range pr.Assignees {
			as = append(as, user(a))
		}
		for _, a := range pr.Requested {
			rq = append(rq, user(a))
		}
		js(map[string]any{"number": n, "title": pr.Title, "state": "open", "draft": pr.Draft, "merged": pr.Merged, "mergeable_state": pr.State,
			"additions": 10, "deletions": 2, "updated_at": pr.Updated, "html_url": fmt.Sprintf("https://github.com/hollis-labs/tachyon/pull/%d", n),
			"user": user("octo"), "assignees": as, "requested_reviewers": rq, "head": map[string]any{"sha": pr.SHA}})
	default:
		http.NotFound(w, r)
	}
}

type env struct {
	gh    *fakeGH
	idx   *Index
	now   *time.Time
	feed  []FeedEvent
	calls func(string) int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fake := newFakeGH()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	e := &env{gh: fake, now: &now}
	e.idx = &Index{DB: db, GH: &GitHub{Token: "t", Base: srv.URL}, Bus: NewBroker(func() time.Time { return now }),
		Now: func() time.Time { return now }, Feed: func(f FeedEvent) { e.feed = append(e.feed, f) },
		Settings: fixedSettings{Settings{Watched: []string{"org:hollis-labs"}, PollS: 60, StaleDays: 7, RateFloor: 200, MaxPending: 100}}}
	e.calls = func(p string) int { fake.mu.Lock(); defer fake.mu.Unlock(); return fake.Calls[p] }
	return e
}

type fixedSettings struct{ s Settings }

func (f fixedSettings) Load(context.Context) Settings { return f.s }

func (e *env) rows(t *testing.T) map[string]Row {
	t.Helper()
	snap, err := e.idx.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Row{}
	for _, r := range snap.Rows {
		m[r.PRKey] = r
	}
	return m
}

const k = "github:hollis-labs/tachyon#"

func seed(e *env) {
	e.gh.PRs[1] = &fakePR{Number: 1, Title: "checks fail", Updated: "2026-10-30T11:00:00Z", SHA: "a1", State: "clean", Assignees: []string{"me"}, Checks: [][2]string{{"completed", "failure"}, {"completed", "success"}}}
	e.gh.PRs[2] = &fakePR{Number: 2, Title: "changes + conflict", Updated: "2026-10-30T10:00:00Z", SHA: "b2", State: "dirty", Assignees: []string{"me"}, Reviews: []string{"CHANGES_REQUESTED", "APPROVED"}}
	e.gh.PRs[3] = &fakePR{Number: 3, Title: "old and unowned", Updated: "2026-10-01T10:00:00Z", SHA: "c3", State: "clean", Requested: []string{"chrispian"}, Mentioned: true}
}

func TestRefreshComputesReasonsAndStaysQuietTheFirstTime(t *testing.T) {
	e := newEnv(t)
	seed(e)
	if err := e.idx.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := e.rows(t)
	want := map[int][]string{1: {"checks_failed"}, 2: {"changes_requested", "conflicting"}, 3: {"review_requested", "mentioned", "unassigned", "stale"}}
	for n, w := range want {
		got := r[k+fmt.Sprint(n)].Reasons
		if strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("PR %d reasons %v, want %v", n, got, w)
		}
	}
	if c := r[k+"1"].Checks; c.Failed != 1 || c.Passed != 1 || c.Total != 2 || len(c.Failing) != 1 {
		t.Errorf("checks %+v", c)
	}
	if r[k+"2"].Mergeable != "conflicting" || r[k+"1"].Mergeable != "mergeable" {
		t.Error("mergeable mapping")
	}
	if len(e.feed) != 0 {
		t.Errorf("first sync must not announce: %v", e.feed)
	}
	if !e.idx.Ready(context.Background()) {
		t.Error("ready after a refresh")
	}
}

func TestUnchangedPRsAreNotRehydratedAndNewReasonsAreAnnouncedOnce(t *testing.T) {
	e := newEnv(t)
	seed(e)
	ctx := context.Background()
	_ = e.idx.Refresh(ctx)
	pulls := e.calls("/repos/hollis-labs/tachyon/pulls/1")
	_ = e.idx.Refresh(ctx)
	if e.calls("/repos/hollis-labs/tachyon/pulls/1") != pulls {
		t.Error("an unchanged PR must not be fetched again")
	}
	// PR 2 gets a failing check: updated, new head.
	e.gh.PRs[2].Updated, e.gh.PRs[2].SHA = "2026-10-30T11:30:00Z", "b3"
	e.gh.PRs[2].Checks = [][2]string{{"completed", "failure"}}
	_ = e.idx.Refresh(ctx)
	if len(e.feed) != 1 || e.feed[0].Kind != "pr.checks_failed" || e.feed[0].ID != "pr:"+k+"2:pr.checks_failed:b3" {
		t.Fatalf("%+v", e.feed)
	}
	_ = e.idx.Refresh(ctx)
	if len(e.feed) != 1 {
		t.Errorf("announced again: %+v", e.feed)
	}
	// Time passes: PR 1 becomes stale; that is announced, once.
	*e.now = e.now.Add(8 * 24 * time.Hour)
	_ = e.idx.Refresh(ctx)
	_ = e.idx.Refresh(ctx)
	stale := 0
	for _, f := range e.feed {
		if f.Kind == "pr.stale" && f.PRKey == k+"1" {
			stale++
		}
	}
	if stale != 1 {
		t.Errorf("stale announcements: %d", stale)
	}
}

func TestMergedPRLeavesTheIndexWithAnEvent(t *testing.T) {
	e := newEnv(t)
	seed(e)
	ctx := context.Background()
	_ = e.idx.Refresh(ctx)
	e.gh.PRs[1].Merged = true
	_ = e.idx.Refresh(ctx)
	if _, ok := e.rows(t)[k+"1"]; ok {
		t.Error("a merged PR must leave the index")
	}
	if len(e.feed) != 1 || e.feed[0].Kind != "pr.merged" {
		t.Errorf("%+v", e.feed)
	}
}

func TestLowRateBudgetDegradesAndKeepsCachedRows(t *testing.T) {
	e := newEnv(t)
	seed(e)
	ctx := context.Background()
	_ = e.idx.Refresh(ctx)
	e.gh.Remaining = 50
	e.gh.PRs[4] = &fakePR{Number: 4, Title: "new", Updated: "2026-10-30T11:50:00Z", SHA: "d4", State: "clean"}
	e.gh.PRs[1].Updated = "2026-10-30T11:55:00Z"
	_ = e.idx.Refresh(ctx) // first call below sees remaining=50 only after a response; run twice
	_ = e.idx.Refresh(ctx)
	st := e.idx.Status(ctx)
	if !st.Degraded || st.Note == "" {
		t.Errorf("status %+v", st)
	}
	r := e.rows(t)
	if _, ok := r[k+"1"]; !ok {
		t.Error("cached rows must stay")
	}
	if _, ok := r[k+"4"]; ok {
		t.Error("a new PR is not hydrated under the rate floor")
	}
}

func TestNoCredentialFallsBackAndReports(t *testing.T) {
	e := newEnv(t)
	e.idx.GH.Token = ""
	if err := e.idx.Refresh(context.Background()); err != ErrNoCredential {
		t.Fatalf("%v", err)
	}
	called := false
	api := &API{Index: e.idx, Fallback: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })}
	api.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/plugins/console/v1/forge/pulls/pending", nil))
	if !called {
		t.Error("before the first refresh the legacy handler answers")
	}
}

func TestPendingStatusAndStream(t *testing.T) {
	e := newEnv(t)
	seed(e)
	ctx := context.Background()
	_ = e.idx.Refresh(ctx)
	api := &API{Index: e.idx, Triage: func(context.Context) (map[string]Triage, error) {
		return map[string]Triage{k + "2": {Status: "dismissed", By: "operator"}, k + "3": {Status: "snoozed", SnoozeUntilMS: 1793330000000}}, nil
	}}
	h := api.Handler()
	get := func(path string) (int, map[string]any) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins/console/v1/forge"+path, nil))
		var b map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		return w.Code, b
	}
	items := func(path string) []any {
		code, b := get(path)
		if code != 200 {
			t.Fatalf("%s: %d %v", path, code, b)
		}
		return b["data"].(map[string]any)["items"].([]any)
	}
	if got := items("/pulls/pending"); len(got) != 1 || got[0].(map[string]any)["pr_key"] != k+"1" {
		t.Errorf("active: %v", got)
	}
	if got := items("/pulls/pending?state=dismissed"); len(got) != 1 || got[0].(map[string]any)["triage"].(map[string]any)["by"] != "operator" {
		t.Errorf("dismissed: %v", got)
	}
	if got := items("/pulls/pending?state=snoozed"); got[0].(map[string]any)["triage"].(map[string]any)["snooze_until"] != float64(1793330000000) {
		t.Errorf("snoozed: %v", got)
	}
	if got := items("/pulls/pending?reason=checks_failed"); len(got) != 1 {
		t.Errorf("reason: %v", got)
	}
	if got := items("/pulls/pending?q=nomatch"); len(got) != 0 {
		t.Errorf("q: %v", got)
	}
	row := items("/pulls/pending")[0].(map[string]any)
	if row["cached"] != true || row["age_s"] == nil || row["fetched_ms"] == nil || row["head_sha"] != "a1" {
		t.Errorf("row %v", row)
	}
	// Paging.
	e.gh.PRs[1].Updated = "2026-10-30T11:59:00Z"
	for i := 10; i < 13; i++ {
		e.gh.PRs[i] = &fakePR{Number: i, Title: "x", Updated: fmt.Sprintf("2026-10-30T11:0%d:00Z", i-10), SHA: fmt.Sprint("s", i), State: "clean", Assignees: []string{"me"}}
	}
	_ = e.idx.Refresh(ctx)
	code, b := get("/pulls/pending?limit=2")
	d := b["data"].(map[string]any)
	if code != 200 || len(d["items"].([]any)) != 2 || d["next_cursor"] != "c2" || d["total"] != float64(4) {
		t.Fatalf("%d %v", code, d)
	}
	if _, b = get("/pulls/pending?limit=2&cursor=c2"); b["data"].(map[string]any)["next_cursor"] != nil {
		t.Errorf("last page: %v", b)
	}
	for _, bad := range []string{"?bogus=1", "?reason=x", "?state=x", "?limit=0", "?limit=101", "?cursor=zz"} {
		if code, _ := get("/pulls/pending" + bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	_, sb := get("/status")
	st := sb["data"].(map[string]any)
	if st["token_owner"] != "chrispian" || st["scope_warning"] == nil || st["watched"].([]any)[0] != "org:hollis-labs" {
		t.Errorf("status %v", st)
	}
	// Stream: a fresh connection gets status; replay and resync.
	stream := func(q string) string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins/console/v1/forge/stream?once=1"+q, nil))
		return w.Body.String()
	}
	if s := stream(""); !strings.Contains(s, `"type":"status"`) {
		t.Errorf("fresh: %s", s)
	}
	base := e.idx.Bus.Head() - 3
	s := stream(fmt.Sprintf("&after=%d", base))
	if strings.Count(s, "id: ") != 3 {
		t.Errorf("replay: %s", s)
	}
	if s := stream("&after=1"); !strings.Contains(s, `"type":"resync"`) {
		t.Errorf("other epoch: %s", s)
	}
	if s := stream(fmt.Sprintf("&after=%d", e.idx.Bus.Head()+5)); !strings.Contains(s, `"type":"resync"`) {
		t.Errorf("past head: %s", s)
	}
	if code, _ := get("/stream?after=-1"); code != 400 {
		t.Errorf("bad after: %d", code)
	}
}

func TestGitHubUsesETagsAndMapsRateLimits(t *testing.T) {
	hits, conditional := 0, 0
	rate := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("X-RateLimit-Remaining", "0")
		if rate {
			w.WriteHeader(403)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional++
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"login":"chrispian"}`))
	}))
	defer srv.Close()
	g := &GitHub{Token: "t", Base: srv.URL}
	for i := 0; i < 2; i++ {
		if v, err := g.Viewer(context.Background()); err != nil || v != "chrispian" {
			t.Fatalf("%q %v", v, err)
		}
	}
	if hits != 2 || conditional != 1 {
		t.Errorf("hits %d conditional %d", hits, conditional)
	}
	rate = true
	if _, err := g.Pull(context.Background(), "o", "n", 1); err != ErrRateLimited {
		t.Errorf("rate limit: %v", err)
	}
	if _, err := (&GitHub{}).Viewer(context.Background()); err != ErrNoCredential {
		t.Errorf("no token: %v", err)
	}
}

package forgereads

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// pullsGH answers the GitHub reads behind the PR detail, commits and comments, and counts requests.
func pullsGH(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/5", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"number":5,"title":"Fix the thing CW-20261115-0087","body":"b","state":"open","draft":false,
"user":{"login":"dev"},"assignees":[{"login":"dep[bot]","type":"Bot"}],"requested_reviewers":[{"login":"rev"}],
"labels":[{"name":"bug"}],"base":{"ref":"main"},"head":{"ref":"fix","sha":%q},"mergeable":null,"mergeable_state":"dirty",
"additions":3,"deletions":1,"changed_files":2,"comments":1,"review_comments":2,"created_at":"2026-10-30T00:00:00Z",
"updated_at":"2026-10-31T00:00:00Z","merged_at":null,"html_url":"https://github.example/o/r/pull/5"}`, sha)
	})
	mux.HandleFunc("/repos/o/r/pulls/5/reviews", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"id":7,"user":{"login":"rev"},"state":"CHANGES_REQUESTED","body":"no","submitted_at":"2026-10-30T01:00:00Z","commit_id":"c"}]`)
	})
	mux.HandleFunc("/repos/o/r/commits/"+sha+"/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"check_runs":[{"id":9,"name":"go","status":"completed","conclusion":"failure","html_url":"u"}]}`)
	})
	mux.HandleFunc("/repos/o/r/pulls/5/commits", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"sha":"s1","html_url":"cu","author":null,"commit":{"message":"m1","author":{"name":"Dev","date":"2026-10-30T00:00:00Z"}},"parents":[{"sha":"p"}]},
{"sha":"s2","author":{"login":"dev"},"commit":{"message":"m2","author":{"name":"Dev","date":"2026-10-30T01:00:00Z"}},"parents":[]}]`)
	})
	mux.HandleFunc("/repos/o/r/issues/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"id":1,"user":{"login":"a"},"body":"later","created_at":"2026-10-30T03:00:00Z"}]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/5/comments", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"id":2,"user":{"login":"b"},"body":"inline","path":"a.go","line":null,"original_line":4,"side":"LEFT",
"commit_id":"c","in_reply_to_id":1,"pull_request_review_id":7,"created_at":"2026-10-30T02:00:00Z"}]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/6", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/repos/o/r/pulls/8", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusForbidden) // a secondary rate limit
	})
	mux.HandleFunc("/repos/o/r/pulls/10", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // no access: not a rate limit
	})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestPullDetail(t *testing.T) {
	var hits atomic.Int64
	s := pullsGH(t, &hits)
	c, m := call(&Handler{Credential: true, HTTP: s.Client(), Base: s.URL}, "/repos/github/o/r/pulls/5")
	d := data(m)
	if c != 200 || d["title"] != "Fix the thing CW-20261115-0087" || d["taskId"] != "CW-20261115-0087" || d["mergeable"] != "conflicting" ||
		d["headSha"] != sha || d["commentCount"].(float64) != 3 || d["baseBranch"] != "main" || d["state"] != "open" {
		t.Fatalf("%d %v", c, m)
	}
	if a := d["assignees"].([]any)[0].(map[string]any); a["isAgent"] != true {
		t.Errorf("assignee %v", a)
	}
	if rv := d["reviews"].([]any)[0].(map[string]any); rv["state"] != "changes_requested" || rv["id"] != "7" {
		t.Errorf("review %v", rv)
	}
	if ck := d["checks"].([]any)[0].(map[string]any); ck["conclusion"] != "failure" || ck["id"] != "9" {
		t.Errorf("check %v", ck)
	}
	if c, _ := call(&Handler{Credential: true, HTTP: s.Client(), Base: s.URL}, "/repos/github/o/r/pulls/404"); c != 404 {
		t.Errorf("missing PR: %d", c)
	}
}

func TestPullCommitsAndComments(t *testing.T) {
	var hits atomic.Int64
	s := pullsGH(t, &hits)
	h := &Handler{Credential: true, HTTP: s.Client(), Base: s.URL}
	c, m := call(h, "/repos/github/o/r/pulls/5/commits")
	items := data(m)["items"].([]any)
	if c != 200 || len(items) != 2 || items[0].(map[string]any)["author"].(map[string]any)["login"] != "Dev" || items[1].(map[string]any)["author"].(map[string]any)["login"] != "dev" {
		t.Fatalf("%d %v", c, m)
	}
	c, m = call(h, "/repos/github/o/r/pulls/5/comments")
	items = data(m)["items"].([]any)
	if c != 200 || len(items) != 2 {
		t.Fatalf("%d %v", c, m)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["kind"] != "review" || first["line"].(float64) != 4 || first["side"] != "left" || first["inReplyTo"] != "1" || first["reviewId"] != "7" || second["kind"] != "issue" {
		t.Errorf("oldest first, review comment mapped: %v", items)
	}
	// Both comment lists missing: the error, not an empty list.
	if c, m := call(h, "/repos/github/o/r/pulls/404/comments"); c != 404 || m["code"] != "not_found" {
		t.Errorf("missing PR comments: %d %v", c, m)
	}
}

// GitHub's answers map onto the flat codes: a refused token is credential_missing (never the
// framework's 401 unauthorized, which signs the browser out), a secondary rate limit is rate_limited
// with its retry, any other refusal is unavailable.
func TestPullErrorMapping(t *testing.T) {
	var hits atomic.Int64
	s := pullsGH(t, &hits)
	h := &Handler{Credential: true, HTTP: s.Client(), Base: s.URL}
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		"/repos/github/o/r/pulls/6":  {401, "credential_missing"},
		"/repos/github/o/r/pulls/8":  {429, "rate_limited"},
		"/repos/github/o/r/pulls/10": {502, "unavailable"},
		"/repos/github/o/r/pulls/x":  {400, "invalid_args"},
		"/repos/gitlab/o/r/pulls/5":  {501, "unavailable"},
	} {
		c, m := call(h, path)
		if c != want.status || m["code"] != want.code || m["message"] == "" {
			t.Errorf("%s: %d %v", path, c, m)
		}
	}
	if _, m := call(h, "/repos/github/o/r/pulls/8"); m["details"].(map[string]any)["retry_after_s"].(float64) != 30 {
		t.Errorf("retry: %v", m)
	}
}

// Disabled answers 503 disabled on every route and sends nothing; an unreadable credential is
// credential_missing and sends nothing either.
func TestDisabledAndCredentialSendNothing(t *testing.T) {
	var hits atomic.Int64
	s := pullsGH(t, &hits)
	off := &Handler{Credential: true, HTTP: s.Client(), Base: s.URL, Disabled: "GitHub reads are off (tachyon.features.forge_index)."}
	rt, err := upstreamauth.Wrap(s.Client().Transport, s.URL, func(context.Context) (string, error) { return "", errors.New("no such secret") })
	if err != nil {
		t.Fatal(err)
	}
	unreadable := &Handler{Credential: true, HTTP: &http.Client{Transport: rt}, Base: s.URL, Org: "acme"}
	for _, r := range Routes {
		path := strings.TrimPrefix(strings.TrimPrefix(r, "GET "), "/api/plugins/console/v1/forge")
		path = strings.NewReplacer("{host}", "github", "{owner}", "o", "{name}", "r", "{num}", "5", "{sha}", sha).Replace(path)
		if c, m := call(off, path); c != 503 || m["code"] != "disabled" || !strings.Contains(m["message"].(string), "forge_index") {
			t.Errorf("off %s: %d %v", path, c, m)
		}
		if c, m := call(unreadable, path); c != 401 || m["code"] != "credential_missing" {
			t.Errorf("unreadable credential %s: %d %v", path, c, m)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached GitHub", n)
	}
}

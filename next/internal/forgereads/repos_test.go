package forgereads

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// reposGH answers a repository, its PR list and the issue search, and records the queries.
func reposGH(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":"r","description":"d","default_branch":"main","private":true,"stargazers_count":4,"open_issues_count":2,"updated_at":"2026-10-30T00:00:00Z","html_url":"https://github.example/o/r"}`)
	})
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") == "closed" {
			fmt.Fprint(w, `[
{"number":2,"title":"Merged one","state":"closed","user":{"login":"dev"},"assignees":[],"merged_at":"2026-10-30T00:00:00Z","head":{"ref":"b"},"base":{"ref":"main"}},
{"number":3,"title":"Closed one","state":"closed","user":{"login":"other"},"assignees":[],"merged_at":null,"head":{"ref":"c"},"base":{"ref":"main"}}]`)
			return
		}
		fmt.Fprint(w, `[
{"number":1,"title":"Open one","body":"","state":"open","user":{"login":"dev"},"assignees":[{"login":"Rev"}],"merged_at":null,"head":{"ref":"a","sha":"s1"},"base":{"ref":"main"}},
{"number":2,"title":"Merged one","state":"closed","user":{"login":"dev"},"assignees":[],"merged_at":"2026-10-30T00:00:00Z","head":{"ref":"b"},"base":{"ref":"main"}},
{"number":3,"title":"Closed one","state":"closed","user":{"login":"other"},"assignees":[],"merged_at":null,"head":{"ref":"c"},"base":{"ref":"main"}}]`)
	})
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"number":7,"title":"t","state":"open","user":{"login":"dev"},"repository_url":"https://api.github.example/repos/o/r"},
{"number":8,"title":"bad","state":"open","user":{"login":"dev"},"repository_url":"https://api.github.example/repos/o/..%2Fx"}]}`)
	})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestRepoAndRepoPulls(t *testing.T) {
	s, _ := reposGH(t)
	h := &Handler{Credential: true, HTTP: s.Client(), Base: s.URL}
	c, m := call(h, "/repos/github/o/r")
	if d := data(m); c != 200 || d["defaultBranch"] != "main" || d["stars"].(float64) != 4 || d["owner"] != "o" || d["private"] != true {
		t.Fatalf("%d %v", c, m)
	}
	numbers := func(path string) string {
		t.Helper()
		c, m := call(h, path)
		if c != 200 || data(m)["nextCursor"] != nil {
			t.Fatalf("%s: %d %v", path, c, m)
		}
		var out []string
		for _, it := range data(m)["items"].([]any) {
			out = append(out, fmt.Sprint(it.(map[string]any)["ref"].(map[string]any)["number"]))
		}
		return strings.Join(out, ",")
	}
	for path, want := range map[string]string{
		"/repos/github/o/r/pulls":                  "1,2,3",
		"/repos/github/o/r/pulls?state=merged":     "2",
		"/repos/github/o/r/pulls?state=closed":     "3",
		"/repos/github/o/r/pulls?author=dev":       "1,2",
		"/repos/github/o/r/pulls?assignee=rev":     "1",
		"/repos/github/o/r/pulls?q=open&cursor=x2": "1",
	} {
		if got := numbers(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
	if _, m := call(h, "/repos/github/o/r/pulls?state=merged"); data(m)["items"].([]any)[0].(map[string]any)["state"] != "merged" {
		t.Errorf("merged state: %v", m)
	}
	for _, bad := range []string{"?state=x", "?author=a%20org:other", "?bogus=1", "?q=" + strings.Repeat("x", 300)} {
		if c, m := call(h, "/repos/github/o/r/pulls"+bad); c != 400 || m["code"] != "invalid_args" {
			t.Errorf("%s: %d %v", bad, c, m)
		}
	}
	if c, _ := call(h, "/repos/github/o/nope"); c != 404 {
		t.Errorf("missing repo: %d", c)
	}
}

func TestOrgPulls(t *testing.T) {
	s, seen := reposGH(t)
	h := &Handler{Credential: true, HTTP: s.Client(), Base: s.URL, Org: "acme"}
	c, m := call(h, "/pulls?state=merged&author=dev&assignee=rev&q=fix&limit=500")
	items := data(m)["items"].([]any)
	if c != 200 || len(items) != 1 || items[0].(map[string]any)["ref"].(map[string]any)["repo"].(map[string]any)["name"] != "r" {
		t.Fatalf("%d %v", c, m)
	}
	q := seen()[0]
	for _, part := range []string{"is%3Apr+org%3Aacme+is%3Amerged+author%3Adev+assignee%3Arev+fix", "per_page=100"} {
		if !strings.Contains(q, part) {
			t.Errorf("search %s lacks %s", q, part)
		}
	}
	if c, m := call(&Handler{Credential: true, HTTP: s.Client(), Base: s.URL}, "/pulls"); c != 503 || m["code"] != "configuration" {
		t.Errorf("no org: %d %v", c, m)
	}
	if n := len(seen()); n != 1 {
		t.Errorf("%d requests; the unconfigured list must send nothing", n)
	}
}

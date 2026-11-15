package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/forgeapi"
)

// forgeSHA is the fake PR's head commit.
const forgeSHA = "0da8631e75cfa86e2506e6b076cceaa4a56f4aa3"

// forgeTriageKey is the inbox triage key of the fake PR, as the client sends it (encodeURIComponent).
const forgeTriageKey = "pr-inbox:github:acme%2Fwidgets%231"

// forgeGitHubReads are the routes that read GitHub, with the fake PR filled in.
var forgeGitHubReads = []string{
	"/viewer",
	"/repos/github/acme/widgets/pulls/1",
	"/repos/github/acme/widgets/pulls/1/files",
	"/repos/github/acme/widgets/pulls/1/diff",
	"/repos/github/acme/widgets/pulls/1/checks",
	"/repos/github/acme/widgets/pulls/1/commits",
	"/repos/github/acme/widgets/pulls/1/comments",
	"/repos/github/acme/widgets/commits/" + forgeSHA,
	"/repos/github/acme/widgets",
	"/repos/github/acme/widgets/pulls?state=open",
	"/pulls?state=open&author=dev",
}

// forgeLocalReads are the forge reads served from the store.
var forgeLocalReads = []string{"/pulls/pending", "/status", "/stream?once=1", "/audit"}

// forgeCredential writes a synthetic GitHub token to a file and returns its secrets reference and value.
func forgeCredential(t *testing.T) (ref, value string) {
	t.Helper()
	value = strings.Join([]string{"test", "github", "credential"}, "-")
	file := filepath.Join(t.TempDir(), "github.token")
	if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return "file://" + file, value
}

func forgeOverride(url, tokenRef string, on bool) string {
	return loopbackOverride + fmt.Sprintf("tachyon:\n  upstreams:\n    github: { url: %q, org: acme, token: %q }\n  features: { forge_index: %t }\n", url, tokenRef, on)
}

// The forge routes in the served binary (slice B11a), with forge_index off. Anonymous is 401 on
// every route; a key without the forge scopes is refused by scope; a key with them reads the
// store-served routes, is refused the audit (operator_required: a key is never an operator) and the
// console state write (requester_cannot_approve: only the browser session writes item state). The
// signed-in owner reads everything served from the store, triages a PR, and is told the GitHub reads
// are off.
func TestForgeRoutesAuth(t *testing.T) {
	base, app := started(t, loopbackOverride)
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "forge-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k, "X-Requested-With": "tachyon-ui"}
	}
	both, readOnly, other := key(forgeRead, forgeWrite), key(forgeRead), key("tachyon.planner.read")
	p := forgeapi.Prefix
	put := forgeapi.StatePrefix + "/" + forgeTriageKey
	dismiss := map[string]any{"type": "dismiss"}
	var reads []string
	for _, r := range append(append([]string{}, forgeGitHubReads...), forgeLocalReads...) {
		reads = append(reads, p+r)
	}
	reads = append(reads, forgeapi.StatePrefix, forgeapi.StatePrefix+"?prefix=pr-inbox:")
	anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
	for _, path := range reads {
		anon.want(401, "GET", path, nil, xrw)
		if code := flatCode(t, keys.want(403, "GET", path, nil, other)); code != "scope_required" {
			t.Errorf("%s without the scope: %s", path, code)
		}
	}
	anon.want(401, "PUT", put, dismiss, xrw)
	if code := flatCode(t, keys.want(403, "PUT", put, dismiss, readOnly)); code != "scope_required" {
		t.Errorf("state PUT with a read key: %s", code)
	}
	if code := flatCode(t, keys.want(403, "PUT", put, dismiss, both)); code != "requester_cannot_approve" {
		t.Errorf("state PUT with a write key: %s", code)
	}
	for _, path := range []string{p + "/pulls/pending", p + "/status", forgeapi.StatePrefix} {
		keys.want(200, "GET", path, nil, readOnly)
	}
	if code := flatCode(t, keys.want(403, "GET", p+"/audit", nil, both)); code != "operator_required" {
		t.Errorf("audit with a key: %s", code)
	}
	for _, path := range forgeGitHubReads {
		if code := flatCode(t, keys.want(503, "GET", p+path, nil, readOnly)); code != "disabled" {
			t.Errorf("%s off, as a key: %s", path, code)
		}
	}

	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	if body := owner.want(200, "GET", p+"/audit", nil, xrw); !bytes.Contains(body, []byte(`"entries":[]`)) {
		t.Errorf("audit: %s", body)
	}
	if code := flatCode(t, owner.want(400, "GET", p+"/audit?limit=0", nil, xrw)); code != "invalid_args" {
		t.Errorf("audit limit: %s", code)
	}
	if body := owner.want(200, "GET", p+"/status", nil, xrw); !bytes.Contains(body, []byte(`"degraded":true`)) || !bytes.Contains(body, []byte("tachyon.features.forge_index")) {
		t.Errorf("status off: %s", body)
	}
	if body := owner.want(200, "GET", p+"/stream?once=1", nil, xrw); !bytes.Contains(body, []byte(`"type":"status"`)) {
		t.Errorf("stream: %s", body)
	}
	for _, path := range forgeGitHubReads {
		body := owner.want(503, "GET", p+path, nil, xrw)
		if code := flatCode(t, body); code != "disabled" || !bytes.Contains(body, []byte("(tachyon.features.forge_index)")) {
			t.Errorf("%s off: %s", path, body)
		}
	}
	// Triage: the PR key's "/" and "#" arrive encoded in one path segment.
	body := owner.want(200, "PUT", put, dismiss, xrw)
	if !bytes.Contains(body, []byte(`"key":"pr-inbox:github:acme/widgets#1"`)) || !bytes.Contains(body, []byte(`"status":"dismissed"`)) {
		t.Errorf("PUT state: %s", body)
	}
	if body := owner.want(200, "GET", forgeapi.StatePrefix+"?prefix=pr-inbox:", nil, xrw); !bytes.Contains(body, []byte(`"version":1`)) {
		t.Errorf("GET state: %s", body)
	}
	if code := flatCode(t, owner.want(409, "PUT", put, map[string]any{"type": "read", "expected_version": 0}, xrw)); code != "conflict" {
		t.Errorf("stale PUT: %s", code)
	}
	if code := flatCode(t, owner.want(400, "PUT", put, map[string]any{"type": "nuke"}, xrw)); code != "invalid_args" {
		t.Errorf("bad op: %s", code)
	}
	// The key's own scope still reads what the owner wrote.
	if body := keys.want(200, "GET", forgeapi.StatePrefix, nil, readOnly); !bytes.Contains(body, []byte("acme/widgets#1")) {
		t.Errorf("GET state as a key: %s", body)
	}
}

// With forge_index off and GitHub configured (address and credential), no route dials it, and the
// poller does not start.
func TestForgeSwitchOffDialsNothing(t *testing.T) {
	up := countingListener(t)
	ref, _ := forgeCredential(t)
	base, _ := started(t, forgeOverride(up.addr, ref, false))
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, path := range forgeGitHubReads {
		c.want(503, "GET", forgeapi.Prefix+path, nil, xrw)
	}
	for _, path := range forgeLocalReads {
		c.want(200, "GET", forgeapi.Prefix+path, nil, xrw)
	}
	c.want(200, "GET", forgeapi.StatePrefix, nil, xrw)
	c.want(200, "PUT", forgeapi.StatePrefix+"/"+forgeTriageKey, map[string]any{"type": "read"}, xrw)
	time.Sleep(300 * time.Millisecond)
	if n := up.conns.Load(); n != 0 {
		t.Fatalf("forge_index off: %d connections to GitHub", n)
	}
}

// fakeGitHub answers the GitHub REST reads the forge makes for one PR (acme/widgets#1), refuses PR 9
// for rate and has no PR 404. It records each request and the Authorization headers it saw.
type fakeGitHub struct {
	mu      sync.Mutex
	reqs    []string
	queries []string
	auths   map[string]bool
}

func (f *fakeGitHub) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, string) {
	t.Helper()
	f := &fakeGitHub{auths: map[string]bool{}}
	repo := "/repos/acme/widgets"
	pr := fmt.Sprintf(`{"number":1,"title":"Widget fix CW-20261115-0087","body":"b","state":"open","draft":false,"merged":false,
"user":{"login":"dev","avatar_url":"https://avatars.example/dev"},"assignees":[],"requested_reviewers":[{"login":"octo-viewer"}],
"labels":[],"base":{"ref":"main"},"head":{"ref":"fix","sha":%q},"mergeable":true,"mergeable_state":"clean",
"additions":3,"deletions":1,"changed_files":1,"comments":1,"review_comments":0,"created_at":"2026-11-14T00:00:00Z",
"updated_at":"2026-11-15T00:00:00Z","merged_at":null,"html_url":"https://github.example/acme/widgets/pull/1"}`, forgeSHA)
	bodies := map[string]string{
		"/user": `{"login":"octo-viewer"}`,
		"/search/issues": `{"items":[{"number":1,"title":"Widget fix CW-20261115-0087","state":"open","updated_at":"2026-11-15T00:00:00Z",
"repository_url":"https://api.github.example/repos/acme/widgets","html_url":"https://github.example/acme/widgets/pull/1","user":{"login":"dev"}}]}`,
		repo + "/pulls/1":         pr,
		repo + "/pulls/1/reviews": `[]`,
		repo:                      `{"name":"widgets","default_branch":"main","private":false,"stargazers_count":2,"open_issues_count":1,"html_url":"https://github.example/acme/widgets"}`,
		repo + "/pulls":           "[" + pr + "]",
		repo + "/commits/" + forgeSHA + "/check-runs": `{"check_runs":[{"id":3,"name":"go","status":"completed","conclusion":"failure",
"started_at":"2026-11-15T00:00:00Z","completed_at":"2026-11-15T00:01:00Z","html_url":"https://ci.example/3","output":{"summary":"1 failed","annotations_count":1}}]}`,
		repo + "/commits/" + forgeSHA + "/status": `{"statuses":[{"context":"ci/ok","state":"success"}]}`,
		repo + "/commits/" + forgeSHA:             `{"sha":"` + forgeSHA + `","html_url":"https://github.example/c","author":{"login":"dev"},"commit":{"message":"fix","author":{"name":"Dev","date":"2026-11-15T00:00:00Z"}},"parents":[{"sha":"p1"}],"files":[{}]}`,
		repo + "/pulls/1/files":                   `[{"filename":"a.go","status":"modified","additions":1,"deletions":1,"patch":"@@ -1,2 +1,2 @@ func A()\n ctx\n-old\n+new\n"}]`,
		repo + "/pulls/1/commits":                 `[{"sha":"` + forgeSHA + `","commit":{"message":"fix","author":{"name":"Dev","date":"2026-11-15T00:00:00Z"}},"author":{"login":"dev"},"parents":[]}]`,
		repo + "/pulls/1/comments":                `[]`,
		repo + "/issues/1/comments":               `[{"id":5,"user":{"login":"dev"},"body":"looks good","created_at":"2026-11-15T00:02:00Z"}]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
		f.auths[r.Header.Get("Authorization")] = true
		if q := r.URL.Query().Get("q"); q != "" {
			f.queries = append(f.queries, q)
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		if r.URL.Path == repo+"/pulls/9" {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "4000")
		if r.URL.Path == "/search/issues" && strings.Contains(r.URL.Query().Get("q"), "mentions:") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		b, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// With forge_index on and a fake GitHub: the poller indexes the PR and the pending inbox serves it
// (with its reasons, the triage state and the linked agent), and every GitHub read answers from the
// fake with the configured credential. GitHub's 404 and rate limit map onto the flat codes.
func TestForgeWithFakeGitHub(t *testing.T) {
	fake, url := newFakeGitHub(t)
	ref, value := forgeCredential(t)
	base, app := started(t, forgeOverride(url, ref, true))
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	p := forgeapi.Prefix

	type pending struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	var got pending
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := json.Unmarshal(c.want(200, "GET", p+"/pulls/pending", nil, xrw), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data.Items) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(got.Data.Items) != 1 {
		t.Fatalf("the poller never indexed the PR: %+v (requests %v)", got, fake.requests())
	}
	row := got.Data.Items[0]
	reasons, _ := json.Marshal(row["reasons"])
	if row["pr_key"] != "github:acme/widgets#1" || row["head_sha"] != forgeSHA || string(reasons) != `["checks_failed","review_requested","unassigned"]` {
		t.Errorf("pending row: %v", row)
	}
	if body := c.want(200, "GET", p+"/status", nil, xrw); !bytes.Contains(body, []byte(`"token_owner":"octo-viewer"`)) || !bytes.Contains(body, []byte(`"degraded":false`)) {
		t.Errorf("status: %s", body)
	}

	// Triage and a linked agent show on the pending row.
	c.want(200, "PUT", forgeapi.StatePrefix+"/"+forgeTriageKey, map[string]any{"type": "dismiss"}, xrw)
	if err := json.Unmarshal(c.want(200, "GET", p+"/pulls/pending", nil, xrw), &got); err != nil || len(got.Data.Items) != 0 {
		t.Errorf("dismissed PR still active: %+v %v", got, err)
	}
	if _, err := database.From(app).ExecContext(context.Background(), `INSERT INTO pr_agent_links(link_id, pr_key, agent_id, agent_urn, task, dedupe_key, status, created_ms, updated_ms) VALUES ('l1', 'github:acme/widgets#1', 'builder', 'msg://agent/x/builder', 'review', 'd1', 'sent', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	body := c.want(200, "GET", p+"/pulls/pending?state=dismissed", nil, xrw)
	if !bytes.Contains(body, []byte(`"kind":"agent"`)) || !bytes.Contains(body, []byte(`"urn":"msg://agent/x/builder"`)) || !bytes.Contains(body, []byte(`"status":"dismissed"`)) {
		t.Errorf("dismissed with an agent: %s", body)
	}

	want := map[string]string{
		"/viewer":                                        `"login":"octo-viewer"`,
		"/repos/github/acme/widgets/pulls/1":             `"taskId":"CW-20261115-0087"`,
		"/repos/github/acme/widgets/pulls/1/files":       `"path":"a.go"`,
		"/repos/github/acme/widgets/pulls/1/diff":        `"header":"func A()"`,
		"/repos/github/acme/widgets/pulls/1/checks":      `"annotations_count":1`,
		"/repos/github/acme/widgets/pulls/1/commits":     `"authoredAt":"2026-11-15T00:00:00Z"`,
		"/repos/github/acme/widgets/pulls/1/comments":    `"body":"looks good"`,
		"/repos/github/acme/widgets/commits/" + forgeSHA: `"parents":["p1"]`,
		"/repos/github/acme/widgets":                     `"defaultBranch":"main"`,
		"/repos/github/acme/widgets/pulls?state=open":    `"headBranch":"fix"`,
		"/pulls?state=open&author=dev":                   `"title":"Widget fix CW-20261115-0087"`,
	}
	for _, path := range forgeGitHubReads {
		if got := c.want(200, "GET", p+path, nil, xrw); !bytes.Contains(got, []byte(want[path])) {
			t.Errorf("%s: %s", path, got)
		}
	}
	if code := flatCode(t, c.want(404, "GET", p+"/repos/github/acme/widgets/pulls/404", nil, xrw)); code != "not_found" {
		t.Errorf("missing PR: %s", code)
	}
	body = c.want(429, "GET", p+"/repos/github/acme/widgets/pulls/9", nil, xrw)
	var limited struct {
		Code    string
		Details struct {
			RetryAfterS int `json:"retry_after_s"`
		}
	}
	if err := json.Unmarshal(body, &limited); err != nil || limited.Code != "rate_limited" || limited.Details.RetryAfterS < 60 {
		t.Errorf("rate limit: %s", body)
	}
	if code := flatCode(t, c.want(501, "GET", p+"/repos/gitlab/acme/widgets/pulls/1", nil, xrw)); code != "unavailable" {
		t.Errorf("other host: %s", code)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !strings.Contains(strings.Join(fake.queries, "\n"), "is:pr org:acme is:open author:dev") {
		t.Errorf("org list search: %v", fake.queries)
	}
	if len(fake.auths) != 1 || !fake.auths["Bearer "+value] {
		t.Errorf("GitHub saw other credentials than the configured one (%d distinct)", len(fake.auths))
	}
}

// The forge event stream ends when the server shuts down, so Serve returns within its grace period.
func TestForgeStreamEndsOnShutdown(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	env := overrideEnv(t, data, loopbackOverride)
	createOwner(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, bootOptions(func(k string) string { return env[k] }, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- (&tachyon{app: run.App, ready: func(a net.Addr) { addr <- a }}).serve(ctx) }()
	base := "http://" + (<-addr).String()
	c := newClient(t, base)
	c.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	req, _ := http.NewRequest(http.MethodGet, base+forgeapi.Prefix+"/stream", nil)
	res, err := (&http.Client{Jar: c.http.Jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	begin := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve ended with %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not return within the grace period with the forge stream open")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("shutdown took %s with the forge stream open", took)
	}
}

// The console state write needs the operator, as the old RequireOperator did: a signed-in person with
// the forge scopes but without tachyon.operator reads the state and is refused the write.
func TestForgeStateWriteNeedsTheOperator(t *testing.T) {
	base, _ := started(t, loopbackOverride)
	owner := newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	password := strings.Join([]string{"plain", "member", "pass", "phrase"}, " ")
	owner.want(201, "POST", "/api/identity/users", map[string]any{"username": "member", "display_name": "Member", "password": password, "scopes": []string{forgeRead, forgeWrite}}, xrw)
	member := newClient(t, base)
	member.want(200, "POST", "/api/login", map[string]string{"username": "member", "password": password}, xrw)
	member.want(200, "GET", forgeapi.StatePrefix, nil, xrw)
	member.want(200, "GET", forgeapi.Prefix+"/pulls/pending", nil, xrw)
	put := forgeapi.StatePrefix + "/" + forgeTriageKey
	if code := flatCode(t, member.want(403, "PUT", put, map[string]any{"type": "dismiss"}, xrw)); code != "operator_required" {
		t.Errorf("state PUT without the operator: %s", code)
	}
	if code := flatCode(t, member.want(403, "GET", forgeapi.Prefix+"/audit", nil, xrw)); code != "operator_required" {
		t.Errorf("audit without the operator: %s", code)
	}
	owner.want(200, "PUT", put, map[string]any{"type": "dismiss"}, xrw)
}

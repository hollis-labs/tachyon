package forgeapi

import (
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const O = "/repos/github/hollis-labs/tachyon/pulls/"

func do(t *testing.T, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	if !strings.HasPrefix(path, "/api/") {
		path = Prefix + path
	}
	r := httptest.NewRequest(method, "http://x"+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	return w
}

func wr(t *testing.T, method, path, body, key string, hdr ...string) *httptest.ResponseRecorder {
	return do(t, method, path, body, append([]string{"Idempotency-Key", key}, hdr...)...)
}

type env struct {
	Data  json.RawMessage `json:"data"`
	Error map[string]any  `json:"error"`
}

func data(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	var e env
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Data == nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(e.Data, v); err != nil {
		t.Fatal(err)
	}
}

func errCodeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e env
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	c, _ := e.Error["code"].(string)
	return c
}

func head(t *testing.T, num string) string {
	var p struct {
		HeadSha string `json:"headSha"`
	}
	data(t, do(t, "GET", O+num, ""), &p)
	return p.HeadSha
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|github_pat_|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
	n := 0
	err := fs.WalkDir(assets, "fixtures", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		var v any
		if json.Unmarshal(b, &v) != nil {
			t.Errorf("%s is not valid JSON", p)
		}
		if m := secret.Find(b); m != nil {
			t.Errorf("%s looks like it holds a secret: %q", p, m)
		}
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatal("no fixtures", err)
	}
}

func TestContractHelpers(t *testing.T) {
	k := PRKey("hollis-labs", "tachyon", 7)
	if o, n, num, ok := ParsePRKey(k); !ok || o != "hollis-labs" || n != "tachyon" || num != 7 {
		t.Error(k)
	}
	for _, bad := range []string{"", "github:a/b#0", "github:a/b", "gitlab:a/b#1", "github:a b/c#1", "github:../x#1"} {
		if _, _, _, ok := ParsePRKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if DedupeKey(k, "a", "review") != "pr-assign:github:hollis-labs/tachyon#7:a:review" || MessageKey("d", 0) != "d" || MessageKey("d", 2) != "d#2" {
		t.Error("keys")
	}
	h := strings.Repeat("ab", 20)
	if !ValidHeadSHA(h) || ValidHeadSHA("abc") || ValidHeadSHA(strings.ToUpper(h)) || !InlineAllowed(h, h) || InlineAllowed("", h) || InlineAllowed(strings.Repeat("cd", 20), h) {
		t.Error("sha helpers")
	}
	if ValidRepoPart("..") || ValidRepoPart("a/b") || ValidRepoPart("") || !ValidRepoPart("tachyon-1.x") {
		t.Error("repo part")
	}
	for op := range WriteLevels {
		if !slices.Contains(AuditOps, op) {
			t.Errorf("WriteLevels has unknown op %s", op)
		}
	}
	if WriteLevels["merge"] != "operator-token" {
		t.Error("merge needs operator-token (proposed)")
	}
}

func TestPendingCoversReasonsStatesAssigneesAndFilters(t *testing.T) {
	var d struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
		Next  *string          `json:"next_cursor"`
	}
	data(t, do(t, "GET", "/pulls/pending", ""), &d)
	reasons, kinds, links, cached := map[string]bool{}, map[string]bool{}, map[string]bool{}, false
	for _, it := range d.Items {
		for _, r := range it["reasons"].([]any) {
			reasons[r.(string)] = true
		}
		for _, a := range it["assignees"].([]any) {
			am := a.(map[string]any)
			kinds[am["kind"].(string)] = true
			if s, ok := am["link_status"].(string); ok {
				links[s] = true
			}
		}
		cached = cached || it["cached"] == true
		if it["triage"].(map[string]any)["status"] != "active" {
			t.Errorf("default state is active: %v", it["pr_key"])
		}
	}
	for _, r := range Reasons {
		if r != "mentioned" && !reasons[r] { // mentioned lives on the snoozed row
			t.Errorf("no fixture row for reason %s", r)
		}
	}
	if reasons["mentioned"] {
		t.Error("the snoozed row must not be in the default list")
	}
	if !kinds["agent"] || !kinds["user"] || !links["sent"] || !links["failed"] || !cached {
		t.Errorf("assignee/link/cached coverage: %v %v %v", kinds, links, cached)
	}
	data(t, do(t, "GET", "/pulls/pending?state=snoozed", ""), &d)
	if len(d.Items) != 1 || d.Items[0]["reasons"].([]any)[0] != "mentioned" || d.Items[0]["triage"].(map[string]any)["snooze_until"] == nil {
		t.Errorf("snoozed: %v", d.Items)
	}
	data(t, do(t, "GET", "/pulls/pending?reason=checks_failed", ""), &d)
	if len(d.Items) != 2 {
		t.Errorf("reason filter: %d", len(d.Items))
	}
	data(t, do(t, "GET", "/pulls/pending?repo=hollis-labs/cerberus", ""), &d)
	if len(d.Items) != 1 {
		t.Errorf("repo filter: %d", len(d.Items))
	}
	data(t, do(t, "GET", "/pulls/pending?q=A3", ""), &d)
	if len(d.Items) != 1 {
		t.Errorf("q: %d", len(d.Items))
	}
	data(t, do(t, "GET", "/pulls/pending?limit=2", ""), &d)
	if len(d.Items) != 2 || d.Next == nil || d.Total != 7 {
		t.Fatalf("page 1: %d next=%v total=%d", len(d.Items), d.Next, d.Total)
	}
	data(t, do(t, "GET", "/pulls/pending?limit=2&cursor="+*d.Next, ""), &d)
	if len(d.Items) != 2 || d.Next == nil {
		t.Errorf("page 2: %d", len(d.Items))
	}
	for _, bad := range []string{"?reason=x", "?state=x", "?limit=0", "?limit=101", "?cursor=zz"} {
		if do(t, "GET", "/pulls/pending"+bad, "").Code != 400 {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestStatusEnvelopesAndScenarios(t *testing.T) {
	var s map[string]any
	data(t, do(t, "GET", "/status", ""), &s)
	if s["degraded"] != false || s["token_owner"] == "" || s["scope_warning"] == nil {
		t.Errorf("status: %v", s)
	}
	data(t, do(t, "GET", "/status", "", "X-Stub-Scenario", "degraded"), &s)
	if s["degraded"] != true {
		t.Error("degraded scenario")
	}
	var d map[string]any
	data(t, do(t, "GET", "/pulls/pending", "", "X-Stub-Scenario", "degraded"), &d)
	if d["cached"] != true || d["degraded"] != true {
		t.Errorf("degraded pending: %v", d)
	}
	w := do(t, "GET", "/pulls/pending", "", "X-Stub-Scenario", "rate_limited")
	if w.Code != 429 || w.Header().Get("Retry-After") != "42" || errCodeOf(t, w) != "rate_limited" || !strings.Contains(w.Body.String(), `"retry_after_s":42`) {
		t.Errorf("rate limited: %d %s", w.Code, w.Body)
	}
	if w := do(t, "GET", "/status", "", "X-Stub-Scenario", "credential_missing"); w.Code != 401 || errCodeOf(t, w) != "credential_missing" {
		t.Error("credential_missing")
	}
	if w := do(t, "GET", "/status", "", "X-Stub-Status", "503"); w.Code != 503 || errCodeOf(t, w) != "unavailable" {
		t.Error("forced")
	}
}

func TestRepoGuardsAndReads(t *testing.T) {
	for path, want := range map[string]int{
		"/repos/gitlab/hollis-labs/tachyon":            501,
		"/repos/github/hollis-labs/unknown-repo":       403,
		"/repos/github/evil-org/tachyon":               403,
		"/repos/github/hollis-labs/tachyon/pulls/9999": 404,
		"/repos/github/hollis-labs/tachyon/pulls/x":    400,
		"/repos/github/hollis-labs/tachyon":            200,
	} {
		if w := do(t, "GET", path, ""); w.Code != want {
			t.Errorf("%s: %d want %d", path, w.Code, want)
		}
	}
	if do(t, "GET", "/repos/github/hol$lis/tachyon", "").Code != 400 {
		t.Error("owner characters")
	}
	var d struct {
		Items []map[string]any `json:"items"`
	}
	data(t, do(t, "GET", O+"201/diff", ""), &d)
	flags := map[string]bool{}
	for _, f := range d.Items {
		if f["binary"] == true {
			flags["binary"] = true
		}
		if f["truncated"] == true {
			flags["truncated"] = true
		}
	}
	if !flags["binary"] || !flags["truncated"] {
		t.Errorf("diff fixtures cover binary and truncated: %v", flags)
	}
	data(t, do(t, "GET", O+"201/diff?path=internal/server/server.go", ""), &d)
	if len(d.Items) != 1 || do(t, "GET", O+"201/diff?path=nope", "").Code != 404 {
		t.Error("lazy per-file diff")
	}
	var c struct {
		Checks []map[string]any `json:"checks"`
	}
	data(t, do(t, "GET", O+"201/checks", ""), &c)
	if len(c.Checks) != 2 || c.Checks[0]["conclusion"] != "failure" {
		t.Errorf("failures first: %v", c.Checks)
	}
	for _, p := range []string{"/files", "/commits", "/comments"} {
		if w := do(t, "GET", O+"201"+p, ""); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	h := head(t, "202")
	if w := do(t, "GET", "/repos/github/hollis-labs/tachyon/commits/"+h, ""); w.Code != 200 {
		t.Errorf("commit: %d", w.Code)
	}
	if w := do(t, "GET", "/repos/github/hollis-labs/tachyon/commits/deadbeef", ""); w.Code != 404 {
		t.Errorf("unknown commit: %d", w.Code)
	}
	if w := do(t, "GET", "/viewer", ""); w.Code != 200 {
		t.Error("viewer")
	}
}

func TestPreflightCoversEveryBlockerAndState(t *testing.T) {
	seen, states := map[string]bool{}, map[string]bool{}
	for _, c := range []struct{ repo, num string }{{"tachyon", "201"}, {"tachyon", "202"}, {"tachyon", "203"}, {"tachyon", "204"}, {"tachyon", "205"}, {"cerberus", "55"}, {"tachyon", "150"}, {"agent-os", "10"}, {"agent-os", "9"}} {
		var p struct {
			State    string `json:"state"`
			Blockers []struct{ Code string }
			Methods  []string `json:"methods_allowed"`
			Head     string   `json:"head_sha"`
		}
		data(t, do(t, "GET", "/repos/github/hollis-labs/"+c.repo+"/pulls/"+c.num+"/merge-preflight", ""), &p)
		states[p.State] = true
		for _, b := range p.Blockers {
			seen[b.Code] = true
		}
		if len(p.Head) != 40 || len(p.Methods) != 3 || (p.State == "ready") != (len(p.Blockers) == 0) && p.State != "unknown" {
			t.Errorf("%s#%s: %+v", c.repo, c.num, p)
		}
	}
	for _, b := range Blockers {
		if b != "head_moved" && !seen[b] { // head_moved is a merge-time error, not a preflight blocker
			t.Errorf("no fixture for blocker %s", b)
		}
	}
	var p struct{ State string }
	data(t, do(t, "GET", O+"202/merge-preflight", "", "X-Stub-Scenario", "mergeable_pending"), &p)
	states[p.State] = true
	for _, s := range PreflightStates {
		if !states[s] {
			t.Errorf("no preflight state %s", s)
		}
	}
}

func TestMergeGuardedIdempotentAndOperatorOnly(t *testing.T) {
	h := head(t, "202")
	body := `{"method":"squash","head_sha":"` + h + `"}`
	w := wr(t, "PUT", O+"202/merge", body, "k1")
	var m map[string]any
	data(t, w, &m)
	if w.Code != 200 || m["merged"] != true || m["audit_class"] != "lifecycle" {
		t.Fatalf("merge: %d %s", w.Code, w.Body)
	}
	again := wr(t, "PUT", O+"202/merge", body, "k1")
	if again.Header().Get("X-Tachyon-Idempotent-Replay") != "1" || again.Body.String() != w.Body.String() {
		t.Error("same key and body must replay")
	}
	if c := errCodeOf(t, wr(t, "PUT", O+"202/merge", `{"method":"merge","head_sha":"`+h+`"}`, "k1")); c != "idempotency_conflict" {
		t.Errorf("same key, different body: %s", c)
	}
	if w := do(t, "PUT", O+"202/merge", body); w.Code != 400 || errCodeOf(t, w) != "idempotency_key_required" {
		t.Error("key required")
	}
	if w := wr(t, "PUT", O+"202/merge", body, "k2", "Authorization", "Bearer x"); w.Code != 403 || errCodeOf(t, w) != "requester_cannot_approve" {
		t.Errorf("agents get no forge writes: %d", w.Code)
	}
	if w := wr(t, "PUT", O+"202/merge", body, "k3", "X-Stub-Scenario", "not_elevated"); w.Code != 403 || errCodeOf(t, w) != "operator_access_required" {
		t.Errorf("merge needs operator-token: %d", w.Code)
	}
	if w := wr(t, "PUT", O+"202/merge", `{"method":"merge","head_sha":"`+strings.Repeat("0", 40)+`"}`, "k4"); w.Code != 409 || errCodeOf(t, w) != "head_moved" || !strings.Contains(w.Body.String(), h) {
		t.Errorf("head moved carries the current head: %d %s", w.Code, w.Body)
	}
	h201 := head(t, "201")
	w = wr(t, "PUT", O+"201/merge", `{"method":"squash","head_sha":"`+h201+`"}`, "k5")
	if w.Code != 409 || errCodeOf(t, w) != "merge_blocked" || !strings.Contains(w.Body.String(), `"conflicts"`) || !strings.Contains(w.Body.String(), `"checks_failing"`) {
		t.Errorf("blocked: %d %s", w.Code, w.Body)
	}
	for name, b := range map[string]string{"method": `{"method":"fast","head_sha":"` + h + `"}`, "sha": `{"method":"merge","head_sha":"abc"}`, "unknown": `{"method":"merge","head_sha":"` + h + `","x":1}`} {
		if w := wr(t, "PUT", O+"202/merge", b, "kv-"+name); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

func TestReviewValidateAndSubmit(t *testing.T) {
	h := head(t, "201")
	var v struct {
		Moved    bool `json:"head_moved"`
		Comments []struct {
			Index  int
			Status string
		}
	}
	good := `{"path":"internal/server/server.go","line":11,"side":"RIGHT","body":"x"}`
	badLine := `{"path":"internal/server/server.go","line":999,"side":"RIGHT","body":"y"}`
	data(t, do(t, "POST", O+"201/reviews/validate", `{"head_sha":"`+h+`","comments":[`+good+`,`+badLine+`]}`), &v)
	if v.Moved || v.Comments[0].Status != "ok" || v.Comments[1].Status != "invalid_line" {
		t.Errorf("validate: %+v", v)
	}
	old := strings.Repeat("0", 40)
	data(t, do(t, "POST", O+"201/reviews/validate", `{"head_sha":"`+old+`","comments":[`+good+`]}`), &v)
	if !v.Moved || v.Comments[0].Status != "outdated" {
		t.Errorf("outdated: %+v", v)
	}
	w := wr(t, "POST", O+"201/reviews", `{"verdict":"request_changes","body":"fix","head_sha":"`+h+`","comments":[`+good+`]}`, "r1")
	if w.Code != 201 || !strings.Contains(w.Body.String(), "changes_requested") {
		t.Errorf("review: %d %s", w.Code, w.Body)
	}
	if w := wr(t, "POST", O+"201/reviews", `{"verdict":"approve","head_sha":"`+old+`"}`, "r2"); w.Code != 409 || errCodeOf(t, w) != "head_moved" {
		t.Errorf("head moved: %d", w.Code)
	}
	for name, b := range map[string]string{"verdict": `{"verdict":"lgtm","head_sha":"` + h + `"}`, "empty": `{"verdict":"comment","head_sha":"` + h + `"}`, "sha": `{"verdict":"approve","head_sha":"x"}`} {
		if w := wr(t, "POST", O+"201/reviews", b, "rv-"+name); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

func TestCommentsPinTheHeadForInline(t *testing.T) {
	h := head(t, "201")
	if w := wr(t, "POST", O+"201/comments", `{"body":"hello"}`, "c1"); w.Code != 201 {
		t.Errorf("issue comment: %d", w.Code)
	}
	inline := func(commit string) string {
		return `{"body":"nit","path":"internal/server/server.go","line":11,"side":"RIGHT","commit_id":"` + commit + `"}`
	}
	if w := wr(t, "POST", O+"201/comments", inline(h), "c2"); w.Code != 201 {
		t.Errorf("inline: %d %s", w.Code, w.Body)
	}
	if w := wr(t, "POST", O+"201/comments", inline(strings.Repeat("0", 40)), "c3"); w.Code != 409 || errCodeOf(t, w) != "head_moved" {
		t.Errorf("stale inline: %d", w.Code)
	}
	for name, b := range map[string]string{"empty": `{"body":" "}`, "partial": `{"body":"x","path":"a"}`, "side": `{"body":"x","path":"a","line":1,"side":"MIDDLE","commit_id":"` + h + `"}`} {
		if w := wr(t, "POST", O+"201/comments", b, "cv-"+name); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if w := wr(t, "POST", O+"201/comments", `{"body":"`+strings.Repeat("x", MaxCommentBytes+1)+`"}`, "c9"); w.Code != 400 {
		t.Errorf("oversized: %d", w.Code)
	}
}

func TestAssignAgentStatesReplayAndErrors(t *testing.T) {
	type resp struct {
		Link     map[string]any `json:"link"`
		Replayed bool           `json:"replayed"`
		Reminded bool           `json:"reminded"`
	}
	var r resp
	post := func(pr, body, key string, hdr ...string) *httptest.ResponseRecorder {
		return wr(t, "POST", "/repos/github/hollis-labs/tachyon/pulls/"+pr+"/assign-agent", body, key, hdr...)
	}
	data(t, post("202", `{"agent_id":"builder-reviewer","task":"review"}`, "a1"), &r)
	if r.Replayed || r.Link["status"] != "sent" || r.Link["dedupe_key"] != "pr-assign:github:hollis-labs/tachyon#202:builder-reviewer:review" || r.Link["agent_urn"] != "msg://agent/agent-mux/builder-reviewer" {
		t.Errorf("sent: %+v", r)
	}
	data(t, post("203", `{"agent_id":"builder-reviewer","task":"review"}`, "a2"), &r)
	if !r.Replayed || r.Link["status"] != "sent" {
		t.Errorf("replayed: %+v", r)
	}
	data(t, post("203", `{"agent_id":"builder-reviewer","task":"review","force":true}`, "a3"), &r)
	if !r.Reminded || r.Link["reminders"].(float64) != 1 {
		t.Errorf("reminder: %+v", r)
	}
	data(t, post("202", `{"agent_id":"builder-reviewer","task":"review"}`, "a4", "X-Stub-Scenario", "assign_failed"), &r)
	if r.Link["status"] != "failed" || r.Link["forge_assigned"] != true || r.Link["error"] == "" || r.Link["message_id"] != "" {
		t.Errorf("failed resume state: %+v", r.Link)
	}
	data(t, post("202", `{"agent_id":"planner","task":"review"}`, "a5", "X-Stub-Scenario", "assign_no_login"), &r)
	if r.Link["forge_assigned"] != false || r.Link["status"] != "sent" {
		t.Errorf("no forge login is not an error: %+v", r.Link)
	}
	data(t, post("204", `{"agent_id":"builder-reviewer","task":"fix_checks","checks":["go"]}`, "a6"), &r)
	if r.Link["task"] != "fix_checks" || r.Link["pr_key"] != "github:hollis-labs/tachyon#204" {
		t.Errorf("fix_checks: %+v", r.Link)
	}
	for body, want := range map[string]struct {
		pr   string
		code int
		err  string
	}{
		`{"agent_id":"ghost","task":"review"}`:         {"202", 404, "agent_not_found"},
		`{"agent_id":"offline-agent","task":"review"}`: {"202", 422, "agent_not_addressable"},
		`{"agent_id":"planner","task":"review"}`:       {"150", 409, "pr_not_open"},
		`{"agent_id":"planner","task":"dance"}`:        {"202", 400, "invalid_args"},
	} {
		if w := post(want.pr, body, "e-"+body); w.Code != want.code || errCodeOf(t, w) != want.err {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}

func TestTriageStateOpsAndBulk(t *testing.T) {
	var rows []map[string]any
	data(t, do(t, "GET", StatePrefix, ""), &rows)
	if len(rows) != 3 {
		t.Fatalf("all: %d", len(rows))
	}
	data(t, do(t, "GET", StatePrefix+"?prefix=pr-assign:", ""), &rows)
	if len(rows) != 1 {
		t.Errorf("prefix: %d", len(rows))
	}
	var s map[string]any
	data(t, do(t, "PUT", StatePrefix+"/pr-inbox:github:hollis-labs%2Ftachyon%23201", `{"type":"snooze","preset":"4h","note":"later"}`), &s)
	if s["status"] != "snoozed" || s["snoozeUntil"] == nil || s["note"] != "later" {
		t.Errorf("snooze: %v", s)
	}
	for name, b := range map[string]string{"type": `{"type":"nuke"}`, "past": `{"type":"snooze","until":"2020-01-01T00:00:00Z"}`, "preset": `{"type":"snooze","preset":"9y"}`, "unknown": `{"type":"read","x":1}`} {
		if w := do(t, "PUT", StatePrefix+"/pr-inbox:github:hollis-labs%2Ftachyon%23201", b); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if w := do(t, "PUT", StatePrefix+"/other:thing", `{"type":"read"}`); w.Code != 400 {
		t.Errorf("foreign key prefix: %d", w.Code)
	}
	if w := do(t, "PUT", StatePrefix+"/pr-inbox:x", `{"type":"read","expected_version":7}`); w.Code != 409 {
		t.Errorf("version: %d", w.Code)
	}
	var b struct {
		Results []struct {
			Key string
			Ok  bool
		}
	}
	data(t, do(t, "POST", StatePrefix+"/bulk", `{"ops":[{"key":"pr-inbox:a","type":"dismiss"},{"key":"pr-inbox:b","type":"snooze","preset":"1h"},{"key":"bad","type":"read"}]}`), &b)
	if len(b.Results) != 3 || !b.Results[0].Ok || !b.Results[1].Ok || b.Results[2].Ok {
		t.Errorf("bulk: %+v", b)
	}
	if do(t, "POST", StatePrefix+"/bulk", `{"ops":[]}`).Code != 400 {
		t.Error("empty bulk")
	}
}

func TestDraftsAuditAndAssigneesAndOpenAPI(t *testing.T) {
	if w := do(t, "GET", O+"201/review-draft", ""); w.Code != 200 {
		t.Error("draft read")
	}
	if w := do(t, "GET", O+"202/review-draft", ""); w.Code != 404 {
		t.Error("no draft")
	}
	if w := do(t, "PUT", O+"201/review-draft", `{"head_sha":"`+head(t, "201")+`","draft":{"body":"wip"}}`); w.Code != 200 {
		t.Errorf("draft save: %d", w.Code)
	}
	if w := do(t, "DELETE", O+"201/review-draft", ""); w.Code != 200 {
		t.Error("draft delete")
	}
	var a struct {
		Entries []map[string]any `json:"entries"`
	}
	data(t, do(t, "GET", "/audit?limit=2", ""), &a)
	if len(a.Entries) != 2 || a.Entries[0]["audit_class"] != "lifecycle" || strings.Contains(fmtJSON(a.Entries), "\"body\"") {
		t.Errorf("audit: %v", a.Entries)
	}
	if do(t, "GET", "/audit?limit=0", "").Code != 400 {
		t.Error("audit limit")
	}
	if w := wr(t, "PUT", O+"202/assignees", `{"assignees":["octo-dev"]}`, "as1"); w.Code != 200 {
		t.Errorf("assignees: %d", w.Code)
	}
	if w := wr(t, "PUT", O+"202/assignees", `{"assignees":[]}`, "as2"); w.Code != 400 {
		t.Error("empty assignees")
	}
	if w := do(t, "GET", "/openapi.yaml", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "openapi: 3.0.3") {
		t.Error("openapi")
	}
	if w := do(t, "GET", "/nope", ""); w.Code != 404 && w.Code != 405 {
		t.Errorf("unknown route: %d", w.Code)
	}
}

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestStreamReplayResumeAndResync(t *testing.T) {
	ids := func(body string) (out []string) {
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "id: ") {
				out = append(out, l[4:])
			}
		}
		return
	}
	w := do(t, "GET", "/stream?once=1", "")
	if got := ids(w.Body.String()); len(got) != 7 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("replay: %v", got)
	}
	for _, typ := range StreamEvents[:4] {
		if !strings.Contains(w.Body.String(), `"type":"`+typ+`"`) {
			t.Errorf("script lacks %s", typ)
		}
	}
	if got := ids(do(t, "GET", "/stream?once=1", "", "Last-Event-ID", "5").Body.String()); len(got) != 2 || got[0] != "6" {
		t.Errorf("resume: %v", got)
	}
	if b := do(t, "GET", "/stream?once=1&after=99", "").Body.String(); !strings.Contains(b, `"type":"resync"`) || strings.Contains(b, "pr.updated") {
		t.Errorf("past the ring: %s", b)
	}
	if b := do(t, "GET", "/stream?once=1", "", "X-Stub-Scenario", "gap").Body.String(); !strings.Contains(b, `"type":"resync"`) {
		t.Error("gap scenario")
	}
	if do(t, "GET", "/stream?once=1&after=x", "").Code != 400 {
		t.Error("bad after")
	}
}

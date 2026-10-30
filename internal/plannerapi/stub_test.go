package plannerapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://x"+Prefix+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	return w
}

func obj(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	return m
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=])`)
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
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEveryRouteIsMounted(t *testing.T) {
	Stub{}.Handler() // panics on a conflicting pattern
	for _, r := range Routes {
		if !strings.Contains(r, " /") {
			t.Errorf("bad route %q", r)
		}
	}
}

func TestThemesFixtureCoversMomentumLabelsAndExtras(t *testing.T) {
	m := obj(t, do("GET", "/stub-fixtures/themes", ""))
	seen := map[string]bool{}
	for _, it := range m["items"].([]any) {
		o := it.(map[string]any)
		seen[o["momentum"].(map[string]any)["label"].(string)] = true
		for _, k := range []string{"area", "horizon", "pinned", "assignees", "need_you", "relations_count"} {
			if _, ok := o[k]; !ok {
				t.Errorf("%v lacks %s", o["id"], k)
			}
		}
	}
	for _, l := range MomentumLabels {
		if !seen[l] {
			t.Errorf("no theme with momentum %s", l)
		}
	}
}

func TestTimelineMilestonesEventKindsAndFacets(t *testing.T) {
	states, kinds := map[string]bool{}, map[string]bool{}
	for _, id := range []string{ThemeRising, ThemeStalled, "theme-empty"} {
		m := obj(t, do("GET", "/themes/"+id+"/timeline", ""))
		for _, ms := range m["milestones"].([]any) {
			states[ms.(map[string]any)["state"].(string)] = true
		}
		for _, e := range m["events"].([]any) {
			kinds[e.(map[string]any)["kind"].(string)] = true
		}
	}
	for _, s := range MilestoneStates {
		if !states[s] {
			t.Errorf("no milestone in state %s", s)
		}
	}
	for _, k := range EventKinds {
		if !kinds[k] {
			t.Errorf("no timeline event of kind %s", k)
		}
	}
	m := obj(t, do("GET", "/themes/"+ThemeRising+"/timeline?facet=prs", ""))
	if ev := m["events"].([]any); len(ev) != 1 {
		t.Errorf("prs facet: %d events", len(ev))
	}
	m = obj(t, do("GET", "/themes/"+ThemeRising+"/timeline?limit=2", ""))
	if m["next_before_seq"] == nil || len(m["events"].([]any)) != 2 {
		t.Errorf("paging: %v", m["next_before_seq"])
	}
	m2 := obj(t, do("GET", "/themes/"+ThemeRising+"/timeline?limit=2&before_seq="+jsonNum(m["next_before_seq"]), ""))
	if len(m2["events"].([]any)) == 0 {
		t.Error("second page empty")
	}
	if w := do("GET", "/themes/"+ThemeRising+"/timeline?facet=nope", ""); w.Code != 400 {
		t.Errorf("bad facet: %d", w.Code)
	}
	if w := do("GET", "/themes/"+ThemeUnknown+"/timeline", ""); w.Code != 404 {
		t.Errorf("unknown theme: %d", w.Code)
	}
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestTransitionRules(t *testing.T) {
	tr := func(task, body string, hdr ...string) *httptest.ResponseRecorder {
		return do("POST", "/tasks/"+task+"/transition", body, hdr...)
	}
	if w := tr("CW-20261030-0202", `{"to":"review","expected_status":"doing"}`); w.Code != 200 || obj(t, w)["undo"] == nil {
		t.Errorf("move: %d %s", w.Code, w.Body)
	}
	if w := tr("CW-20261030-0202", `{"to":"doing","expected_status":"doing"}`); obj(t, w)["noop"] != true {
		t.Errorf("same status is not a no-op: %s", w.Body)
	}
	if w := tr("CW-20261030-0202", `{"to":"blocked","expected_status":"doing"}`); w.Code != 400 {
		t.Errorf("blocked without reason: %d", w.Code)
	}
	if w := tr("CW-20261030-0202", `{"to":"blocked","expected_status":"doing","reason":"waiting"}`); w.Code != 200 {
		t.Errorf("blocked with reason: %d", w.Code)
	}
	w := tr(TaskBlocked, `{"to":"doing","expected_status":"todo"}`)
	if w.Code != 409 || obj(t, w)["code"] != "conflict" {
		t.Errorf("conflict: %d %s", w.Code, w.Body)
	}
	if w := tr("CW-20261030-0202", `{"to":"doing","expected_status":"todo"}`, "X-Stub-Scenario", "conflict"); w.Code != 409 {
		t.Errorf("scenario conflict: %d", w.Code)
	}
	for _, bad := range []string{`{"to":"nope","expected_status":"todo"}`, `{"to":"done","expected_status":"todo","x":1}`, `[]`, ``} {
		if w := tr("CW-20261030-0202", bad); w.Code != 400 {
			t.Errorf("%q: %d", bad, w.Code)
		}
	}
	if w := tr("nope", `{"to":"done","expected_status":"todo"}`); w.Code != 404 {
		t.Errorf("bad task id: %d", w.Code)
	}
}

func TestRelationRules(t *testing.T) {
	rel := func(from, body string, hdr ...string) int {
		return do("POST", "/"+from+"/x/relations", body, hdr...).Code
	}
	cases := []struct {
		from, body string
		want       int
	}{
		{"themes", `{"kind":"needs","to":{"db":"themes","id":"y"},"expected_revision":3}`, 200},
		{"themes", `{"kind":"delivers","to":{"db":"roadmap","id":"y"},"expected_revision":3}`, 200},
		{"themes", `{"kind":"delivers","to":{"db":"risks","id":"y"},"expected_revision":3}`, 422},
		{"themes", `{"kind":"mitigates","to":{"db":"risks","id":"y"},"expected_revision":3}`, 200},
		{"ideas", `{"kind":"mitigates","to":{"db":"risks","id":"y"},"expected_revision":3}`, 422},
		{"themes", `{"kind":"needs","to":{"db":"risks","id":"y"},"expected_revision":3}`, 422},
		{"themes", `{"kind":"relates","to":{"db":"themes","id":"x"},"expected_revision":3}`, 422},
		{"themes", `{"kind":"relates","to":{"db":"nope","id":"y"},"expected_revision":3}`, 400},
		{"themes", `{"kind":"relates","to":{"db":"ideas","id":"y"}}`, 400},
		{"nope", `{"kind":"relates","to":{"db":"ideas","id":"y"},"expected_revision":3}`, 404},
	}
	for _, c := range cases {
		if got := rel(c.from, c.body); got != c.want {
			t.Errorf("%s %s: %d want %d", c.from, c.body, got, c.want)
		}
	}
	w := do("POST", "/themes/x/relations", cases[0].body, "X-Stub-Scenario", "cycle")
	if w.Code != 422 || obj(t, w)["code"] != "cycle" {
		t.Errorf("cycle: %d %s", w.Code, w.Body)
	}
	if w := do("DELETE", "/themes/x/relations", cases[0].body); w.Code != 200 {
		t.Errorf("delete: %d", w.Code)
	}
	if w := do("GET", "/themes/x/links", ""); w.Code != 200 || obj(t, w)["external"] == nil {
		t.Errorf("links: %d", w.Code)
	}
}

func TestCommentWithStatus(t *testing.T) {
	ok := `{"text":"Approving. @tachyon_pm","status":"approved","pin":true,"expected_revision":15}`
	if w := do("POST", "/themes/theme-agents-console/comments", ok); w.Code != 200 || obj(t, w)["applied"] == nil {
		t.Errorf("comment: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"text":"","expected_revision":1}`, `{"text":"x"}`, `{"text":"x","pin":"yes","expected_revision":1}`, `{"text":"x","kind":"note","expected_revision":1}`, `{"text":"` + strings.Repeat("a", MaxCommentBytes+1) + `","expected_revision":1}`} {
		if w := do("POST", "/themes/a/comments", bad); w.Code != 400 {
			t.Errorf("%.40q: %d", bad, w.Code)
		}
	}
	if w := do("POST", "/risks/a/comments", `{"text":"x","status":"approved","expected_revision":1}`); w.Code != 422 {
		t.Errorf("approved on a risk: %d", w.Code)
	}
}

func TestAssignmentsAndWork(t *testing.T) {
	post := func(b string) int { return do("POST", "/assignments", b).Code }
	if c := post(`{"target":{"db":"themes","id":"a"},"agent_id":"tachyon_pm"}`); c != 201 {
		t.Errorf("planner target: %d", c)
	}
	if c := post(`{"target":{"kind":"torque.task","ref":"CW-20261030-0202"},"team_id":"t"}`); c != 201 {
		t.Errorf("torque target: %d", c)
	}
	for _, bad := range []string{`{"target":{"db":"themes","id":"a"}}`, `{"target":{"db":"themes","id":"a"},"agent_id":"a","team_id":"b"}`, `{"agent_id":"a"}`, `{"target":{"kind":"torque.task","ref":"x"},"agent_id":"a"}`} {
		if c := post(bad); c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
	var l struct{ Items []map[string]any }
	if err := json.Unmarshal(do("GET", "/assignments?state=active&agent_id=tachyon_pm", "").Body.Bytes(), &l); err != nil || len(l.Items) != 1 {
		t.Errorf("filter: %v %v", err, l.Items)
	}
	if err := json.Unmarshal(do("GET", "/assignments?target=CW-20261030-0202", "").Body.Bytes(), &l); err != nil || len(l.Items) != 1 {
		t.Errorf("target filter: %v", l.Items)
	}
	if w := do("POST", "/assignments/asg-0001/complete", ""); w.Code != 200 || obj(t, w)["state"] != "completed" {
		t.Errorf("complete: %d", w.Code)
	}
	if w := do("POST", "/assignments/asg-0004/cancel", ""); w.Code != 409 {
		t.Errorf("end twice: %d", w.Code)
	}
	if w := do("POST", "/assignments/asg-9999/cancel", ""); w.Code != 404 {
		t.Errorf("unknown: %d", w.Code)
	}
	why := map[string]bool{}
	for _, it := range obj(t, do("GET", "/agents/tachyon_pm/work", ""))["items"].([]any) {
		why[it.(map[string]any)["why"].(string)] = true
	}
	for _, w := range WorkWhy {
		if !why[w] {
			t.Errorf("no work item with why %s", w)
		}
	}
	if n := len(obj(t, do("GET", "/agents/tachyon_idle/work", ""))["items"].([]any)); n != 0 {
		t.Errorf("idle agent has %d items", n)
	}
}

func TestMetaTaskContextAndReads(t *testing.T) {
	if w := do("PUT", "/tasks/CW-20261030-0203/meta", `{"done_summary":"Shipped.","pr_cites":[{"owner":"o","repo":"r","number":1}]}`); w.Code != 200 {
		t.Errorf("meta: %d %s", w.Code, w.Body)
	}
	if w := do("PUT", "/tasks/CW-20261030-0203/meta", `{"pr_cites":[{"owner":"o"}]}`); w.Code != 400 {
		t.Errorf("bad cite: %d", w.Code)
	}
	if w := do("PUT", "/tasks/CW-20261030-0203/meta", `{"done_summary":"`+strings.Repeat("a", MaxSummaryChars+1)+`"}`); w.Code != 400 {
		t.Errorf("long summary: %d", w.Code)
	}
	if obj(t, do("GET", "/task-context?task=CW-20261030-0202", ""))["theme"] == nil {
		t.Error("task-context has no theme")
	}
	if obj(t, do("GET", "/task-context?task=CW-20261030-0999", ""))["theme"] != nil {
		t.Error("loose task has a theme")
	}
	if w := do("GET", "/task-context", ""); w.Code != 400 {
		t.Errorf("missing task: %d", w.Code)
	}
	lanes := obj(t, do("GET", "/themes/"+ThemeRising+"/tasks", ""))["lanes"].(map[string]any)
	for _, l := range Lanes {
		if _, ok := lanes[l]; !ok {
			t.Errorf("no %s lane", l)
		}
	}
	if m := obj(t, do("GET", "/momentum", "", "X-Stub-Scenario", "degraded")); m["ingest"].(map[string]any)["degraded"] != true {
		t.Error("degraded momentum")
	}
	if n := obj(t, do("GET", "/need-you", ""))["by_reason"].(map[string]any); len(n) != len(NeedYouReasons) {
		t.Errorf("by_reason: %v", n)
	}
	if w := do("POST", "/ideas/i/promote", `{"to_db":"themes","expected_revision":2}`); w.Code != 200 {
		t.Errorf("promote: %d", w.Code)
	}
	if w := do("POST", "/ideas/i/promote", `{"to_db":"risks","expected_revision":2}`); w.Code != 400 {
		t.Errorf("promote to risks: %d", w.Code)
	}
	if w := do("GET", "/need-you?x=1", ""); w.Code != 400 {
		t.Errorf("unknown param: %d", w.Code)
	}
	if w := do("GET", "/need-you", "", "X-Stub-Status", "502"); w.Code != 502 {
		t.Errorf("forced status: %d", w.Code)
	}
	if w := do("GET", "/stub-fixtures/errors", ""); w.Code != 404 {
		t.Errorf("errors fixture: %d", w.Code)
	}
	if w := do("GET", "/stub-fixtures/nope", ""); w.Code != 404 {
		t.Errorf("unknown fixture: %d", w.Code)
	}
}

func TestStreamReplayAndResync(t *testing.T) {
	all := do("GET", "/stub/stream", "").Body.String()
	for _, e := range StreamEvents {
		if e != "resync" && !strings.Contains(all, "event: "+e+"\n") {
			t.Errorf("script lacks %s", e)
		}
	}
	part := do("GET", "/stub/stream", "", "Last-Event-ID", "104").Body.String()
	if strings.Contains(part, "id: 104") || !strings.Contains(part, "id: 105") {
		t.Errorf("replay after 104: %q", part)
	}
	if got := do("GET", "/stub/stream", "", "Last-Event-ID", "5").Body.String(); !strings.Contains(got, "event: resync") {
		t.Errorf("beyond ring: %q", got)
	}
	if w := do("GET", "/stub/stream", ""); w.Header().Get("Content-Type") != "text/event-stream" || w.Code != http.StatusOK {
		t.Error("stream headers")
	}
}

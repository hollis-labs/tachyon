package builderapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

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

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
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
		if strings.Contains(string(b), "@sha:") {
			t.Errorf("%s has an unresolved sha placeholder", p)
		}
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatal("no fixtures", err)
	}
}

func TestReadRoutesServeFinalShapes(t *testing.T) {
	paths := []string{"/summary", "/agents", "/agents?cursor=c2", "/agents/builder-reviewer", "/agents/builder-reviewer/resolved",
		"/agents/builder-reviewer/used_by", "/agents/builder-reviewer/checks", "/agents/builder-reviewer/history",
		"/launches", "/launches?live=1", "/launches/builder-reviewer-tachyon", "/changes", "/changes?state=open", "/openapi.yaml",
		"/api/plugins/console/v1/registry/projects", "/api/plugins/console/v1/registry/projects/tachyon"}
	for _, k := range LibraryKinds {
		paths = append(paths, "/library/"+k)
	}
	for _, p := range paths {
		if w := do(t, "GET", p, ""); w.Code != 200 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/agents/nope", "/agents/nope/used_by", "/launches/nope", "/changes/nope", "/library/skills/nope",
		"/api/plugins/console/v1/registry/projects/nope", "/nope"} {
		if w := do(t, "GET", p, ""); w.Code != 404 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/agents?state=bogus", "/agents?cursor=zzz", "/library/bogus", "/changes?state=bogus"} {
		if w := do(t, "GET", p, ""); w.Code != 400 {
			t.Errorf("GET %s: %d, want 400", p, w.Code)
		}
	}
	if do(t, "GET", "/summary", "", "X-Stub-Status", "503").Code != 503 {
		t.Error("X-Stub-Status not honoured")
	}
	if w := do(t, "GET", "/summary", "", "X-Stub-Scenario", "catalog_down"); !strings.Contains(w.Body.String(), `"unreachable"`) {
		t.Error("catalog_down scenario not served")
	}
}

type agentRow = map[string]any

func allAgents(t *testing.T) []agentRow {
	t.Helper()
	var out []agentRow
	for _, p := range []string{"/agents", "/agents?cursor=c2"} {
		var d struct {
			Agents []agentRow `json:"agents"`
		}
		decode(t, do(t, "GET", p, ""), &d)
		out = append(out, d.Agents...)
	}
	return out
}

func TestAgentFixturesCoverStatesAndAgreeWithEveryOtherFixture(t *testing.T) {
	agents := allAgents(t)
	states, owned := map[string]int{}, 0
	for _, a := range agents {
		states[a["state"].(string)]++
		id := a["id"].(string)
		if a["owned_draft"] == true {
			owned++
			if a["sha"] != nil || a["layer"] != nil {
				t.Errorf("%s: an owned draft has no file", id)
			}
			for _, sub := range []string{"/resolved", "/checks", "/history"} {
				if w := do(t, "GET", "/agents/"+id+sub, ""); w.Code != 404 || !strings.Contains(w.Body.String(), "not_catalog_agent") {
					t.Errorf("%s%s: %d %s", id, sub, w.Code, w.Body)
				}
			}
			continue
		}
		for _, sub := range []string{"", "/resolved", "/used_by", "/checks", "/history"} {
			if w := do(t, "GET", "/agents/"+id+sub, ""); w.Code != 200 {
				t.Errorf("GET /agents/%s%s: %d", id, sub, w.Code)
			}
		}
		if (a["state"] == AgentActive) != (a["enabled"] == true) && a["state"] != AgentActive {
			// disabled and retired agents are not enabled
			if a["enabled"] == true {
				t.Errorf("%s: %v but enabled", id, a["state"])
			}
		}
	}
	for _, s := range []string{AgentActive, AgentDisabled, AgentRetired} {
		if states[s] == 0 {
			t.Errorf("no %s agent fixture", s)
		}
	}
	if owned == 0 {
		t.Error("no owned_draft fixture")
	}
	var sum struct {
		Agents struct {
			Total, Active, Disabled, Retired int
		} `json:"agents"`
	}
	decode(t, do(t, "GET", "/summary", ""), &sum)
	if sum.Agents.Total != len(agents) || sum.Agents.Active != states[AgentActive] || sum.Agents.Disabled != states[AgentDisabled] || sum.Agents.Retired != states[AgentRetired] {
		t.Errorf("summary %+v disagrees with the list %v", sum.Agents, states)
	}
	// launch_count on a row equals the launches that name the agent.
	var ls struct {
		Launches []map[string]any `json:"launches"`
	}
	decode(t, do(t, "GET", "/launches", ""), &ls)
	per, live := map[string]int{}, 0
	for _, l := range ls.Launches {
		per[l["agent"].(string)]++
		if l["live_count"].(float64) > 0 {
			live++
		}
	}
	for _, a := range agents {
		if int(a["launch_count"].(float64)) != per[a["id"].(string)] {
			t.Errorf("%v launch_count %v, launches say %d", a["id"], a["launch_count"], per[a["id"].(string)])
		}
	}
	var s2 struct {
		Launches struct{ Total, Live int } `json:"launches"`
	}
	decode(t, do(t, "GET", "/summary", ""), &s2)
	if s2.Launches.Total != len(ls.Launches) || s2.Launches.Live != live {
		t.Errorf("summary launches %+v, list says %d/%d live", s2.Launches, len(ls.Launches), live)
	}
}

func TestAgentEditorModelShowsPartsAndDrift(t *testing.T) {
	var d struct {
		Prompt struct {
			Body  string `json:"body"`
			Parts []any  `json:"parts"`
		} `json:"prompt"`
		Parts []struct {
			Kind    string `json:"kind"`
			Drifted bool   `json:"drifted"`
		} `json:"parts"`
	}
	decode(t, do(t, "GET", "/agents/builder-reviewer", ""), &d)
	if !strings.Contains(d.Prompt.Body, MarkerOpen+" review-etiquette@3 -->") || !strings.Contains(d.Prompt.Body, MarkerClose+" -->") || len(d.Prompt.Parts) != 1 {
		t.Errorf("compiled prompt part missing from body: %q", d.Prompt.Body)
	}
	drift := map[string]bool{}
	for _, p := range d.Parts {
		drift[p.Kind] = p.Drifted
	}
	if drift["prompt"] || !drift["policy"] {
		t.Errorf("want a clean prompt part and a drifted policy part, got %v", drift)
	}
}

func TestResolvedChecksAndHistoryCoverTheirVariants(t *testing.T) {
	by, status, withDiff, withoutDiff := map[string]bool{}, map[string]bool{}, false, false
	for _, a := range allAgents(t) {
		if a["owned_draft"] == true {
			continue
		}
		id := a["id"].(string)
		var r struct {
			ResolvedBy string `json:"resolved_by"`
			Tokens     int    `json:"prompt_tokens_est"`
			Parts      []struct{ Bytes, TokensEst int }
			Estimated  bool `json:"prompt_tokens_estimated"`
		}
		decode(t, do(t, "GET", "/agents/"+id+"/resolved", ""), &r)
		by[r.ResolvedBy] = true
		sum := 0
		for _, p := range r.Parts {
			sum += p.TokensEst
		}
		if !r.Estimated || r.Tokens < 0 {
			t.Errorf("%s: token estimate must be labelled an estimate", id)
		}
		var c struct {
			Checks  []struct{ Status string } `json:"checks"`
			Summary map[string]int            `json:"summary"`
		}
		decode(t, do(t, "GET", "/agents/"+id+"/checks", ""), &c)
		n := map[string]int{}
		for _, k := range c.Checks {
			status[k.Status] = true
			n[k.Status]++
		}
		if n["ok"] != c.Summary["ok"] || n["warn"] != c.Summary["warn"] || n["fail"] != c.Summary["fail"] {
			t.Errorf("%s: checks summary %v disagrees with %v", id, c.Summary, n)
		}
		var h struct {
			Entries []struct {
				Diff *string `json:"diff"`
			} `json:"entries"`
		}
		decode(t, do(t, "GET", "/agents/"+id+"/history", ""), &h)
		for _, e := range h.Entries {
			if e.Diff != nil {
				withDiff = true
			} else {
				withoutDiff = true
			}
		}
	}
	if !by[ResolvedByTether] || !by[ResolvedByApprox] {
		t.Errorf("resolved_by variants: %v", by)
	}
	if !status[CheckOK] || !status[CheckWarn] || !status[CheckFail] {
		t.Errorf("check statuses: %v", status)
	}
	if !withDiff || !withoutDiff {
		t.Errorf("history must show entries with and without a diff (%v/%v)", withDiff, withoutDiff)
	}
}

// Environment values are write-only: a launch carries key names and a count, nothing else.
func TestLaunchesCarryEnvKeysOnly(t *testing.T) {
	like := regexp.MustCompile(`(?i)KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL|AUTH`)
	var d struct {
		Launches []struct {
			ID  string         `json:"id"`
			Env map[string]any `json:"env"`
		} `json:"launches"`
	}
	decode(t, do(t, "GET", "/launches", ""), &d)
	withKeys := false
	for _, l := range d.Launches {
		if len(l.Env) != 2 || l.Env["keys"] == nil || l.Env["secret_like_count"] == nil {
			t.Errorf("%s: env must be exactly keys and secret_like_count, got %v", l.ID, l.Env)
			continue
		}
		n := 0
		for _, k := range l.Env["keys"].([]any) {
			withKeys = true
			if strings.ContainsAny(k.(string), "=: ") {
				t.Errorf("%s: env key %q looks like a key=value pair", l.ID, k)
			}
			if like.MatchString(k.(string)) {
				n++
			}
		}
		if n != int(l.Env["secret_like_count"].(float64)) {
			t.Errorf("%s: secret_like_count %v, names say %d", l.ID, l.Env["secret_like_count"], n)
		}
	}
	if !withKeys {
		t.Error("no launch fixture lists env keys")
	}
}

func TestLaunchActionNeverAcceptsEnv(t *testing.T) {
	if w := do(t, "POST", "/launches/builder-reviewer-tachyon/launch", `{"request_id":"r1"}`); w.Code != 201 || !strings.Contains(w.Body.String(), "agents_url") {
		t.Errorf("launch: %d %s", w.Code, w.Body)
	}
	for name, c := range map[string]struct {
		path, body string
		hdr        []string
		want       int
		code       string
	}{
		"env override":       {"/launches/builder-reviewer-tachyon/launch", `{"request_id":"r","env":{"A":"b"}}`, nil, 400, "invalid"},
		"no request id":      {"/launches/builder-reviewer-tachyon/launch", `{}`, nil, 400, "invalid"},
		"disabled":           {"/launches/nightly-triage-agent-os/launch", `{"request_id":"r"}`, nil, 409, "agent_disabled"},
		"worker unavailable": {"/launches/builder-reviewer-tachyon/launch", `{"request_id":"r"}`, []string{"X-Stub-Scenario", "worker_unavailable"}, 409, "worker_unavailable"},
		"unknown":            {"/launches/nope/launch", `{"request_id":"r"}`, nil, 404, "not_found"},
	} {
		w := do(t, "POST", c.path, c.body, c.hdr...)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestLibraryCoversEveryKindAndIsReadOnlyWhereTetherIs(t *testing.T) {
	var sum struct {
		Library map[string]int `json:"library"`
	}
	decode(t, do(t, "GET", "/summary", ""), &sum)
	for _, k := range LibraryKinds {
		var d struct {
			Items []map[string]any `json:"items"`
		}
		decode(t, do(t, "GET", "/library/"+k, ""), &d)
		if len(d.Items) != sum.Library[k] {
			t.Errorf("%s: summary says %d, list has %d", k, sum.Library[k], len(d.Items))
		}
		for _, it := range d.Items {
			id := it["id"].(string)
			for _, sub := range []string{"", "/used_by", "/history"} {
				if w := do(t, "GET", "/library/"+k+"/"+id+sub, ""); w.Code != 200 {
					t.Errorf("GET /library/%s/%s%s: %d", k, id, sub, w.Code)
				}
			}
			if it["kind"] != k {
				t.Errorf("%s/%s: kind %v", k, id, it["kind"])
			}
			if k == LibToolServers {
				if it["editable"] != false || it["read_only"] != true || it["scopes"] != nil {
					t.Errorf("tool server %s must be read-only with scopes not reported: %v", id, it)
				}
			}
			var ub struct {
				Counts map[string]int `json:"counts"`
			}
			decode(t, do(t, "GET", "/library/"+k+"/"+id+"/used_by", ""), &ub)
			if int(it["used_by_count"].(float64)) != ub.Counts["agents"] {
				t.Errorf("%s/%s: used_by_count %v, used_by says %d", k, id, it["used_by_count"], ub.Counts["agents"])
			}
		}
	}
	var tools struct {
		Items []map[string]any `json:"items"`
	}
	decode(t, do(t, "GET", "/library/tool_servers", ""), &tools)
	var sawNull bool
	for _, it := range tools.Items {
		sawNull = sawNull || it["tool_count"] == nil
	}
	if !sawNull {
		t.Error("a down tool server must report tool_count null, not 0")
	}
	var q struct {
		Items []any `json:"items"`
	}
	decode(t, do(t, "GET", "/library/skills?q=commit", ""), &q)
	if len(q.Items) != 1 {
		t.Errorf("skills q=commit: %d", len(q.Items))
	}
}

func TestRegistryProjectsComposedAndUnlinked(t *testing.T) {
	var d struct {
		Projects []struct {
			ID   string
			Link struct{ State string }
		}
	}
	decode(t, do(t, "GET", RegistryPrefix+"/projects", ""), &d)
	st := map[string]bool{}
	for _, p := range d.Projects {
		st[p.Link.State] = true
		var det struct {
			Link    struct{ State string }
			Catalog map[string]any
		}
		decode(t, do(t, "GET", RegistryPrefix+"/projects/"+p.ID, ""), &det)
		if det.Link.State != p.Link.State || (det.Link.State == "unlinked") != (det.Catalog == nil) {
			t.Errorf("%s: link %q with catalog %v", p.ID, det.Link.State, det.Catalog)
		}
	}
	if !st["linked"] || !st["unlinked"] {
		t.Errorf("link states: %v", st)
	}
	var sum struct {
		Projects struct{ Total int } `json:"projects"`
	}
	decode(t, do(t, "GET", "/summary", ""), &sum)
	if sum.Projects.Total != len(d.Projects) {
		t.Errorf("summary projects %d, list %d", sum.Projects.Total, len(d.Projects))
	}
}

func TestRegistryPatchAllowlist(t *testing.T) {
	p := RegistryPrefix + "/projects/tachyon"
	w := do(t, "PATCH", p, `{"description":"New","tags":["a"],"guidelines":"g","status":"paused"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"New"`) {
		t.Errorf("patch: %d %s", w.Code, w.Body)
	}
	for name, body := range map[string]string{
		"unknown field":   `{"repo_root":"/x"}`,
		"mcp servers":     `{"mcp_servers":["a"]}`,
		"null":            `{"description":null}`,
		"empty":           `{}`,
		"bad status":      `{"status":"gone"}`,
		"tags not a list": `{"tags":"a"}`,
		"blank name":      `{"display_name":"  "}`,
		"not json":        `nope`,
	} {
		if w := do(t, "PATCH", p, body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if do(t, "PATCH", RegistryPrefix+"/projects/nope", `{"description":"x"}`).Code != 404 {
		t.Error("unknown project must be 404")
	}
}

func TestEveryChangeStateHasAFixtureAndTheStateMachineHolds(t *testing.T) {
	var all struct {
		Changes []struct {
			ID      string `json:"change_id"`
			State   string
			Plan    map[string]any
			Result  map[string]any
			Failure map[string]any
			Stale   map[string]any
			Diff    string
			Handoff map[string]any
		}
	}
	decode(t, do(t, "GET", "/changes", ""), &all)
	have, steps := map[string]bool{}, map[string]bool{}
	for _, c := range all.Changes {
		have[c.State] = true
		switch c.State {
		case ChangeAwaitingApproval, ChangeApproved, ChangeApplying, ChangeApplied:
			// a handoff change only hints the plan id, so it has no expiry
			if c.Plan == nil || c.Plan["plan_id"] == nil || c.Plan["expires_at"] == nil && c.Handoff == nil {
				t.Errorf("%s: %s needs a plan with an expiry", c.ID, c.State)
			}
			if c.State == ChangeAwaitingApproval && !strings.HasPrefix(c.Plan["approve_command"].(string), "tether catalog approve plan_") {
				t.Errorf("%s: approve_command %v", c.ID, c.Plan["approve_command"])
			}
		}
		if c.State == ChangeApplied && (c.Result == nil || c.Result["backup_id"] == nil || c.Result["reloaded"] == nil) {
			t.Errorf("%s: applied without a result", c.ID)
		}
		if c.State == ChangeFailed {
			if c.Failure == nil {
				t.Errorf("%s: failed without a failure", c.ID)
			} else {
				steps[c.Failure["step"].(string)] = true
			}
		}
		if (c.State == ChangeStale) != (c.Stale != nil) {
			t.Errorf("%s: stale payload only on stale changes", c.ID)
		}
		if c.State == ChangeStale && (c.Stale["mine_diff"] == "" || c.Stale["theirs_diff"] == "") {
			t.Errorf("%s: stale needs both diffs", c.ID)
		}
		if strings.Contains(c.Diff, "\n+") && regexp.MustCompile(`(?i)(token|secret|key|password)\w*:\s*[^<\s{]`).MatchString(c.Diff) {
			t.Errorf("%s: diff shows an unmasked secret-looking value", c.ID)
		}
	}
	for _, s := range ChangeStates {
		if !have[s] {
			t.Errorf("no %s change fixture", s)
		}
	}
	for _, s := range []string{StepValidate, StepPlan, StepApply} {
		if !steps[s] {
			t.Errorf("no failed(%s) fixture", s)
		}
	}
	var open struct{ Changes []struct{ State string } }
	decode(t, do(t, "GET", "/changes?state=open", ""), &open)
	for _, c := range open.Changes {
		if !Open(c.State) {
			t.Errorf("state=open returned %s", c.State)
		}
	}
	var sum struct {
		OpenChanges int `json:"open_changes"`
	}
	decode(t, do(t, "GET", "/summary", ""), &sum)
	if sum.OpenChanges != 3 {
		t.Errorf("summary open_changes %d", sum.OpenChanges)
	}
}

func op(kind, id, sha, typ, path, value string) string {
	v := ""
	if value != "" {
		v = `,"value":` + value
	}
	if path != "" {
		path = `,"path":"` + path + `"`
	}
	return `{"target":{"kind":"` + kind + `","id":"` + id + `"},"expected_sha":"` + sha + `","op":{"type":"` + typ + `"` + path + v + `}}`
}

func create(ops ...string) string {
	return `{"request_id":"r1","note":"n","ops":[` + strings.Join(ops, ",") + `]}`
}

func TestChangeCreateValidates(t *testing.T) {
	good := create(op("agent", "builder-reviewer", "abc", OpSet, "runtime.model", `"claude-opus-5-5"`))
	var c struct {
		ID, State  string
		Privileged []any
		RequestID  string `json:"request_id"`
	}
	w := do(t, "POST", "/changes", good, "X-Stub-Scenario", "direct")
	decode(t, w, &c)
	if w.Code != 201 || c.State != ChangeDraft || len(c.Privileged) != 0 || c.RequestID != "r1" {
		t.Errorf("create: %d %s", w.Code, w.Body)
	}
	w = do(t, "POST", "/changes", create(op("agent", "release-captain", "abc", OpSet, "policy.permission_mode", `"acceptEdits"`)))
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"privileged": [`) && !strings.Contains(w.Body.String(), `"privileged":[`) || strings.Contains(w.Body.String(), `"privileged":[]`) {
		t.Errorf("a policy edit must come back privileged: %d %s", w.Code, w.Body)
	}
	long := strings.Repeat("a", MaxPromptBytes+10)
	for name, c := range map[string]struct {
		body string
		hdr  []string
		want int
		code string
	}{
		"no request id":      {`{"ops":[]}`, nil, 400, "invalid"},
		"no ops":             {`{"request_id":"r","ops":[]}`, nil, 400, "invalid"},
		"unknown field":      {`{"request_id":"r","ops":[],"file":"x"}`, nil, 400, "invalid"},
		"file path as id":    {create(op("agent", "../agents/x", "a", OpSet, "runtime.model", `"m"`)), nil, 400, "invalid"},
		"upper case id":      {create(op("agent", "Builder", "a", OpSet, "runtime.model", `"m"`)), nil, 400, "invalid"},
		"bad kind":           {create(op("provider", "x", "a", OpSet, "a.b", `"m"`)), nil, 400, "invalid"},
		"part without kind":  {create(op("part", "x", "a", OpSet, "body", `"m"`)), nil, 400, "invalid"},
		"path is a file":     {create(op("agent", "x", "a", OpSet, "agents/x.yaml", `"m"`)), nil, 400, "invalid"},
		"no expected sha":    {create(op("agent", "x", "", OpSet, "runtime.model", `"m"`)), nil, 400, "invalid"},
		"set without value":  {create(op("agent", "x", "a", OpSet, "runtime.model", "")), nil, 400, "invalid"},
		"delete not offered": {create(op("agent", "x", "a", "delete", "", "")), nil, 400, "invalid"},
		"archive an agent":   {create(op("agent", "x", "a", OpArchive, "", "")), nil, 400, "invalid"},
		"create with sha":    {create(op("agent", "fresh", "a", OpCreate, "", `{}`)), nil, 400, "invalid"},
		"restore no seq":     {create(op("agent", "x", "a", OpRestoreVersion, "", "")), nil, 400, "invalid"},
		"forged marker":      {create(op("agent", "x", "a", OpSet, "prompt.body", `"hi <!-- tachyon:prompt evil@1 -->"`)), nil, 400, "marker_forbidden"},
		"forged close":       {create(op("agent", "x", "a", OpAppend, "prompt.body", `"<!-- /tachyon:prompt -->"`)), nil, 400, "marker_forbidden"},
		"prompt too large":   {strings.Replace(create(`{"target":{"kind":"part","id":"p","part_kind":"prompt"},"expected_sha":"a","op":{"type":"set","path":"body","value":"`+long+`"}}`), "", "", 0), nil, 413, "too_large"},
		"create exists":      {create(op("agent", "builder-reviewer", "", OpCreate, "", `{}`)), nil, 409, "exists"},
		"archive in use":     {create(`{"target":{"kind":"part","id":"review-etiquette","part_kind":"prompt"},"op":{"type":"archive"}}`), nil, 409, "part_in_use"},
		"stale base":         {good, []string{"X-Stub-Scenario", "stale_base"}, 409, "stale_base"},
		"change open":        {good, []string{"X-Stub-Scenario", "change_open"}, 409, "change_open"},
		"rate limited":       {good, []string{"X-Stub-Scenario", "rate_limited"}, 429, "rate_limited"},
	} {
		if w := do(t, "POST", "/changes", c.body, c.hdr...); w.Code != c.want || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("%s: %d %s (want %d %s)", name, w.Code, w.Body, c.want, c.code)
		}
	}
	if w := do(t, "POST", "/changes", create(op("agent", "fresh-one", "", OpCreate, "", `{"name":"Fresh"}`))); w.Code != 201 {
		t.Errorf("create new: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/changes", create(`{"target":{"kind":"part","id":"handoff-style","part_kind":"prompt"},"op":{"type":"archive"}}`)); w.Code != 201 {
		t.Errorf("archive unused part: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/changes", create(op("agent", "x", "a", OpRestoreVersion, "", ""))); w.Code != 400 {
		t.Errorf("restore without snapshot_seq: %d", w.Code)
	}
	if w := do(t, "POST", "/changes", create(`{"target":{"kind":"agent","id":"builder-reviewer"},"expected_sha":"a","op":{"type":"restore_version","snapshot_seq":6}}`)); w.Code != 201 {
		t.Errorf("restore: %d %s", w.Code, w.Body)
	}
}

func TestSubmitCancelRearmFollowTheStateMachine(t *testing.T) {
	if w := do(t, "POST", "/changes/chg_draft/submit", `{}`, "X-Stub-Scenario", "direct"); w.Code != 200 || !strings.Contains(w.Body.String(), `"awaiting_approval"`) || !strings.Contains(w.Body.String(), `"chg_draft"`) {
		t.Errorf("submit: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/changes/chg_draft_privileged/submit", `{}`); w.Code != 400 || !strings.Contains(w.Body.String(), "ack_required") {
		t.Errorf("privileged submit without ack: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/changes/chg_draft_privileged/submit", `{"ack_privileged":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"ack_privileged":true`) {
		t.Errorf("privileged submit: %d %s", w.Code, w.Body)
	}
	for _, id := range []string{"chg_applied", "chg_awaiting", "chg_stale"} {
		if w := do(t, "POST", "/changes/"+id+"/submit", `{}`); w.Code != 409 {
			t.Errorf("submit %s: %d, want 409", id, w.Code)
		}
	}
	for id, want := range map[string]int{"chg_draft": 200, "chg_awaiting": 200, "chg_stale": 200, "chg_approved": 409, "chg_applying": 409, "chg_applied": 409, "chg_cancelled": 409} {
		if w := do(t, "POST", "/changes/"+id+"/cancel", ""); w.Code != want {
			t.Errorf("cancel %s: %d, want %d", id, w.Code, want)
		}
	}
	for id, want := range map[string]int{"chg_awaiting": 200, "chg_expired": 200, "chg_draft": 409, "chg_applied": 409} {
		if w := do(t, "POST", "/changes/"+id+"/rearm", ""); w.Code != want {
			t.Errorf("rearm %s: %d, want %d", id, w.Code, want)
		}
	}
	var c struct {
		Plan struct {
			PlanID    string `json:"plan_id"`
			ExpiresAt int64  `json:"expires_at"`
		}
	}
	decode(t, do(t, "POST", "/changes/chg_expired/rearm", ""), &c)
	if c.Plan.PlanID != "plan_7Q3K9" || c.Plan.ExpiresAt <= 0 {
		t.Errorf("rearm must keep the plan id and give a fresh expiry: %+v", c.Plan)
	}
	if do(t, "POST", "/changes/nope/cancel", "").Code != 404 {
		t.Error("unknown change must be 404")
	}
}

func TestStreamReplaysAfterLastEventID(t *testing.T) {
	all := do(t, "GET", "/stream?once=1", "")
	if all.Code != 200 || !strings.HasPrefix(all.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", all.Code, all.Header().Get("Content-Type"))
	}
	for _, typ := range []string{EventChange, EventCatalog, EventSummary} {
		if !strings.Contains(all.Body.String(), `"type":"`+typ+`"`) {
			t.Errorf("stream has no %s event", typ)
		}
	}
	body := do(t, "GET", "/stream?once=1", "", "Last-Event-ID", "204").Body.String()
	if strings.Contains(body, "id: 204") || strings.Contains(body, "id: 201") || !strings.Contains(body, "id: 205") {
		t.Errorf("resume after 204:\n%s", body)
	}
	if strings.Contains(body, `"type":"summary"`) {
		t.Error("live snapshots must not replay on resume")
	}
	if do(t, "GET", "/stream?once=1", "", "Last-Event-ID", "x").Code != http.StatusBadRequest {
		t.Error("bad Last-Event-ID must be 400")
	}
	if !IDed(EventChange) || !IDed(EventCatalog) || IDed(EventSummary) {
		t.Error("change and catalog replay; summary is a snapshot")
	}
}

func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	doc := string(OpenAPI())
	for _, p := range []string{"/builder/summary:", "/builder/agents:", "/builder/agents/{id}:", "/builder/agents/{id}/resolved:", "/builder/agents/{id}/used_by:",
		"/builder/agents/{id}/checks:", "/builder/agents/{id}/history:", "/builder/launches:", "/builder/launches/{id}:", "/builder/launches/{id}/launch:",
		"/builder/library/{kind}:", "/builder/library/{kind}/{id}:", "/builder/library/{kind}/{id}/used_by:", "/builder/library/{kind}/{id}/history:",
		"/builder/changes:", "/builder/changes/{id}:", "/builder/changes/{id}/submit:", "/builder/changes/{id}/cancel:", "/builder/changes/{id}/rearm:",
		"/builder/stream:", "/registry/projects:", "/registry/projects/{id}:"} {
		if !strings.Contains(doc, "\n  "+p) {
			t.Errorf("openapi.yaml does not document %s", p)
		}
	}
}

func TestHandoffModeCapabilitiesBundleAndDetection(t *testing.T) {
	var caps struct {
		Changes struct {
			Mode    string
			Handoff []string
		}
	}
	for scenario, want := range map[string]string{"": ModeHandoff, "direct": ModeDirect, "unavailable": ModeUnavailable} {
		decode(t, do(t, "GET", "/capabilities", "", "X-Stub-Scenario", scenario), &caps)
		if caps.Changes.Mode != want {
			t.Errorf("capabilities %q: %v", scenario, caps)
		}
	}
	decode(t, do(t, "GET", "/capabilities", ""), &caps)
	if len(caps.Changes.Handoff) != 1 || caps.Changes.Handoff[0] != HandoffCopy {
		t.Errorf("handoff modes: %v", caps.Changes.Handoff)
	}
	// An unprivileged change is awaiting_handoff at once; a privileged one waits for the ack.
	var c struct {
		State   string
		Plan    any
		Handoff map[string]any
		Targets []map[string]any
	}
	w := do(t, "POST", "/changes", create(op("agent", "builder-reviewer", "abc", OpSet, "runtime.model", `"claude-opus-5-5"`)))
	decode(t, w, &c)
	if w.Code != 201 || c.State != ChangeAwaitingHandoff || c.Plan != nil || c.Handoff["mode"] != HandoffCopy || c.Targets[0]["new_sha"] == nil {
		t.Errorf("handoff create: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/changes", create(op("agent", "release-captain", "abc", OpSet, "policy.permission_mode", `"acceptEdits"`))); !strings.Contains(w.Body.String(), `"state":"draft"`) {
		t.Errorf("privileged handoff change stays a draft: %s", w.Body)
	}
	if w := do(t, "POST", "/changes/chg_draft/submit", `{}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"awaiting_handoff"`) {
		t.Errorf("handoff submit: %d %s", w.Code, w.Body)
	}
	var b struct {
		Name  string
		Sha   string `json:"sha256"`
		Files []struct{ Path, Content string }
	}
	w = do(t, "GET", "/changes/chg_awaiting_handoff/bundle", "")
	decode(t, w, &b)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || len(b.Files) != 1 || b.Files[0].Content == "" || b.Sha == "" {
		t.Errorf("bundle: %d %s", w.Code, w.Body)
	}
	for _, id := range []string{"chg_draft", "chg_applied", "chg_stale"} {
		if w := do(t, "GET", "/changes/"+id+"/bundle", ""); w.Code != 409 {
			t.Errorf("bundle of %s: %d", id, w.Code)
		}
	}
	if do(t, "GET", "/changes/nope/bundle", "").Code != 404 {
		t.Error("bundle of an unknown change must be 404")
	}
	w = do(t, "POST", "/changes/chg_awaiting_handoff/mark_planned", `{"plan_id":"plan_ABC123"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"approve_command":"tether catalog approve plan_ABC123"`) || !strings.Contains(w.Body.String(), `"awaiting_approval"`) {
		t.Errorf("mark_planned: %d %s", w.Code, w.Body)
	}
	if do(t, "POST", "/changes/chg_awaiting_handoff/mark_planned", `{"plan_id":"a b"}`).Code != 400 || do(t, "POST", "/changes/chg_applied/mark_planned", `{}`).Code != 409 {
		t.Error("mark_planned must refuse a bad id and a finished change")
	}
	for scenario, want := range map[string]string{"": ChangeAwaitingHandoff, "detected": ChangeApplied, "stale": ChangeStale} {
		var got struct{ State string }
		decode(t, do(t, "POST", "/changes/chg_awaiting_handoff/check", `{}`, "X-Stub-Scenario", scenario), &got)
		if got.State != want {
			t.Errorf("check %q: %s want %s", scenario, got.State, want)
		}
	}
	var det struct {
		Result map[string]any
	}
	decode(t, do(t, "POST", "/changes/chg_awaiting_handoff/check", `{}`, "X-Stub-Scenario", "detected"), &det)
	if det.Result["detected"] != true || det.Result["activation"] != "" {
		t.Errorf("a detected apply carries no activation text: %v", det.Result)
	}
	if w := do(t, "POST", "/changes/chg_awaiting_handoff/cancel", `{}`); w.Code != 200 {
		t.Errorf("cancel awaiting_handoff: %d", w.Code)
	}
}

func TestHandoffCommandsAreServerBuilt(t *testing.T) {
	type cmd struct{ Step, Label, Kind, Command, Tool string }
	var c struct{ Handoff struct{ Commands []cmd } }
	decode(t, do(t, "GET", "/changes/chg_awaiting_handoff", ""), &c)
	steps := []string{}
	for _, k := range c.Handoff.Commands {
		steps = append(steps, k.Step)
		switch k.Kind {
		case "shell": // the CLI only has show and approve
			if !strings.HasPrefix(k.Command, "tether catalog show ") && !strings.HasPrefix(k.Command, "tether catalog approve ") || k.Tool != "" {
				t.Errorf("shell step: %+v", k)
			}
		case "action": // plan and apply are MCP tools, never a shell command
			if k.Command != "" || !strings.HasPrefix(k.Tool, "tether_catalog_") {
				t.Errorf("action step: %+v", k)
			}
		default:
			t.Errorf("kind: %+v", k)
		}
		if k.Label == "" || strings.Contains(k.Command, "catalog plan") || strings.Contains(k.Command, "catalog apply") {
			t.Errorf("step: %+v", k)
		}
	}
	if strings.Join(steps, ",") != "plan,show,approve,apply" || !strings.Contains(c.Handoff.Commands[2].Command, "<plan_id>") {
		t.Errorf("commands before a plan id: %+v", c.Handoff.Commands)
	}
	var b struct{ Commands []cmd }
	decode(t, do(t, "GET", "/changes/chg_awaiting_handoff/bundle", ""), &b)
	if len(b.Commands) != 4 {
		t.Errorf("bundle commands: %+v", b.Commands)
	}
	decode(t, do(t, "POST", "/changes/chg_awaiting_handoff/mark_planned", `{"plan_id":"plan_ABC"}`), &c)
	if c.Handoff.Commands[1].Command != "tether catalog show plan_ABC" || c.Handoff.Commands[2].Command != "tether catalog approve plan_ABC" {
		t.Errorf("commands after mark_planned: %+v", c.Handoff.Commands)
	}
}

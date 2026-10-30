package builderchange

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const secret = "sk-supersecretvalue1234567890"

const agentYAML = `# the reviewer
id: reviewer
name: Reviewer
provider: claude
model: claude-sonnet-5-5
extends: base-worker
custom_key: keep-me # unknown keys survive
skills:
  - review
permissions:
  permission_mode: default
  network: none
env:
  API_TOKEN: ` + secret + `
`

type fakeTether struct {
	files    map[string]string
	plans    map[string]*Plan
	bundles  map[string][]File
	validOK  bool
	applyErr error
	rolled   bool
	down     bool
	nplan    int
	applied  []string
}

func newFake() *fakeTether {
	return &fakeTether{files: map[string]string{"agents/reviewer.yaml": agentYAML, "agents/other.yaml": "id: other\nname: Other\n"},
		plans: map[string]*Plan{}, bundles: map[string][]File{}, validOK: true}
}

func (f *fakeTether) ReadFile(_ context.Context, p string) (string, bool, error) {
	if f.down {
		return "", false, errUnreachable
	}
	c, ok := f.files[p]
	return c, ok, nil
}
func (f *fakeTether) Validate(_ context.Context, files []File) (Report, error) {
	if f.down {
		return Report{}, errUnreachable
	}
	r := Report{OK: f.validOK, Results: []Check{{Name: "tether_validate", Status: "ok", Message: "Passed.", Entity: files[0].Path}}}
	if !f.validOK {
		r.Results[0].Status, r.Results[0].Message = "fail", "unknown skill"
	}
	return r, nil
}
func (f *fakeTether) Plan(_ context.Context, name string, files []File) (Plan, error) {
	key := ""
	for _, x := range files {
		key += x.Path + "\x00" + x.Content + "\x00"
	}
	for id, b := range f.bundles { // the same bundle re-arms the same plan
		k := ""
		for _, x := range b {
			k += x.Path + "\x00" + x.Content + "\x00"
		}
		if k == key {
			p := f.plans[id]
			p.ExpiresAt = time.Now().Add(15 * time.Minute)
			p.State = "planned"
			return *p, nil
		}
	}
	f.nplan++
	id := "plan_" + string(rune('a'+f.nplan))
	p := &Plan{ID: id, State: "planned", Digest: "d" + id, ExpiresAt: time.Now().Add(15 * time.Minute), ValidationOK: true, Activation: "restart_required", RunningSessions: []string{"sess_1"}}
	for _, x := range files {
		if strings.Contains(x.Content, "permission_mode: bypassPermissions") {
			p.Privileged = append(p.Privileged, x.Path)
		}
	}
	f.plans[id], f.bundles[id] = p, files
	return *p, nil
}
func (f *fakeTether) GetPlan(_ context.Context, id string) (Plan, error) {
	p, ok := f.plans[id]
	if !ok {
		return Plan{}, &TetherError{Status: 404, Code: "not_found"}
	}
	return *p, nil
}
func (f *fakeTether) Apply(_ context.Context, id string) (ApplyResult, error) {
	if f.applyErr != nil {
		return ApplyResult{}, f.applyErr
	}
	if f.rolled {
		return ApplyResult{RolledBack: true}, nil
	}
	for _, x := range f.bundles[id] {
		f.files[x.Path] = x.Content
	}
	f.plans[id].State = "applied"
	f.applied = append(f.applied, id)
	return ApplyResult{Applied: true, BackupID: "bk_1", Activation: "restart_required", Reloaded: true, RestartRequired: []string{"agents/reviewer.yaml"}}, nil
}

type env struct {
	svc  *Service
	fake *fakeTether
	h    http.Handler
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{fake: newFake(), now: time.Now()}
	idx := &catalogindex.Store{DB: db}
	e.svc = &Service{DB: db, Tether: e.fake, Index: idx, Bus: catalogindex.NewBroker(time.Now), Now: func() time.Time { return e.now }}
	mux := http.NewServeMux()
	h := &HTTP{Service: func() *Service { return e.svc }, Principal: func(*http.Request) string { return "op1" }}
	id := func(n http.Handler) http.Handler { return n }
	h.Mount(mux, id, id)
	e.h = mux
	return e
}

func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://x"+builderapi.Prefix+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func sha(s string) string { return shaOf(s) }

func req(id string, ops ...string) string {
	return `{"request_id":"` + id + `","note":"n","ops":[` + strings.Join(ops, ",") + `]}`
}

func setOp(kind, tid, sha, path, value string) string {
	return `{"target":{"kind":"` + kind + `","id":"` + tid + `"},"expected_sha":"` + sha + `","op":{"type":"set","path":"` + path + `","value":` + value + `}}`
}

func (e *env) create(t *testing.T, id string, ops ...string) Change {
	t.Helper()
	w := e.do("POST", "/changes", req(id, ops...))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var c Change
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	return c
}

func (e *env) get(t *testing.T, id string) Change {
	t.Helper()
	var c Change
	w := e.do("GET", "/changes/"+id, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	return c
}

func (e *env) submit(id string, ack bool) *httptest.ResponseRecorder {
	b := `{}`
	if ack {
		b = `{"ack_privileged":true}`
	}
	return e.do("POST", "/changes/"+id+"/submit", b)
}

func modelOp() string {
	return setOp("agent", "reviewer", sha(agentYAML), "runtime.model", `"claude-opus-5-5"`)
}

func TestPatchKeepsCommentsKeysAndUnknownFields(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	out, privs, err := Apply("agent", "reviewer", "agents/reviewer.yaml", agentYAML, true, []Edit{
		{Type: "set", Path: "runtime.model", Value: raw(`"claude-opus-5-5"`)},
		{Type: "append", Path: "tools_skills.skills", Value: raw(`"lint"`)},
		{Type: "append", Path: "tools_skills.skills", Value: raw(`"review"`)}, // duplicate: no-op
		{Type: "set", Path: "policy.permission_mode", Value: raw(`"acceptEdits"`)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# the reviewer", "custom_key: keep-me # unknown keys survive", "model: claude-opus-5-5", "- lint", "permission_mode: acceptEdits", "API_TOKEN: " + secret} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "- review") != 1 {
		t.Errorf("duplicate append:\n%s", out)
	}
	if !strings.HasPrefix(out, "# the reviewer\nid: reviewer\nname: Reviewer\n") {
		t.Errorf("key order changed:\n%s", out)
	}
	if len(privs) != 1 || privs[0].Field != "policy.permission_mode" || privs[0].Path != "agents/reviewer.yaml" {
		t.Errorf("privileged: %+v", privs)
	}
	out2, _, err := Apply("agent", "reviewer", "p", out, true, []Edit{{Type: "remove", Path: "tools_skills.skills", Value: raw(`"lint"`)}, {Type: "remove", Path: "runtime.extends"}}, nil)
	if err != nil || strings.Contains(out2, "- lint") || strings.Contains(out2, "extends:") {
		t.Fatalf("remove: %v\n%s", err, out2)
	}
}

func TestPatchRefusals(t *testing.T) {
	val := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := map[string]Edit{
		"env path":     {Type: "set", Path: "env.API_TOKEN", Value: val(`"x"`)},
		"env in value": {Type: "set", Path: "identity.description", Value: val(`{"env":"x"}`)},
		"unknown path": {Type: "set", Path: "runtime.nope", Value: val(`"x"`)},
		"file path":    {Type: "set", Path: "../x", Value: val(`"x"`)},
		"id":           {Type: "set", Path: "id", Value: val(`"x"`)},
		"marker":       {Type: "set", Path: "prompt.body", Value: val(`"a <!-- tachyon:prompt x@1 --> b"`)},
		"close marker": {Type: "set", Path: "prompt.body", Value: val(`"<!-- /tachyon:prompt"`)},
		"no value":     {Type: "set", Path: "runtime.model"},
		"unknown op":   {Type: "delete", Path: "runtime.model"},
		"archive":      {Type: "archive"},
	}
	for name, e := range cases {
		if _, _, err := Apply("agent", "reviewer", "p", agentYAML, true, []Edit{e}, nil); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
	big := `"` + strings.Repeat("a", builderapi.MaxPromptBytes+1) + `"`
	if _, _, err := Apply("agent", "reviewer", "p", agentYAML, true, []Edit{{Type: "set", Path: "prompt.body", Value: val(big)}}, nil); err == nil || err.Status != 413 {
		t.Errorf("oversize prompt: %v", err)
	}
	if _, _, err := Apply("agent", "reviewer", "p", agentYAML, true, []Edit{{Type: "create"}}, nil); err == nil || err.Status != 409 {
		t.Errorf("create over existing: %v", err)
	}
	if _, _, err := Apply("agent", "Bad Id", "p", "", false, []Edit{{Type: "create"}}, nil); err == nil {
		t.Error("bad slug")
	}
	out, _, err := Apply("agent", "fresh", "agents/fresh.yaml", "", false, []Edit{{Type: "create"}, {Type: "set", Path: "runtime.provider", Value: val(`"claude"`)}}, nil)
	if err != nil || !strings.Contains(out, "id: fresh") || !strings.Contains(out, "provider: claude") {
		t.Errorf("create: %v\n%s", err, out)
	}
}

func TestRestoreKeepsLiveEnvAndRefusesMasked(t *testing.T) {
	snap, _, _ := catalogindex.MaskYAML(strings.Replace(agentYAML, "claude-sonnet-5-5", "claude-haiku-5-5", 1))
	getSnap := func(int) (string, bool) { return snap, true }
	live := strings.Replace(agentYAML, "API_TOKEN: "+secret, "API_TOKEN: "+secret+"-rotated", 1)
	out, _, err := Apply("agent", "reviewer", "p", live, true, []Edit{{Type: "restore_version", SnapshotSeq: 1}}, getSnap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude-haiku-5-5") || !strings.Contains(out, secret+"-rotated") || strings.Contains(out, "***") {
		t.Fatalf("restore:\n%s", out)
	}
	masked := "id: reviewer\nname: Reviewer\napi_key: \"***\"\n"
	if _, _, err := Apply("agent", "reviewer", "p", "id: reviewer\nname: Reviewer\n", true, []Edit{{Type: "restore_version", SnapshotSeq: 1}}, func(int) (string, bool) { return masked, true }); err == nil || err.Code != "restore_masked" {
		t.Fatalf("masked value with no live counterpart: %v", err)
	}
	if _, _, err := Apply("agent", "reviewer", "p", agentYAML, true, []Edit{{Type: "restore_version", SnapshotSeq: 9}}, func(int) (string, bool) { return "", false }); err == nil || err.Status != 404 {
		t.Fatalf("missing snapshot: %v", err)
	}
}

func TestCreateSubmitApproveApply(t *testing.T) {
	e := setup(t)
	c := e.create(t, "r1", modelOp())
	if c.State != "draft" || c.Plan == nil || c.Plan.PlanID == "" || c.Plan.ApproveCommand != "tether catalog approve "+c.Plan.PlanID {
		t.Fatalf("draft: %+v", c)
	}
	if c.Base["agents/reviewer.yaml"] != sha(agentYAML) || len(c.Targets) != 1 || c.Targets[0].Path != "agents/reviewer.yaml" {
		t.Fatalf("base/targets: %+v", c)
	}
	if !strings.Contains(c.Diff, "-model: claude-sonnet-5-5") || !strings.Contains(c.Diff, "+model: claude-opus-5-5") || strings.Contains(c.Diff, secret) {
		t.Fatalf("diff:\n%s", c.Diff)
	}
	if len(c.Effect.RunningSessions) != 1 || c.Effect.Activation != "restart_required" || len(c.Checks) != 1 || len(c.Privileged) != 0 {
		t.Fatalf("effect/checks: %+v", c)
	}
	// replay by request_id
	w := e.do("POST", "/changes", req("r1", modelOp()))
	var again Change
	_ = json.Unmarshal(w.Body.Bytes(), &again)
	if w.Code != 200 || w.Header().Get("X-Tachyon-Replay") != "1" || again.ChangeID != c.ChangeID {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	// one open change per path
	if w = e.do("POST", "/changes", req("r2", modelOp())); w.Code != 409 || !strings.Contains(w.Body.String(), "change_open") {
		t.Fatalf("second open change: %d %s", w.Code, w.Body)
	}
	if w = e.submit(c.ChangeID, false); w.Code != 200 {
		t.Fatalf("submit: %d %s", w.Code, w.Body)
	}
	if got := e.get(t, c.ChangeID); got.State != "awaiting_approval" {
		t.Fatalf("state: %s", got.State)
	}
	if err := e.svc.Poll(context.Background()); err != nil || e.get(t, c.ChangeID).State != "awaiting_approval" || len(e.fake.applied) != 0 {
		t.Fatalf("unapproved plans must wait: %v", err)
	}
	e.fake.plans[c.Plan.PlanID].State = "approved" // the host runs `tether catalog approve`
	_ = e.svc.Poll(context.Background())
	got := e.get(t, c.ChangeID)
	if got.State != "applied" || got.Result == nil || got.Result.BackupID != "bk_1" || !got.Result.Reloaded || got.Result.AppliedAt == nil || got.Result.Activation != "restart_required" {
		t.Fatalf("applied: %+v %+v", got, got.Result)
	}
	if !strings.Contains(e.fake.files["agents/reviewer.yaml"], "claude-opus-5-5") {
		t.Fatal("the file was not written")
	}
	_ = e.svc.Poll(context.Background())
	if len(e.fake.applied) != 1 {
		t.Fatalf("applied %d times", len(e.fake.applied))
	}
	// the path is free again, and a masked builder snapshot exists
	if w = e.do("POST", "/changes", req("r3", setOp("agent", "reviewer", sha(e.fake.files["agents/reviewer.yaml"]), "runtime.model", `"claude-haiku-5-5"`))); w.Code != 201 {
		t.Fatalf("path should be free after apply: %d %s", w.Code, w.Body)
	}
	snaps, _ := e.svc.Index.Snapshots(context.Background(), "agents/reviewer.yaml", 5)
	if len(snaps) != 1 || snaps[0].Source != "builder" || strings.Contains(snaps[0].Content, secret) || snaps[0].Actor != "op1" {
		t.Fatalf("snapshots: %+v", snaps)
	}
	// no secret anywhere the service persisted or returned
	var n int
	_ = e.svc.DB.Reader.QueryRow(`SELECT COUNT(*) FROM catalog_changes WHERE body LIKE ? OR request LIKE ?`, "%"+secret+"%", "%"+secret+"%").Scan(&n)
	if n != 0 {
		t.Fatal("a secret reached the database")
	}
	if l := e.do("GET", "/changes?state=open", ""); l.Code != 200 || strings.Contains(l.Body.String(), secret) {
		t.Fatalf("list: %d", l.Code)
	}
}

func TestPrivilegedNeedsAck(t *testing.T) {
	e := setup(t)
	c := e.create(t, "p1", setOp("agent", "reviewer", sha(agentYAML), "policy.permission_mode", `"bypassPermissions"`))
	if len(c.Privileged) == 0 {
		t.Fatalf("privileged not reported: %+v", c)
	}
	if w := e.submit(c.ChangeID, false); w.Code != 400 || !strings.Contains(w.Body.String(), "ack_required") {
		t.Fatalf("no ack: %d %s", w.Code, w.Body)
	}
	if w := e.submit(c.ChangeID, true); w.Code != 200 {
		t.Fatalf("ack: %d %s", w.Code, w.Body)
	}
	if !e.get(t, c.ChangeID).AckPrivileged {
		t.Fatal("ack not recorded")
	}
}

func TestStaleBaseAndKeepMine(t *testing.T) {
	e := setup(t)
	if w := e.do("POST", "/changes", req("s0", setOp("agent", "reviewer", "deadbeef", "runtime.model", `"x"`))); w.Code != 409 || !strings.Contains(w.Body.String(), "stale_base") {
		t.Fatalf("wrong expected_sha: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/changes", req("s00", `{"target":{"kind":"agent","id":"reviewer"},"expected_sha":"","op":{"type":"set","path":"runtime.model","value":"x"}}`)); w.Code != 400 {
		t.Fatalf("missing expected_sha: %d", w.Code)
	}
	c := e.create(t, "s1", modelOp())
	e.submit(c.ChangeID, false)
	e.fake.files["agents/reviewer.yaml"] = strings.Replace(agentYAML, "network: none", "network: open", 1) // someone else
	e.fake.plans[c.Plan.PlanID].State = "approved"
	_ = e.svc.Poll(context.Background())
	got := e.get(t, c.ChangeID)
	if got.State != "stale" || got.Stale == nil || len(got.Stale.ChangedPaths) != 1 || got.Stale.TheirsSHA != sha(e.fake.files["agents/reviewer.yaml"]) || got.Stale.MineDiff == "" {
		t.Fatalf("stale: %+v %+v", got.State, got.Stale)
	}
	if len(e.fake.applied) != 0 {
		t.Fatal("a stale change must never apply")
	}
	if w := e.do("POST", "/changes/"+c.ChangeID+"/cancel", ""); w.Code != 200 {
		t.Fatalf("cancel stale: %d", w.Code)
	}
	// keep mine: a new change on the new sha
	if w := e.do("POST", "/changes", req("s2", setOp("agent", "reviewer", got.Stale.TheirsSHA, "runtime.model", `"claude-opus-5-5"`))); w.Code != 201 {
		t.Fatalf("keep mine: %d %s", w.Code, w.Body)
	}
}

func TestValidationFailureKeepsDraftButCannotSubmit(t *testing.T) {
	e := setup(t)
	e.fake.validOK = false
	c := e.create(t, "v1", modelOp())
	if c.Plan != nil || len(c.Checks) != 1 || c.Checks[0].Status != "fail" {
		t.Fatalf("draft: %+v", c)
	}
	if w := e.submit(c.ChangeID, false); w.Code != 409 || !strings.Contains(w.Body.String(), "validation_failed") {
		t.Fatalf("submit: %d %s", w.Code, w.Body)
	}
}

func TestExpiryRearmAndCancel(t *testing.T) {
	e := setup(t)
	c := e.create(t, "x1", modelOp())
	e.submit(c.ChangeID, false)
	e.fake.plans[c.Plan.PlanID].ExpiresAt = time.Now().Add(-time.Minute)
	e.now = time.Now()
	_ = e.svc.Poll(context.Background())
	if got := e.get(t, c.ChangeID); got.State != "expired" {
		t.Fatalf("expired: %s", got.State)
	}
	w := e.do("POST", "/changes/"+c.ChangeID+"/rearm", "")
	var got Change
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.State != "awaiting_approval" || got.Plan.PlanID != c.Plan.PlanID || got.Plan.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("rearm: %d %s", w.Code, w.Body)
	}
	if w = e.do("POST", "/changes/"+c.ChangeID+"/rearm", `{}`); w.Code != 200 {
		t.Fatalf("rearm awaiting: %d", w.Code)
	}
	if w = e.do("POST", "/changes/"+c.ChangeID+"/cancel", ""); w.Code != 200 || e.get(t, c.ChangeID).State != "cancelled" {
		t.Fatalf("cancel: %d", w.Code)
	}
	if w = e.do("POST", "/changes/"+c.ChangeID+"/cancel", ""); w.Code != 409 {
		t.Fatalf("cancel twice: %d", w.Code)
	}
	if w = e.do("POST", "/changes/"+c.ChangeID+"/submit", `{}`); w.Code != 409 {
		t.Fatalf("submit cancelled: %d", w.Code)
	}
}

func TestApplyFailureAndRollback(t *testing.T) {
	e := setup(t)
	e.fake.applyErr = &TetherError{Status: 500, Code: "apply_failed", Message: "reload refused the catalog with " + secret}
	c := e.create(t, "f1", modelOp())
	e.submit(c.ChangeID, false)
	e.fake.plans[c.Plan.PlanID].State = "approved"
	_ = e.svc.Poll(context.Background())
	got := e.get(t, c.ChangeID)
	if got.State != "failed" || got.Failure == nil || got.Failure.Step != "reload" || strings.Contains(got.Failure.Message, secret) {
		t.Fatalf("failed: %+v", got.Failure)
	}
	e2 := setup(t)
	e2.fake.rolled = true
	c = e2.create(t, "f2", modelOp())
	e2.submit(c.ChangeID, false)
	e2.fake.plans[c.Plan.PlanID].State = "approved"
	_ = e2.svc.Poll(context.Background())
	if got = e2.get(t, c.ChangeID); got.State != "rolled_back" {
		t.Fatalf("rolled back: %s", got.State)
	}
	// stuck applying
	e3 := setup(t)
	c = e3.create(t, "f3", modelOp())
	e3.submit(c.ChangeID, false)
	ch, _ := e3.svc.load(context.Background(), c.ChangeID)
	ch.State = "applying"
	e3.svc.save(context.Background(), ch)
	e3.now = e3.now.Add(applyStuck + time.Minute)
	_ = e3.svc.Poll(context.Background())
	if got = e3.get(t, c.ChangeID); got.State != "failed" || got.Failure.Code != "interrupted" {
		t.Fatalf("stuck: %+v", got)
	}
}

func TestRateLimitAndValidation(t *testing.T) {
	e := setup(t)
	for i := 0; i < builderapi.ChangesPerMinute; i++ {
		id := "other" + string(rune('a'+i))
		e.fake.files["agents/"+id+".yaml"] = "id: " + id + "\nname: X\n"
		if w := e.do("POST", "/changes", req("rl"+id, setOp("agent", id, sha(e.fake.files["agents/"+id+".yaml"]), "identity.name", `"Y"`))); w.Code != 201 {
			t.Fatalf("#%d: %d %s", i, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/changes", req("rlx", modelOp())); w.Code != 429 {
		t.Fatalf("rate limit: %d %s", w.Code, w.Body)
	}
	e = setup(t)
	for name, body := range map[string]string{
		"no ops":       req("a"),
		"bad request":  `{"request_id":"has space","ops":[` + modelOp() + `]}`,
		"part target":  req("b", `{"target":{"kind":"part","id":"x","part_kind":"prompt"},"expected_sha":"","op":{"type":"set","path":"body","value":"x"}}`),
		"bad kind":     req("c", `{"target":{"kind":"provider","id":"x"},"expected_sha":"","op":{"type":"set","path":"a","value":"x"}}`),
		"bad id":       req("d", setOp("agent", "../etc", "x", "runtime.model", `"x"`)),
		"unknown":      `{"request_id":"e","ops":[` + modelOp() + `],"x":1}`,
		"not json":     `nope`,
		"no change":    req("f", setOp("agent", "reviewer", sha(agentYAML), "runtime.model", `"claude-sonnet-5-5"`)),
		"missing file": req("g", setOp("agent", "ghost", "x", "runtime.model", `"x"`)),
	} {
		if w := e.do("POST", "/changes", body); w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/changes?state=bogus", ""); w.Code != 400 {
		t.Errorf("state filter: %d", w.Code)
	}
	if w := e.do("GET", "/changes/nope", ""); w.Code != 404 {
		t.Errorf("get unknown: %d", w.Code)
	}
}

func TestTetherDown(t *testing.T) {
	e := setup(t)
	e.fake.down = true
	if w := e.do("POST", "/changes", req("d1", modelOp())); w.Code != 503 || !strings.Contains(w.Body.String(), "tether_unavailable") {
		t.Fatalf("down: %d %s", w.Code, w.Body)
	}
	_ = errors.New
}

func TestCreateNewSkillAndLaunchOps(t *testing.T) {
	e := setup(t)
	e.fake.files["launches/l1.yaml"] = "id: l1\nagent: reviewer\nenabled: true\nenv:\n  K: " + secret + "\n"
	w := e.do("POST", "/changes", req("n1",
		`{"target":{"kind":"skill","id":"new-skill"},"expected_sha":"","op":{"type":"create"}}`,
		`{"target":{"kind":"skill","id":"new-skill"},"expected_sha":"","op":{"type":"set","path":"description","value":"does things"}}`,
		`{"target":{"kind":"skill","id":"new-skill"},"expected_sha":"","op":{"type":"set","path":"body","value":"# Steps\n"}}`,
		setOp("launch", "l1", sha(e.fake.files["launches/l1.yaml"]), "enabled", `false`)))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var c Change
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if len(c.Targets) != 2 || strings.Contains(c.Diff, secret) || !strings.Contains(c.Diff, "+description: does things") || !strings.Contains(c.Diff, "-enabled: true") {
		t.Fatalf("targets/diff: %+v\n%s", c.Targets, c.Diff)
	}
	files := e.fake.bundles[c.Plan.PlanID]
	var skill string
	for _, f := range files {
		if f.Path == "skills/new-skill.md" {
			skill = f.Content
		}
	}
	if !strings.HasPrefix(skill, "---\nid: new-skill\n") || !strings.Contains(skill, "description: does things") || !strings.HasSuffix(skill, "# Steps\n") {
		t.Fatalf("skill:\n%q", skill)
	}
	e.do("POST", "/changes/"+c.ChangeID+"/cancel", "")
	if w = e.do("POST", "/changes", req("n2", `{"target":{"kind":"launch","id":"l1"},"expected_sha":"`+sha(e.fake.files["launches/l1.yaml"])+`","op":{"type":"set","path":"env.K","value":"x"}}`)); w.Code != 400 {
		t.Fatalf("env write: %d %s", w.Code, w.Body)
	}
}

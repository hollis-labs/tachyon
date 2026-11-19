package builderchange

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

const secondID = "second"

// setupHandoff is setup with no control credential: reads work, every Tether write is refused.
func setupHandoff(t *testing.T) *env {
	t.Helper()
	e := setup(t)
	e.svc.Mode = builderapi.ModeHandoff
	e.svc.Tether = ReadOnly(e.fake)
	e.fake.files["agents/"+secondID+".yaml"] = strings.Replace(agentYAML, "id: reviewer", "id: "+secondID, 1)
	return e
}

func secondOp() string {
	return setOp("agent", secondID, sha(strings.Replace(agentYAML, "id: reviewer", "id: "+secondID, 1)), "runtime.model", `"claude-opus-5-5"`)
}

// bundle fetches the bundle and, like the operator running `tether catalog plan/approve/apply`, writes its files.
func (e *env) bundle(t *testing.T, id string) Bundle {
	t.Helper()
	w := e.do("GET", "/changes/"+id+"/bundle", "")
	var b Bundle
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bundle: %d %s", w.Code, w.Body)
	}
	return b
}

func (e *env) applyBundle(b Bundle, only ...string) {
	for _, f := range b.Files {
		if len(only) == 0 || only[0] == f.Path {
			e.fake.files[f.Path] = f.Content
		}
	}
}

func TestHandoffCreateBundleAndAppliedIsDetected(t *testing.T) {
	e := setupHandoff(t)
	c := e.create(t, "h1", modelOp())
	if c.State != builderapi.ChangeAwaitingHandoff || c.Plan != nil || c.Handoff == nil || c.Handoff.Mode != "copy" || c.Handoff.Files != 1 || c.Handoff.SessionAvailable {
		t.Fatalf("created: %+v", c)
	}
	if len(c.Targets) != 1 || c.Targets[0].NewSHA == "" || c.Targets[0].NewSHA == c.Targets[0].BaseSHA || len(e.fake.plans) != 0 {
		t.Fatalf("targets/plans: %+v %d", c.Targets, len(e.fake.plans))
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), secret) {
		t.Fatal("the change body must not carry file content")
	}
	b := e.bundle(t, c.ChangeID)
	if len(b.Files) != 1 || b.Files[0].Path != "agents/reviewer.yaml" || !strings.Contains(b.Files[0].Content, "model: claude-opus-5-5") ||
		!strings.Contains(b.Files[0].Content, "API_TOKEN: "+secret) || b.SHA256 != c.Handoff.BundleSHA256 || b.Name != c.Handoff.Name {
		t.Fatalf("bundle: %+v", b)
	}
	if shaOf(b.Files[0].Content) != c.Targets[0].NewSHA {
		t.Fatal("bundle content must hash to the target's new_sha")
	}
	// the CLI has show and approve only; plan and apply are text for a session that holds catalog.write
	if cm := c.Handoff.Commands; len(cm) != 4 || cm[0].Step != "plan" || cm[0].Kind != "session" || !strings.Contains(cm[0].Command, "tether_catalog_plan") ||
		cm[1].Command != "tether catalog show <plan_id>" || cm[1].Kind != "shell" || cm[2].Command != "tether catalog approve <plan_id>" ||
		cm[3].Step != "apply" || cm[3].Kind != "session" || !strings.Contains(cm[3].Command, "tether_catalog_apply") || len(b.Commands) != 4 {
		t.Fatalf("commands: %+v / %+v", c.Handoff.Commands, b.Commands)
	}
	if c.Handoff.Reason != "contains_env_values" || c.Handoff.SessionAvailable {
		t.Fatalf("a bundle with env values is never for a session: %+v", c.Handoff)
	}
	// nothing planned yet: the poller leaves it alone
	if err := e.svc.Poll(context.Background()); err != nil || e.get(t, c.ChangeID).State != builderapi.ChangeAwaitingHandoff {
		t.Fatalf("poll before apply: %v", err)
	}
	e.applyBundle(b)
	if err := e.svc.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, c.ChangeID)
	if got.State != builderapi.ChangeApplied || got.Result == nil || !got.Result.Detected || got.Result.FilesChanged != 1 || got.Result.Activation != "" || got.Result.AppliedAt == nil {
		t.Fatalf("detected apply: %+v %+v", got, got.Result)
	}
	// the path is free again
	if w := e.do("POST", "/changes", req("h2", setOp("agent", "reviewer", sha(e.fake.files["agents/reviewer.yaml"]), "runtime.model", `"claude-sonnet-5-5"`))); w.Code != 201 {
		t.Fatalf("path not freed: %d %s", w.Code, w.Body)
	}
}

func TestHandoffManyFilesPartialMatchWaitsThenStale(t *testing.T) {
	e := setupHandoff(t)
	c := e.create(t, "n1", modelOp(), secondOp())
	b := e.bundle(t, c.ChangeID)
	if c.State != builderapi.ChangeAwaitingHandoff || len(b.Files) != 2 || c.Handoff.Files != 2 {
		t.Fatalf("two files: %+v %+v", c, b)
	}
	e.applyBundle(b, "agents/reviewer.yaml")
	got, perr := e.svc.Check(context.Background(), c.ChangeID)
	if perr != nil || got.State != builderapi.ChangeAwaitingHandoff {
		t.Fatalf("a partial match must keep waiting: %+v %v", got, perr)
	}
	e.applyBundle(b, "agents/"+secondID+".yaml")
	if err := e.svc.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got = e.get(t, c.ChangeID)
	if got.State != builderapi.ChangeApplied || got.Result.FilesChanged != 2 {
		t.Fatalf("all files applied: %+v", got)
	}

	// one file applied, the other edited by someone else: stale, and only the other file is listed
	e2 := setupHandoff(t)
	c2 := e2.create(t, "n2", modelOp(), secondOp())
	b2 := e2.bundle(t, c2.ChangeID)
	e2.applyBundle(b2, "agents/reviewer.yaml")
	e2.fake.files["agents/"+secondID+".yaml"] += "# edited elsewhere\n"
	_ = e2.svc.Poll(context.Background())
	got = e2.get(t, c2.ChangeID)
	if got.State != builderapi.ChangeStale || got.Stale == nil || len(got.Stale.ChangedPaths) != 1 || got.Stale.ChangedPaths[0] != "agents/"+secondID+".yaml" {
		t.Fatalf("partial then moved: %+v %+v", got.State, got.Stale)
	}
}

func TestHandoffBaseMovedIsStale(t *testing.T) {
	e := setupHandoff(t)
	c := e.create(t, "s1", modelOp())
	e.fake.files["agents/reviewer.yaml"] += "# someone else\n"
	_ = e.svc.Poll(context.Background())
	got := e.get(t, c.ChangeID)
	if got.State != builderapi.ChangeStale || got.Stale == nil || len(got.Stale.ChangedPaths) != 1 {
		t.Fatalf("moved base: %+v", got)
	}
	// the bundle of a stale change is refused
	if w := e.do("GET", "/changes/"+c.ChangeID+"/bundle", ""); w.Code != 409 {
		t.Fatalf("stale bundle: %d", w.Code)
	}
	// and a bundle request on a moved base marks it stale
	e2 := setupHandoff(t)
	c2 := e2.create(t, "s2", modelOp())
	e2.fake.files["agents/reviewer.yaml"] += "# later\n"
	if w := e2.do("GET", "/changes/"+c2.ChangeID+"/bundle", ""); w.Code != 409 || e2.get(t, c2.ChangeID).State != builderapi.ChangeStale {
		t.Fatalf("bundle on a moved base: %d %s", w.Code, w.Body)
	}
}

func TestHandoffMarkPlannedIsOnlyAHint(t *testing.T) {
	e := setupHandoff(t)
	c := e.create(t, "m1", modelOp())
	if w := e.do("POST", "/changes/"+c.ChangeID+"/mark_planned", `{"plan_id":"a b"}`); w.Code != 400 {
		t.Fatalf("bad plan id: %d", w.Code)
	}
	w := e.do("POST", "/changes/"+c.ChangeID+"/mark_planned", `{"plan_id":"plan_7Q3K9"}`)
	var got Change
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.State != builderapi.ChangeAwaitingApproval || got.Plan == nil || got.Plan.ApproveCommand != "tether catalog approve plan_7Q3K9" || got.Plan.ExpiresAt != nil {
		t.Fatalf("mark_planned: %d %s", w.Code, w.Body)
	}
	if got.Handoff.Commands[1].Command != "tether catalog show plan_7Q3K9" || got.Handoff.Commands[2].Command != "tether catalog approve plan_7Q3K9" {
		t.Fatalf("commands carry the plan id once known: %+v", got.Handoff.Commands)
	}
	if !strings.Contains(w.Body.String(), `"expires_at":null`) {
		t.Fatalf("the hint has no expiry: %s", w.Body)
	}
	// the hint is not trusted: nothing in Tether, and the change still waits (no GetPlan on a read-only client)
	_ = e.svc.Poll(context.Background())
	if e.get(t, c.ChangeID).State != builderapi.ChangeAwaitingApproval {
		t.Fatal("hint changed the state")
	}
	e.applyBundle(e.bundle(t, c.ChangeID))
	_ = e.svc.Poll(context.Background())
	if e.get(t, c.ChangeID).State != builderapi.ChangeApplied {
		t.Fatal("applied must be detected from awaiting_approval too")
	}
	if w := e.do("POST", "/changes/"+c.ChangeID+"/mark_planned", `{}`); w.Code != 409 {
		t.Fatalf("mark_planned on an applied change: %d", w.Code)
	}
	// without a hint the change still moves to awaiting_approval
	c2 := e.create(t, "m2", setOp("agent", "other", sha("id: other\nname: Other\n"), "identity.name", `"Other 2"`))
	if c2.Handoff.Reason != "not_configured" {
		t.Fatalf("a bundle without env values has only the not-configured reason: %+v", c2.Handoff)
	}
	if w := e.do("POST", "/changes/"+c2.ChangeID+"/mark_planned", `{}`); w.Code != 200 || e.get(t, c2.ChangeID).State != builderapi.ChangeAwaitingApproval {
		t.Fatalf("no hint: %d %s", w.Code, w.Body)
	}
}

func TestHandoffPrivilegedNeedsAckThenWaits(t *testing.T) {
	e := setupHandoff(t)
	c := e.create(t, "p1", setOp("agent", "reviewer", sha(agentYAML), "policy.permission_mode", `"bypassPermissions"`))
	if c.State != builderapi.ChangeDraft || c.Handoff == nil {
		t.Fatalf("a privileged change waits for its ack: %+v", c)
	}
	if w := e.do("GET", "/changes/"+c.ChangeID+"/bundle", ""); w.Code != 409 {
		t.Fatalf("no bundle before the ack: %d", w.Code)
	}
	if w := e.submit(c.ChangeID, false); w.Code != 400 || !strings.Contains(w.Body.String(), "ack_required") {
		t.Fatalf("no ack: %d %s", w.Code, w.Body)
	}
	if w := e.submit(c.ChangeID, true); w.Code != 200 || e.get(t, c.ChangeID).State != builderapi.ChangeAwaitingHandoff {
		t.Fatalf("ack: %d %s", w.Code, w.Body)
	}
}

func TestHandoffCancelRearmLaunchAndWrites(t *testing.T) {
	e := setupHandoff(t)
	c := e.create(t, "c1", modelOp())
	if w := e.do("POST", "/changes/"+c.ChangeID+"/rearm", ""); w.Code != 409 {
		t.Fatalf("rearm has nothing to re-arm: %d", w.Code)
	}
	if w := e.do("POST", "/changes/"+c.ChangeID+"/cancel", ""); w.Code != 200 || e.get(t, c.ChangeID).State != builderapi.ChangeCancelled {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/changes", req("c2", modelOp())); w.Code != 201 {
		t.Fatalf("path not freed by cancel: %d", w.Code)
	}
	if w := e.do("POST", "/launches/some-launch/launch", `{"request_id":"l1"}`); w.Code != 503 || !strings.Contains(w.Body.String(), "changes_unavailable") {
		t.Fatalf("launch needs the control credential: %d %s", w.Code, w.Body)
	}
	ro := ReadOnly(newFake())
	for name, err := range map[string]error{
		"plan":    func() error { _, err := ro.Plan(context.Background(), "n", nil); return err }(),
		"getplan": func() error { _, err := ro.GetPlan(context.Background(), "p"); return err }(),
		"apply":   func() error { _, err := ro.Apply(context.Background(), "p"); return err }(),
		"session": ro.StartSession(context.Background(), "s"),
	} {
		var te *TetherError
		if !errors.As(err, &te) || te.Code != "no_control_credential" {
			t.Errorf("%s must be refused: %v", name, err)
		}
	}
}

type noValidate struct{ *fakeTether }

func (noValidate) Validate(context.Context, []File) (Report, error) {
	return Report{}, &TetherError{Status: 403, Code: "forbidden"}
}

func TestHandoffValidateRefusalBecomesAWarning(t *testing.T) {
	e := setupHandoff(t)
	e.svc.Tether = ReadOnly(noValidate{e.fake})
	c := e.create(t, "v1", modelOp())
	if c.State != builderapi.ChangeAwaitingHandoff || len(c.Checks) != 1 || c.Checks[0].Status != builderapi.CheckWarn || !strings.Contains(c.Checks[0].Message, "Not validated by Tether") {
		t.Fatalf("validate refused by the read token: %+v", c)
	}
	// a real failing validation still blocks the hand-off
	e3 := setupHandoff(t)
	e3.fake.validOK = false
	c3 := e3.create(t, "v2", modelOp())
	if c3.State != builderapi.ChangeDraft || c3.Handoff != nil {
		t.Fatalf("a failing check keeps the draft and builds no bundle: %+v", c3)
	}
	if w := e3.submit(c3.ChangeID, false); w.Code != 409 {
		t.Fatalf("submit of a failing change: %d", w.Code)
	}
}

func TestCapabilitiesReportTheMode(t *testing.T) {
	e := setupHandoff(t)
	get := func(svc *Service) (string, []any) {
		mux := http.NewServeMux()
		h := &HTTP{Service: func() *Service { return svc }}
		h.MountCapabilities(mux, func(n http.Handler) http.Handler { return n })
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "http://x"+builderapi.Prefix+"/capabilities", nil))
		var v struct {
			Changes struct {
				Mode    string
				Handoff []any
			}
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("capabilities: %d %s", w.Code, w.Body)
		}
		return v.Changes.Mode, v.Changes.Handoff
	}
	if m, h := get(e.svc); m != "handoff" || len(h) != 1 || h[0] != "copy" {
		t.Errorf("handoff: %s %v", m, h)
	}
	e.svc.Mode = ""
	if m, h := get(e.svc); m != "direct" || len(h) != 0 {
		t.Errorf("direct: %s %v", m, h)
	}
	if m, h := get(nil); m != "unavailable" || len(h) != 0 {
		t.Errorf("unavailable: %s %v", m, h)
	}
}

func TestDirectModeIsUnchangedByHandoffFields(t *testing.T) {
	e := setup(t)
	c := e.create(t, "d1", modelOp())
	if c.State != builderapi.ChangeDraft || c.Plan == nil || c.Handoff != nil || c.Plan.ExpiresAt == nil {
		t.Fatalf("direct create: %+v", c)
	}
	if w := e.do("GET", "/changes/"+c.ChangeID+"/bundle", ""); w.Code != 409 {
		t.Fatalf("direct mode has no bundle: %d", w.Code)
	}
	if w := e.do("POST", "/changes/"+c.ChangeID+"/mark_planned", `{}`); w.Code != 409 {
		t.Fatalf("direct mode has no mark_planned: %d", w.Code)
	}
}

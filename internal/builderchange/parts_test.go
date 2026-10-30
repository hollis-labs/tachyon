package builderchange

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/builderparts"
)

func partOp(pk, id, sha, typ, path, value string) string {
	s := `{"target":{"kind":"part","part_kind":"` + pk + `","id":"` + id + `"},"expected_sha":"` + sha + `","op":{"type":"` + typ + `"`
	if path != "" {
		s += `,"path":"` + path + `"`
	}
	if value != "" {
		s += `,"value":` + value
	}
	return s + `}}`
}

func agentAttach(sha, typ, path, value string) string {
	return `{"target":{"kind":"agent","id":"reviewer"},"expected_sha":"` + sha + `","op":{"type":"` + typ + `","path":"` + path + `","value":` + value + `}}`
}

func (e *env) approve(t *testing.T, id string) Change {
	t.Helper()
	c := e.get(t, id)
	e.fake.plans[c.Plan.PlanID].State = "approved"
	_ = e.svc.Poll(context.Background())
	c = e.get(t, id)
	if c.State != "applied" {
		t.Fatalf("state %s %+v", c.State, c.Failure)
	}
	return c
}

func (e *env) part(t *testing.T, kind, id string) builderparts.Part {
	t.Helper()
	p, ok, err := builderparts.Get(context.Background(), e.svc.DB.Reader, kind, id)
	if err != nil || !ok {
		t.Fatalf("part %s/%s: %v %v", kind, id, ok, err)
	}
	return p
}

func (e *env) newPrompt(t *testing.T, rid, id, text string) {
	t.Helper()
	c := e.create(t, rid, partOp("prompt", id, "", "create", "", `{"name":"Style","summary":"house style","body":"`+text+`"}`))
	if c.Plan != nil || len(c.PartEffects.Parts) != 1 {
		t.Fatalf("part-only change should have no plan: %+v", c)
	}
	if w := e.submit(c.ChangeID, false); w.Code != 200 || e.get(t, c.ChangeID).State != "applied" {
		t.Fatalf("submit: %d %s", w.Code, w.Body)
	}
}

func TestPartCreateAttachEditRecompileDetach(t *testing.T) {
	e := setup(t)
	e.newPrompt(t, "r1", "style", "Be terse.")
	if p := e.part(t, "prompt", "style"); p.Revision != 1 || p.Text != "Be terse." {
		t.Fatalf("part: %+v", p)
	}
	// Attach: the marker block lands in the body through a Tether plan; the reference is written on apply.
	c := e.create(t, "r2", agentAttach(sha(agentYAML), "append", "prompt.parts", `{"kind":"prompt","id":"style"}`))
	if c.Plan == nil || !strings.Contains(c.Diff, "tachyon:prompt style@1") {
		t.Fatalf("attach: %+v", c)
	}
	if refs, _ := builderparts.RefsOfPart(context.Background(), e.svc.DB.Reader, "prompt", "style"); len(refs) != 0 {
		t.Fatal("reference written before apply")
	}
	e.submit(c.ChangeID, false)
	e.approve(t, c.ChangeID)
	live := e.fake.files["agents/reviewer.yaml"]
	if !strings.Contains(live, "<!-- tachyon:prompt style@1 -->") || !strings.Contains(live, "custom_key: keep-me") || !strings.Contains(live, secret) {
		t.Fatalf("live:\n%s", live)
	}
	refs, _ := builderparts.RefsOfAgent(context.Background(), e.svc.DB.Reader, "reviewer")
	if len(refs) != 1 || refs[0].Rev != 1 || refs[0].Hash != builderparts.TextHash("Be terse.") {
		t.Fatalf("refs: %+v", refs)
	}
	if builderparts.Drifted(live, refs[0], nil) {
		t.Fatal("fresh block reported as drift")
	}
	// A second agent carries it too, so one edit of the part plans both files in one plan.
	c = e.create(t, "r3", `{"target":{"kind":"agent","id":"other"},"expected_sha":"`+sha(e.fake.files["agents/other.yaml"])+`","op":{"type":"append","path":"prompt.parts","value":{"kind":"prompt","id":"style"}}}`)
	e.submit(c.ChangeID, false)
	e.approve(t, c.ChangeID)
	c = e.create(t, "r4", partOp("prompt", "style", "1", "set", "body", `"Be terse and kind."`))
	if c.Plan == nil || len(c.Targets) != 3 || len(e.fake.bundles[c.Plan.PlanID]) != 2 {
		t.Fatalf("edit should plan every referencing agent in one plan: %+v", c.Targets)
	}
	if p := e.part(t, "prompt", "style"); p.Revision != 1 {
		t.Fatal("part changed before apply")
	}
	e.submit(c.ChangeID, false)
	e.approve(t, c.ChangeID)
	if p := e.part(t, "prompt", "style"); p.Revision != 2 || p.Text != "Be terse and kind." {
		t.Fatalf("part: %+v", p)
	}
	for _, f := range []string{"agents/reviewer.yaml", "agents/other.yaml"} {
		if !strings.Contains(e.fake.files[f], "style@2 -->") || !strings.Contains(e.fake.files[f], "Be terse and kind.") || strings.Contains(e.fake.files[f], "style@1") {
			t.Errorf("%s:\n%s", f, e.fake.files[f])
		}
	}
	revs, _ := builderparts.Revisions(context.Background(), e.svc.DB.Reader, "prompt", "style")
	if len(revs) != 2 || revs[0].Revision != 2 {
		t.Fatalf("revisions: %+v", revs)
	}
	// A hand edit of the block is drift.
	refs, _ = builderparts.RefsOfAgent(context.Background(), e.svc.DB.Reader, "reviewer")
	hand := strings.Replace(e.fake.files["agents/reviewer.yaml"], "Be terse and kind.", "Be rude.", 1)
	if !builderparts.Drifted(hand, refs[0], nil) {
		t.Fatal("hand edit not detected")
	}
	// In use: archive refused. Detach, then archive.
	w := e.do("POST", "/changes", req("r5", partOp("prompt", "style", "2", "archive", "", "")))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "part_in_use") {
		t.Fatalf("archive in use: %d %s", w.Code, w.Body)
	}
	for i, id := range []string{"reviewer", "other"} {
		c = e.create(t, "d"+string(rune('a'+i)), `{"target":{"kind":"agent","id":"`+id+`"},"expected_sha":"`+sha(e.fake.files["agents/"+id+".yaml"])+`","op":{"type":"remove","path":"prompt.parts","value":{"id":"style"}}}`)
		e.submit(c.ChangeID, false)
		e.approve(t, c.ChangeID)
		if strings.Contains(e.fake.files["agents/"+id+".yaml"], "tachyon:prompt") {
			t.Fatalf("block left in %s", id)
		}
	}
	c = e.create(t, "r6", partOp("prompt", "style", "2", "archive", "", ""))
	e.submit(c.ChangeID, false)
	if !e.part(t, "prompt", "style").Archived {
		t.Fatal("not archived")
	}
}

func TestPartRefusals(t *testing.T) {
	e := setup(t)
	e.newPrompt(t, "r1", "style", "Be terse.")
	cases := map[string]string{
		"stale revision": partOp("prompt", "style", "9", "set", "body", `"x"`),
		"no revision":    partOp("prompt", "style", "", "set", "body", `"x"`),
		"marker":         partOp("prompt", "style", "1", "set", "body", `"<!-- tachyon:prompt a@1 -->"`),
		"unknown part":   partOp("prompt", "nope", "1", "set", "body", `"x"`),
		"exists":         partOp("prompt", "style", "", "create", "", `{"name":"x"}`),
		"bad kind":       `{"target":{"kind":"part","part_kind":"skill","id":"x"},"op":{"type":"create"}}`,
		"policy field":   partOp("policy", "nope", "", "create", "", `{"name":"x","bundle":{"env":"x"}}`),
		"bad attach op":  agentAttach(sha(agentYAML), "set", "prompt.parts", `{"id":"style"}`),
		"missing part":   agentAttach(sha(agentYAML), "append", "prompt.parts", `{"id":"ghost"}`),
	}
	for name, op := range cases {
		if w := e.do("POST", "/changes", req("x-"+strings.ReplaceAll(name, " ", "-"), op)); w.Code < 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// No change is refused too.
	if w := e.do("POST", "/changes", req("nc", partOp("prompt", "style", "1", "set", "body", `"Be terse."`))); w.Code != 400 {
		t.Errorf("no_change: %d", w.Code)
	}
}

func TestPolicyPartIsPrivilegedAndCompilesFields(t *testing.T) {
	e := setup(t)
	c := e.create(t, "p1", partOp("policy", "strict", "", "create", "", `{"name":"Strict","summary":"locked down","bundle":{"permission_mode":"plan","network":"none"}}`))
	if len(c.Privileged) != 0 {
		t.Fatalf("create alone sets no field: %+v", c.Privileged)
	}
	e.submit(c.ChangeID, false)
	c = e.create(t, "p2", agentAttach(sha(agentYAML), "set", "policy.part", `"strict"`))
	if len(c.Privileged) == 0 {
		t.Fatalf("attaching a policy must be privileged: %+v", c)
	}
	if w := e.submit(c.ChangeID, false); w.Code != 400 || !strings.Contains(w.Body.String(), "ack_required") {
		t.Fatalf("no ack: %d %s", w.Code, w.Body)
	}
	e.submit(c.ChangeID, true)
	e.approve(t, c.ChangeID)
	live := e.fake.files["agents/reviewer.yaml"]
	if !strings.Contains(live, "permission_mode: plan") || strings.Contains(live, "permission_mode: default") {
		t.Fatalf("live:\n%s", live)
	}
	refs, _ := builderparts.RefsOfAgent(context.Background(), e.svc.DB.Reader, "reviewer")
	p := e.part(t, "policy", "strict")
	if len(refs) != 1 || builderparts.Drifted(live, refs[0], keysOf(p.Bundle)) {
		t.Fatalf("refs %+v", refs)
	}
	if !builderparts.Drifted(strings.Replace(live, "permission_mode: plan", "permission_mode: default", 1), refs[0], keysOf(p.Bundle)) {
		t.Fatal("policy drift not detected")
	}
	// Editing the policy is privileged and recompiles the agent.
	c = e.create(t, "p3", partOp("policy", "strict", "1", "set", "network", `"allow"`))
	if len(c.Privileged) == 0 || c.Plan == nil {
		t.Fatalf("policy edit: %+v", c)
	}
	e.submit(c.ChangeID, true)
	e.approve(t, c.ChangeID)
	if !strings.Contains(e.fake.files["agents/reviewer.yaml"], "network: allow") {
		t.Fatalf("not recompiled:\n%s", e.fake.files["agents/reviewer.yaml"])
	}
}

func TestPartStaleWhenPartMovesUnderAnOpenChange(t *testing.T) {
	e := setup(t)
	e.newPrompt(t, "r1", "style", "Be terse.")
	c := e.create(t, "r2", agentAttach(sha(agentYAML), "append", "prompt.parts", `{"id":"style"}`))
	e.submit(c.ChangeID, false)
	// Another path moves the part (a direct commit stands in for a second applied change).
	_ = e.svc.DB.Writer.Submit(context.Background(), "t", func(ctx context.Context, tx *sql.Tx) error {
		return builderparts.Commit(ctx, tx, &builderparts.Effects{Parts: []builderparts.Part{{Kind: "prompt", ID: "style", Text: "new", Revision: 2}}}, "x", "", 1)
	})
	e.fake.plans[e.get(t, c.ChangeID).Plan.PlanID].State = "approved"
	_ = e.svc.Poll(context.Background())
	if s := e.get(t, c.ChangeID).State; s != "stale" && s != "awaiting_approval" {
		t.Fatalf("state %s", s)
	}
	if len(e.fake.applied) != 0 {
		t.Fatal("applied against a moved part")
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

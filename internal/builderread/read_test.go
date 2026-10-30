package builderread

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

const planted = "sk-planted1234567890abcdef"

type fakeSource struct {
	files map[string]string
	plan  string
}

func (f *fakeSource) Files(context.Context) ([]catalogindex.FileRef, error) {
	var out []catalogindex.FileRef
	for p := range f.files {
		out = append(out, catalogindex.FileRef{Path: p, Layer: "system"})
	}
	return out, nil
}
func (f *fakeSource) Read(_ context.Context, p string) (string, error) { return f.files[p], nil }
func (f *fakeSource) LastPlan(context.Context) (string, error)         { return f.plan, nil }

type fakeSessions struct {
	list []LiveSession
	err  error
}

func (f fakeSessions) Running(context.Context) ([]LiveSession, error) { return f.list, f.err }

const agentV1 = `id: builder
name: Builder
description: builds things
provider: claude
model: claude-sonnet-4-5
extends: base
skills: [commit, ghost-skill]
permissions:
  permission_mode: acceptEdits
mcp:
  servers: [tether]
`

var agentV2 = strings.Replace(agentV1, "claude-sonnet-4-5", "claude-sonnet-5-5", 1)

const launchYAML = `id: builder-main
agent: builder
project: tachyon
worker: local
workspace:
  mode: worktree
  base: main
env:
  FORGE_TOKEN: ` + planted + `
  PLAIN_NAME: visible-looking-value
overrides:
  env:
    API_SECRET: hunter2-secret-value
`

func fixture(t *testing.T, sess SessionSource) (*Reader, *http.ServeMux, *fakeSource, *catalogindex.Index) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Unix(1_800_000_000, 0).UTC()
	src := &fakeSource{plan: "plan_1", files: map[string]string{
		"agents/builder.yaml":        agentV1,
		"agents/base.yaml":           "id: base\nabstract: true\nskills: [commit]\npermissions:\n  network: restricted\n  mcp_scopes: [tether:read]\nprompt:\n  system_prompt: Be careful.\n",
		"projects/tachyon.yaml":      "id: tachyon\nmcp:\n  servers: [tether, extra]\n",
		"agents/old.yaml":            "id: old\nenabled: false\nprovider: codex\nmodel: gpt-5\n",
		"launches/builder-main.yaml": launchYAML,
		"skills/commit.md":           "---\nid: commit\nname: Commit\ndescription: small commits\n---\nBody\nAuthorization: Bearer " + planted + "\n",
		"mcp-servers/tether.yaml":    "command: x\nenv:\n  TOKEN: " + planted + "\n",
	}}
	ix := &catalogindex.Index{Source: src, Store: &catalogindex.Store{DB: db, Now: func() time.Time { return now }}, Bus: catalogindex.NewBroker(func() time.Time { return now })}
	if err := ix.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := &Reader{Index: ix.Store, Sessions: sess}
	mux := http.NewServeMux()
	r.Mount(mux, func(h http.Handler) http.Handler { return h })
	return r, mux, src, ix
}

func get(t *testing.T, mux *http.ServeMux, path string, want int) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", builderapi.Prefix+path, nil))
	if rec.Code != want {
		t.Fatalf("GET %s = %d, want %d: %s", path, rec.Code, want, rec.Body)
	}
	for _, leak := range []string{planted, "hunter2", "visible-looking-value"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("GET %s leaked %q: %s", path, leak, rec.Body)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAgentsList(t *testing.T) {
	_, mux, _, _ := fixture(t, nil)
	m := get(t, mux, "/agents", 200)
	rows := m["agents"].([]any)
	if len(rows) != 3 || m["total"].(float64) != 3 || m["next_cursor"] != nil {
		t.Fatalf("unexpected list: %v", m)
	}
	var b map[string]any
	for _, r := range rows {
		if r.(map[string]any)["id"] == "builder" {
			b = r.(map[string]any)
		}
	}
	if b["policy_tag"] != "edits" || b["launch_count"].(float64) != 1 || b["urn"] != "urn:tether:agent:builder" || b["enabled"] != true {
		t.Fatalf("builder row: %v", b)
	}
	if d := get(t, mux, "/agents?state=disabled", 200); len(d["agents"].([]any)) != 1 {
		t.Fatalf("state filter: %v", d)
	}
	p1 := get(t, mux, "/agents?limit=2", 200)
	if len(p1["agents"].([]any)) != 2 || p1["next_cursor"] != "c2" {
		t.Fatalf("page 1: %v", p1)
	}
	if p2 := get(t, mux, "/agents?limit=2&cursor=c2", 200); len(p2["agents"].([]any)) != 1 || p2["next_cursor"] != nil {
		t.Fatalf("page 2: %v", p2)
	}
}

func TestAgentDetailAndNotFound(t *testing.T) {
	_, mux, _, _ := fixture(t, nil)
	m := get(t, mux, "/agents/builder", 200)
	if m["identity"].(map[string]any)["description"] != "builds things" ||
		m["runtime"].(map[string]any)["extends"] != "base" ||
		m["policy"].(map[string]any)["permission_mode"] != "acceptEdits" ||
		m["revision"].(map[string]any)["path"] != "agents/builder.yaml" {
		t.Fatalf("detail: %v", m)
	}
	if l := m["launches"].([]any); len(l) != 1 || l[0].(map[string]any)["live_count"] != nil {
		t.Fatalf("launches without sessions must report null live_count: %v", l)
	}
	get(t, mux, "/agents/nope", 404)
}

func TestUsedByLiveAndUnknown(t *testing.T) {
	sess := fakeSessions{list: []LiveSession{{ID: "s1", LaunchID: "builder-main", AgentID: "builder", State: "working"}, {ID: "s2", AgentID: "other"}}}
	_, mux, _, _ := fixture(t, sess)
	m := get(t, mux, "/agents/builder/used_by", 200)
	if ls := m["live_sessions"].([]any); len(ls) != 1 || m["counts"].(map[string]any)["live_sessions"].(float64) != 1 {
		t.Fatalf("live: %v", m)
	}
	if e := get(t, mux, "/agents/base/used_by", 200)["extended_by"].([]any); len(e) != 1 || e[0] != "builder" {
		t.Fatalf("extended_by: %v", e)
	}
	_, mux2, _, _ := fixture(t, fakeSessions{err: errors.New("down")})
	m = get(t, mux2, "/agents/builder/used_by", 200)
	if m["live_sessions"] != nil || m["counts"].(map[string]any)["live_sessions"] != nil {
		t.Fatalf("an unreachable Tether must read null, not zero: %v", m)
	}
}

func TestChecks(t *testing.T) {
	_, mux, _, _ := fixture(t, nil)
	m := get(t, mux, "/agents/builder/checks", 200)
	c := m["checks"].([]any)[0].(map[string]any)
	if c["name"] != "references_exist" || c["status"] != "fail" || !strings.Contains(c["message"].(string), "ghost-skill") {
		t.Fatalf("checks: %v", m)
	}
	if m["summary"].(map[string]any)["fail"].(float64) != 1 {
		t.Fatalf("summary: %v", m)
	}
	ok := get(t, mux, "/agents/base/checks", 200)
	if ok["checks"].([]any)[0].(map[string]any)["status"] != "ok" {
		t.Fatalf("abstract agent with no refs should pass: %v", ok)
	}
}

func TestHistoryDiff(t *testing.T) {
	_, mux, src, ix := fixture(t, nil)
	src.files["agents/builder.yaml"], src.plan = agentV2, "plan_2"
	if err := ix.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := get(t, mux, "/agents/builder/history", 200)
	es := m["entries"].([]any)
	if len(es) != 2 {
		t.Fatalf("entries: %v", m)
	}
	newest, oldest := es[0].(map[string]any), es[1].(map[string]any)
	d, _ := newest["diff"].(string)
	if !strings.Contains(d, "-model: claude-sonnet-4-5") || !strings.Contains(d, "+model: claude-sonnet-5-5") || !strings.HasPrefix(d, "--- a/agents/builder.yaml") {
		t.Fatalf("diff: %q", d)
	}
	if oldest["diff"] != nil || newest["snapshot_seq"].(float64) != 2 || oldest["snapshot_seq"].(float64) != 1 {
		t.Fatalf("seq/diff: %v", es)
	}
	if det := get(t, mux, "/agents/builder", 200); det["revision"].(map[string]any)["snapshot_seq"].(float64) != 2 {
		t.Fatalf("detail revision: %v", det["revision"])
	}
}

func TestLaunchesNeverExposeEnvValues(t *testing.T) {
	sess := fakeSessions{list: []LiveSession{{ID: "s1", LaunchID: "builder-main", AgentID: "builder"}}}
	_, mux, _, _ := fixture(t, sess)
	m := get(t, mux, "/launches", 200)
	l := m["launches"].([]any)[0].(map[string]any)
	env := l["env"].(map[string]any)
	if l["provider"] != "claude" || l["live_count"].(float64) != 1 || env["secret_like_count"].(float64) != 2 {
		t.Fatalf("row: %v", l)
	}
	keys := env["keys"].([]any)
	if len(keys) != 3 {
		t.Fatalf("keys: %v", keys)
	}
	d := get(t, mux, "/launches/builder-main", 200)
	if ws := d["workspace"].(map[string]any); ws["mode"] != "worktree" || ws["base"] != "main" {
		t.Fatalf("workspace: %v", d)
	}
	get(t, mux, "/launches/nope", 404)
	if f := get(t, mux, "/launches?agent=other", 200); len(f["launches"].([]any)) != 0 {
		t.Fatalf("filter: %v", f)
	}
}

func TestUnifiedDiff(t *testing.T) {
	if unifiedDiff("f", "a\nb\n", "a\nb\n") != "" {
		t.Fatal("equal texts must diff to empty")
	}
	got := unifiedDiff("f", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n10\n")
	want := "--- a/f\n+++ b/f\n@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCachedSessions(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	c := newCached(countSrc{&calls}, 5*time.Second, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		_, _ = c.Running(context.Background())
	}
	now = now.Add(6 * time.Second)
	_, _ = c.Running(context.Background())
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

type countSrc struct{ n *int }

func (c countSrc) Running(context.Context) ([]LiveSession, error) { *c.n++; return nil, nil }

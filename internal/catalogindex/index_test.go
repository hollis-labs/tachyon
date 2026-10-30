package catalogindex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

type fakeSource struct {
	files map[string]string
	fp    map[string]string
	plan  string
	err   error
	reads map[string]int
}

func (f *fakeSource) Files(context.Context) ([]FileRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []FileRef
	for p := range f.files {
		out = append(out, FileRef{Path: p, Layer: "system", Fingerprint: f.fp[p]})
	}
	return out, nil
}
func (f *fakeSource) Read(_ context.Context, p string) (string, error) {
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[p]++
	return f.files[p], nil
}
func (f *fakeSource) LastPlan(context.Context) (string, error) { return f.plan, f.err }

const planted = "sk-planted1234567890abcdef"

const agentYAML = `# the builder
id: builder
name: Builder
provider: claude
model: claude-sonnet-5-5
skills: [commit]
permissions:
  permission_mode: default
mcp:
  servers: [tether]
`

const launchYAML = `id: builder-main
agent: builder
project: tachyon
worker: local
env:
  FORGE_TOKEN: ` + planted + `
  PLAIN_NAME: visible-looking-value
overrides:
  env:
    API_SECRET: hunter2-secret-value
`

func setup(t *testing.T) (*Index, *fakeSource, *time.Time) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Unix(1_800_000_000, 0).UTC()
	src := &fakeSource{plan: "plan_1", files: map[string]string{
		"agents/builder.yaml":        agentYAML,
		"agents/old.yaml":            "id: old\nenabled: false\n",
		"agents/retired.yaml":        "id: retired\nenabled: false\nspec:\n  lifecycle_state: retired\n",
		"launches/builder-main.yaml": launchYAML,
		"projects/tachyon.yaml":      "id: tachyon\nname: Tachyon\n",
		"skills/commit.md":           "---\nid: commit\nname: Commit\ndescription: commit things\n---\nBody. Authorization: Bearer abcdefghijklmnop1234\n",
		"mcp-servers/tether.yaml":    "command: x\nenv:\n  TOKEN: " + planted + "\n",
		"providers/claude.yaml":      "token: " + planted + "\n",
		"global.yaml":                "x: 1\n",
	}}
	ix := &Index{Source: src, Store: &Store{DB: db, Now: func() time.Time { return now }}, Bus: NewBroker(func() time.Time { return now })}
	return ix, src, &now
}

func TestPassIndexesTypedEntitiesAndNeverReadsCredentialDirs(t *testing.T) {
	ix, src, _ := setup(t)
	if err := ix.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := ix.Summary(context.Background())
	if s.Agents.Total != 3 || s.Agents.Active != 1 || s.Agents.Disabled != 1 || s.Agents.Retired != 1 || s.LaunchesTotal != 1 || s.Skills != 1 || s.Projects != 1 || s.ToolServers != 1 {
		t.Errorf("summary: %+v", s)
	}
	for _, p := range []string{"mcp-servers/tether.yaml", "providers/claude.yaml", "global.yaml"} {
		if src.reads[p] != 0 {
			t.Errorf("%s was read", p)
		}
	}
	ents, _ := ix.Store.Entities(context.Background())
	l := ents["launches/builder-main.yaml"]
	if l.Fields["secret_like_count"].(float64) != 2 || len(l.Fields["env_keys"].([]any)) != 3 || l.Fields["agent"] != "builder" {
		t.Errorf("launch fields: %v", l.Fields)
	}
	a := ents["agents/builder.yaml"]
	if a.ID != "builder" || a.Provider != "claude" || a.SHA != shaOf(agentYAML) || a.Fields["mcp_servers"].([]any)[0] != "tether" {
		t.Errorf("agent: %+v", a)
	}
}

func TestNoSecretReachesAnyTableEventOrSnapshot(t *testing.T) {
	ix, _, _ := setup(t)
	if err := ix.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"catalog_entities", "catalog_snapshots", "catalog_state"} {
		rows, err := ix.Store.DB.Reader.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for _, v := range vals {
				s := ""
				switch x := v.(type) {
				case string:
					s = x
				case []byte:
					s = string(x)
				}
				for _, bad := range []string{planted, "hunter2-secret-value", "visible-looking-value", "abcdefghijklmnop1234"} {
					if strings.Contains(s, bad) {
						t.Errorf("%s holds %q", table, bad)
					}
				}
			}
		}
		rows.Close()
	}
	w := httptest.NewRecorder()
	ix.SummaryHandler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), planted) {
		t.Error("summary leaks")
	}
	sn, _ := ix.Store.Snapshots(context.Background(), "launches/builder-main.yaml", 5)
	if len(sn) != 1 || !strings.Contains(sn[0].Content, "FORGE_TOKEN: '***'") && !strings.Contains(sn[0].Content, `FORGE_TOKEN: "***"`) && !strings.Contains(sn[0].Content, "FORGE_TOKEN: ***") {
		t.Errorf("env keys are kept, values masked: %v", sn)
	}
}

func TestMaskYAMLKeepsCommentsOrderAndUnknownKeys(t *testing.T) {
	out, n, err := MaskYAML(agentYAML + "unknown_key: {nested: 1}\n")
	if err != nil || !strings.Contains(out, "# the builder") || !strings.Contains(out, "unknown_key") || n != 0 {
		t.Errorf("%v %d\n%s", err, n, out)
	}
	if strings.Index(out, "id: builder") > strings.Index(out, "skills") {
		t.Error("key order changed")
	}
	if _, _, err := MaskYAML("a: [unclosed"); err == nil {
		t.Error("unparsable yaml must be refused, never stored raw")
	}
}

func TestChangeDetectionEventsFingerprintsAndOwnApplies(t *testing.T) {
	ix, src, now := setup(t)
	ctx := context.Background()
	src.fp = map[string]string{"agents/builder.yaml": "f1"}
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ix.Bus.Since(ix.Bus.base)) != 0 {
		t.Error("the seeding pass publishes no catalog event")
	}
	// Nothing changed: a fingerprinted file is not re-read.
	src.reads = nil
	*now = now.Add(time.Minute)
	if err := ix.Pass(ctx); err != nil || src.reads["agents/builder.yaml"] != 0 {
		t.Errorf("fingerprinted file re-read: %v %v", err, src.reads)
	}
	// An edit outside the Builder is observed.
	src.files["agents/builder.yaml"] = strings.Replace(agentYAML, "claude-sonnet-5-5", "claude-opus-5-5", 1)
	src.fp["agents/builder.yaml"] = "f2"
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	ev := ix.Bus.Since(ix.Bus.base)
	if len(ev) != 1 || ev[0].Data["source"] != "observed" || ev[0].Data["changed_paths"].([]string)[0] != "agents/builder.yaml" {
		t.Fatalf("events: %+v", ev)
	}
	// The Builder's own apply labels the snapshot with the plan and actor.
	src.files["agents/builder.yaml"] += "# edited\n"
	src.fp["agents/builder.yaml"] = "f3"
	ix.NoteApplied("plan_9", "operator", []string{"agents/builder.yaml"})
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	sn, _ := ix.Store.Snapshots(ctx, "agents/builder.yaml", 10)
	if len(sn) != 3 || sn[0].Source != "builder" || sn[0].PlanID != "plan_9" || sn[1].Source != "observed" {
		t.Errorf("snapshots: %+v", sn)
	}
	if last := ix.Bus.Since(ix.Bus.base); last[len(last)-1].Data["source"] != "builder" {
		t.Error("own apply event source")
	}
	// A vanished file is removed and reported.
	delete(src.files, "agents/old.yaml")
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	ents, _ := ix.Store.Entities(ctx)
	if _, ok := ents["agents/old.yaml"]; ok {
		t.Error("vanished file stays indexed")
	}
}

func TestSourceFailureKeepsDataAndReportsStatus(t *testing.T) {
	ix, src, _ := setup(t)
	ctx := context.Background()
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	src.err = errors.New("Tether unreachable")
	if err := ix.Pass(ctx); err == nil {
		t.Fatal("pass must report the failure")
	}
	s, _ := ix.Summary(ctx)
	if s.State.SourceStatus != "unreachable" || s.Agents.Total != 3 {
		t.Errorf("data kept, status reported: %+v", s)
	}
	m := s.asMap()
	if m["catalog"].(map[string]any)["source_status"] != "unreachable" {
		t.Error("summary status")
	}
	src.err = nil
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ = ix.Summary(ctx); s.State.SourceStatus != "ok" {
		t.Error("recovery")
	}
}

func TestRetentionKeepsFiftyAndNinetyDays(t *testing.T) {
	ix, _, now := setup(t)
	ctx := context.Background()
	var snaps []Snapshot
	for i := 0; i < 60; i++ {
		snaps = append(snaps, Snapshot{Path: "agents/a.yaml", SHA: shaOf(string(rune('a' + i))), Content: "x", At: now.Add(time.Duration(i) * time.Second), Source: "observed"})
	}
	snaps = append(snaps, Snapshot{Path: "agents/b.yaml", SHA: "old", Content: "x", At: now.Add(-100 * 24 * time.Hour), Source: "observed"})
	if err := ix.Store.Apply(ctx, nil, nil, snaps); err != nil {
		t.Fatal(err)
	}
	if err := ix.Store.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := ix.Store.Snapshots(ctx, "agents/a.yaml", 100)
	b, _ := ix.Store.Snapshots(ctx, "agents/b.yaml", 100)
	if len(a) != SnapshotsPerPath || len(b) != 0 {
		t.Errorf("a=%d b=%d", len(a), len(b))
	}
}

func TestStreamSendsSummaryThenReplaysAfterLastEventID(t *testing.T) {
	ix, src, now := setup(t)
	ctx := context.Background()
	_ = ix.Pass(ctx)
	src.files["agents/new.yaml"] = "id: new\n"
	_ = ix.Pass(ctx)
	src.files["agents/new2.yaml"] = "id: new2\n"
	*now = now.Add(time.Second)
	_ = ix.Pass(ctx)
	ev := ix.Bus.Since(ix.Bus.base)
	if len(ev) != 2 {
		t.Fatalf("ring: %d", len(ev))
	}
	run := func(last string) string {
		r := httptest.NewRequest("GET", "/?once=1", nil)
		if last != "" {
			r.Header.Set("Last-Event-ID", last)
		}
		w := httptest.NewRecorder()
		ix.StreamHandler().ServeHTTP(w, r)
		return w.Body.String()
	}
	all := run("")
	if !strings.HasPrefix(all, "data: {\"") || !strings.Contains(all, `"type":"summary"`) || strings.Contains(all, "id: ") {
		t.Errorf("fresh connection: summary snapshot only: %s", all)
	}
	resumed := run(strings.TrimSpace(strings.Split(strings.Split(mustID(ev[0]), "\n")[0], " ")[0]))
	if strings.Count(resumed, "id: ") != 1 || !strings.Contains(resumed, "agents/new2.yaml") {
		t.Errorf("resume after first: %s", resumed)
	}
	if strings.Contains(run("5"), "id: ") {
		t.Error("an id from another epoch replays nothing")
	}
	r := httptest.NewRequest("GET", "/?once=1", nil)
	r.Header.Set("Last-Event-ID", "x")
	w := httptest.NewRecorder()
	ix.StreamHandler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Error("bad Last-Event-ID")
	}
	var m map[string]any
	if strings.Contains(all, "data: ") {
		_ = json.Unmarshal([]byte(strings.TrimPrefix(strings.Split(all, "\n")[0], "data: ")), &m)
		if m["agents"] == nil {
			t.Error("summary fields are flat in the frame")
		}
	}
}

func mustID(e Event) string { return strings.TrimSpace(strings.Join([]string{itoa(e.ID)}, "")) }

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestKindOfAllowList(t *testing.T) {
	for p, want := range map[string]string{"agents/a.yaml": KindAgent, "skills/x.md": KindSkill, "launches/l.yml": KindLaunch, "mcp-servers/m.yaml": KindMCPServer} {
		if k, _, ok := kindOf(p); !ok || k != want {
			t.Errorf("%s: %s %v", p, k, ok)
		}
	}
	for _, p := range []string{"global.yaml", "agents/../x.yaml", "agents/A.yaml", "agents/a/b.yaml", "agents/a.txt", "credentials/x.yaml", "sandbox-profiles/s.yaml"} {
		if _, _, ok := kindOf(p); ok {
			t.Errorf("%s allowed", p)
		}
	}
	if _, readable, _ := kindOf("mcp-servers/m.yaml"); readable {
		t.Error("mcp-servers must never be read")
	}
}

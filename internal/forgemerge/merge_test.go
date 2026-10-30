package forgemerge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	sha1 = "0da8631e75cfa86e2506e6b076cceaa4a56f4aa3"
	sha2 = "b9bde5dfabfb122d47d9d8b15ed443be34836f3a"
	base = "/api/plugins/console/v1/forge/repos/github/o/r/pulls/7"
)

type gh struct {
	pull       map[string]any
	mergeable  func() any // returns the "mergeable" value for each read
	runs       string
	reviews    string
	mergeCode  int
	merged     atomic.Int32
	mergeCalls atomic.Int32
	deleted    atomic.Int32
}

func (g *gh) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Error("missing token")
		}
		p := r.URL.Path
		switch {
		case r.Method == "PUT" && strings.HasSuffix(p, "/pulls/7/merge"):
			g.mergeCalls.Add(1)
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["sha"] == "" || in["merge_method"] == "" {
				t.Error("merge needs sha and method", in)
			}
			if g.mergeCode != 0 && g.mergeCode != 200 {
				w.WriteHeader(g.mergeCode)
				_, _ = w.Write([]byte(`{"message":"Pull Request is not mergeable"}`))
				return
			}
			g.merged.Store(1)
			_, _ = w.Write([]byte(`{"merged":true,"sha":"1b7a867c692932f990cfa105b150d4ce07ce3cd2","message":"Pull Request successfully merged"}`))
		case r.Method == "DELETE":
			g.deleted.Add(1)
			w.WriteHeader(204)
		case strings.HasSuffix(p, "/pulls/7"):
			pr := map[string]any{}
			for k, v := range g.pull {
				pr[k] = v
			}
			pr["mergeable"] = g.mergeable()
			if g.merged.Load() == 1 {
				pr["state"], pr["merged"], pr["merge_commit_sha"] = "closed", true, "1b7a867c692932f990cfa105b150d4ce07ce3cd2"
			}
			_ = json.NewEncoder(w).Encode(pr)
		case strings.HasSuffix(p, "/pulls/7/reviews"):
			_, _ = w.Write([]byte(g.reviews))
		case strings.HasSuffix(p, "/check-runs"):
			_, _ = w.Write([]byte(g.runs))
		case strings.HasSuffix(p, "/status"):
			_, _ = w.Write([]byte(`{"statuses":[]}`))
		case strings.Contains(p, "/compare/"):
			_, _ = w.Write([]byte(`{"behind_by":6}`))
		case strings.Contains(p, "/protection"):
			w.WriteHeader(404)
		case p == "/repos/o/r":
			_, _ = w.Write([]byte(`{"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":false}`))
		default:
			t.Error("unexpected GitHub path", r.Method, p)
			w.WriteHeader(500)
		}
	}))
}

func newGH() *gh {
	return &gh{
		pull: map[string]any{"number": 7, "title": "feat: thing", "state": "open", "draft": false, "mergeable_state": "clean",
			"head": map[string]any{"sha": sha1, "ref": "feat/x", "repo": map[string]any{"full_name": "o/r"}},
			"base": map[string]any{"ref": "main", "repo": map[string]any{"full_name": "o/r"}}},
		mergeable: func() any { return true },
		runs:      `{"check_runs":[{"status":"completed","conclusion":"success"}]}`,
		reviews:   `[]`,
	}
}

func handler(t *testing.T, g *gh) (*Handler, http.Handler, func()) {
	srv := g.server(t)
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Token: "tok", Base: srv.URL, DB: db, Wait: func(time.Duration) {}}
	return h, h.Mux(), func() { srv.Close(); db.Close() }
}

func call(h http.Handler, method, path, body, key string) (int, map[string]any) {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func blockerCodes(out map[string]any, under string) []string {
	m, _ := out[under].(map[string]any)
	var codes []string
	for _, b := range m["blockers"].([]any) {
		codes = append(codes, b.(map[string]any)["code"].(string))
	}
	return codes
}

func TestPreflightStatesAndBlockers(t *testing.T) {
	g := newGH()
	_, h, done := handler(t, g)
	defer done()
	status, out := call(h, "GET", base+"/merge-preflight", "", "")
	d, _ := out["data"].(map[string]any)
	if status != 200 || d["state"] != "ready" || d["head_sha"] != sha1 || d["default_method"] != "squash" || d["commit_title"] != "feat: thing (#7)" {
		t.Fatal(status, out)
	}
	if ms := d["methods_allowed"].([]any); len(ms) != 2 {
		t.Fatal("methods follow the repository settings", ms)
	}
	// Conflicts, failing and pending checks, changes requested, behind base, draft.
	g.pull["mergeable_state"], g.mergeable = "dirty", func() any { return false }
	g.runs = `{"check_runs":[{"status":"completed","conclusion":"failure"},{"status":"in_progress"}]}`
	g.reviews = `[{"user":{"login":"alice"},"state":"CHANGES_REQUESTED"}]`
	_, out = call(h, "GET", base+"/merge-preflight", "", "")
	d = out["data"].(map[string]any)
	got := map[string]bool{}
	for _, c := range blockerCodes(out, "data") {
		got[c] = true
	}
	if d["state"] != "blocked" || !got["conflicts"] || !got["checks_failing"] || !got["checks_pending"] || !got["changes_requested"] {
		t.Fatal(out)
	}
	g.pull["mergeable_state"], g.mergeable, g.runs, g.reviews = "behind", func() any { return true }, `{"check_runs":[]}`, `[]`
	_, out = call(h, "GET", base+"/merge-preflight", "", "")
	if d = out["data"].(map[string]any); d["behind_by"].(float64) != 6 || blockerCodes(out, "data")[0] != "behind_base" {
		t.Fatal(out)
	}
	g.pull["mergeable_state"] = "blocked"
	_, out = call(h, "GET", base+"/merge-preflight", "", "")
	if blockerCodes(out, "data")[0] != "branch_protection" {
		t.Fatal("an unexplained block is the generic branch protection reason", out)
	}
	g.pull["mergeable_state"], g.pull["draft"] = "clean", true
	_, out = call(h, "GET", base+"/merge-preflight", "", "")
	if blockerCodes(out, "data")[0] != "draft" {
		t.Fatal(out)
	}
	g.pull["draft"], g.pull["state"] = false, "closed"
	_, out = call(h, "GET", base+"/merge-preflight", "", "")
	if blockerCodes(out, "data")[0] != "closed" {
		t.Fatal(out)
	}
}

func TestPreflightRetriesMergeabilityThenReportsPending(t *testing.T) {
	g := newGH()
	reads := 0
	g.mergeable = func() any {
		reads++
		if reads < 3 {
			return nil
		}
		return true
	}
	_, h, done := handler(t, g)
	defer done()
	if _, out := call(h, "GET", base+"/merge-preflight", "", ""); out["data"].(map[string]any)["state"] != "ready" || reads != 3 {
		t.Fatal("did not retry until GitHub answered", reads, out)
	}
	reads = -100
	if _, out := call(h, "GET", base+"/merge-preflight", "", ""); out["data"].(map[string]any)["state"] != "pending" {
		t.Fatal("never a guess", out)
	}
}

func TestMergeGuardedByPreflightHeadPinAndMethod(t *testing.T) {
	g := newGH()
	_, h, done := handler(t, g)
	defer done()
	put := func(body, key string) (int, map[string]any) { return call(h, "PUT", base+"/merge", body, key) }
	good := `{"method":"squash","head_sha":"` + sha1 + `"}`
	if s, out := put(good, ""); s != 400 || codeOf(out) != "idempotency_key_required" {
		t.Fatal(s, out)
	}
	for _, bad := range []string{`{}`, `{"method":"fast","head_sha":"` + sha1 + `"}`, `{"method":"squash","head_sha":"abc"}`, `{"method":"squash","head_sha":"` + sha1 + `","x":1}`} {
		if s, out := put(bad, "k-bad"); s != 400 {
			t.Fatal("bad body allowed", bad, s, out)
		}
	}
	if s, out := put(`{"method":"squash","head_sha":"`+sha2+`"}`, "k-moved"); s != 409 || codeOf(out) != "head_moved" || out["error"].(map[string]any)["head_sha"] != sha1 {
		t.Fatal(s, out)
	}
	if s, out := put(`{"method":"rebase","head_sha":"`+sha1+`"}`, "k-rebase"); s != 400 {
		t.Fatal("a method the repository does not allow", s, out)
	}
	g.runs = `{"check_runs":[{"status":"completed","conclusion":"failure"}]}`
	if s, out := put(good, "k-blocked"); s != 409 || codeOf(out) != "merge_blocked" || blockerCodes(out, "error")[0] != "checks_failing" {
		t.Fatal(s, out)
	}
	if g.mergeCalls.Load() != 0 {
		t.Fatal("a blocked merge reached GitHub")
	}
}

func TestMergeSucceedsOnceAndIsIdempotent(t *testing.T) {
	g := newGH()
	var after atomic.Int32
	hd, h, done := handler(t, g)
	defer done()
	hd.After = func(_ context.Context, key, sha string) Settled {
		after.Add(1)
		if key != "github:o/r#7" || sha == "" {
			t.Error("after hook", key, sha)
		}
		return Settled{Settled: true, LinksDone: 2}
	}
	body := `{"method":"squash","head_sha":"` + sha1 + `","commit_title":"feat: thing (#7)","delete_branch":true}`
	s, out := call(h, "PUT", base+"/merge", body, "k1")
	d, _ := out["data"].(map[string]any)
	if s != 200 || d["merged"] != true || d["sha"] != "1b7a867c692932f990cfa105b150d4ce07ce3cd2" || d["settled"] != true || d["links_done"].(float64) != 2 || d["method"] != "squash" {
		t.Fatal(s, out)
	}
	if g.deleted.Load() != 1 {
		t.Fatal("branch delete was asked for")
	}
	// The same key and body: the stored answer, no second merge, no second hook.
	if s2, out2 := call(h, "PUT", base+"/merge", body, "k1"); s2 != 200 || out2["data"].(map[string]any)["sha"] != d["sha"] || g.mergeCalls.Load() != 1 || after.Load() != 1 {
		t.Fatal("replay", s2, out2, g.mergeCalls.Load(), after.Load())
	}
	// The same key for a different merge is refused.
	if s3, out3 := call(h, "PUT", base+"/merge", `{"method":"merge","head_sha":"`+sha1+`"}`, "k1"); s3 != 409 || codeOf(out3) != "idempotency_conflict" {
		t.Fatal(s3, out3)
	}
	// A new key on an already merged PR reports it accurately and merges nothing again.
	if s4, out4 := call(h, "PUT", base+"/merge", body, "k2"); s4 != 200 || out4["data"].(map[string]any)["replayed"] != true || g.mergeCalls.Load() != 1 {
		t.Fatal("already merged", s4, out4)
	}
}

func TestMergeWhenGitHubDeclinesOrOutcomeIsUnknown(t *testing.T) {
	g := newGH()
	g.mergeCode = 405
	_, h, done := handler(t, g)
	defer done()
	body := `{"method":"squash","head_sha":"` + sha1 + `"}`
	if s, out := call(h, "PUT", base+"/merge", body, "k1"); s != 409 || codeOf(out) != "conflict" || !strings.Contains(out["error"].(map[string]any)["message"].(string), "not mergeable") {
		t.Fatal(s, out)
	}
	// A declined merge is not remembered: the same key can be tried again.
	g.mergeCode = 200
	if s, out := call(h, "PUT", base+"/merge", body, "k1"); s != 200 {
		t.Fatal(s, out)
	}
	// An outage after the merge went through still reports merged.
	g2 := newGH()
	g2.mergeCode = 500
	_, h2, done2 := handler(t, g2)
	defer done2()
	g2.merged.Store(1)
	if s, out := call(h2, "PUT", base+"/merge", body, "k9"); s != 200 || out["data"].(map[string]any)["merged"] != true {
		t.Fatal("unknown outcome must be re-read", s, out)
	}
}

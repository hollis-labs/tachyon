package agentsserve

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentsapi"
	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/agentsderive"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	db    *localstore.Store
	store *agentscollector.Store
	srv   *Server
	now   time.Time
	seq   int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, now: t0}
	e.store = &agentscollector.Store{DB: db, Now: func() time.Time { return e.now }}
	e.srv = &Server{DB: db, Collector: &agentscollector.Collector{Store: e.store, Now: func() time.Time { return e.now }}, Now: func() time.Time { return e.now }}
	return e
}

// frame applies one session event at a time offset (relative to t0).
func (e *env) frame(kind, session string, at time.Duration, payload string) {
	e.t.Helper()
	e.seq++
	raw := fmt.Sprintf(`{"id":"e:%d","kind":%q,"session_id":%q,"time":%q,"payload":%s}`, e.seq, kind, session, t0.Add(at).Format(time.RFC3339Nano), payload)
	f, ok := agentscollector.ParseFrame([]byte(raw))
	if !ok {
		e.t.Fatalf("frame did not parse: %s", raw)
	}
	if err := e.store.Apply(context.Background(), []agentscollector.Frame{f}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) bus(kind, app, payload string) {
	e.t.Helper()
	e.seq++
	raw := fmt.Sprintf(`{"id":"e:%d","kind":%q,"app":%q,"time":%q,"payload":%s}`, e.seq, kind, app, e.now.Format(time.RFC3339Nano), payload)
	f, _ := agentscollector.ParseFrame([]byte(raw))
	if err := e.store.Apply(context.Background(), []agentscollector.Frame{f}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if err := e.db.Writer.Submit(context.Background(), "test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) get(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	mux := http.NewServeMux()
	e.srv.Mount(mux, func(h http.Handler) http.Handler { return h })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "http://x"+agentsapi.Prefix+path, nil))
	return w
}

func (e *env) send(method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	e.srv.Mount(mux, func(h http.Handler) http.Handler { return h })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, "http://x"+agentsapi.Prefix+path, strings.NewReader(body)))
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	return v
}

type list struct {
	Sessions  []Session      `json:"sessions"`
	Summary   map[string]int `json:"summary"`
	Collector map[string]any `json:"collector"`
}

// seed builds one session in each state at e.now = t0 + 1h.
func seed(t *testing.T) *env {
	e := newEnv(t)
	e.now = t0.Add(time.Hour)
	const m = `"provider":"claude","model":"claude-sonnet-5-5","workroot":"/work/tachyon"`
	recent := 55 * time.Minute
	e.frame("session.work_state_changed", "s_work", recent, `{"from":"idle","to":"processing",`+m+`}`)
	e.frame("session.work_state_changed", "s_wait", recent, `{"from":"processing","to":"awaiting_approval","provider":"claude","workroot":"/work/other"}`)
	e.frame("session.work_state_changed", "s_idle", recent, `{"from":"processing","to":"idle","provider":"codex","workroot":"/work/tachyon"}`)
	e.frame("session.work_state_changed", "s_old", 0, `{"from":"idle","to":"processing","provider":"claude","workroot":"/work/old"}`) // 1h silent
	e.frame("session.work_state_changed", "s_dead", recent, `{"from":"idle","to":"processing","provider":"claude","workroot":"/work/dead"}`)
	e.exec(`UPDATE agents_sessions SET alive=0 WHERE session_id='s_dead'`)
	e.frame("session.created", "s_new", recent, `{"provider":"pty"}`) // a lifecycle event only: no work state
	e.frame("session.ended", "s_end", 10*time.Minute, `{"session_state":"ended","reason":"stopped","provider":"claude"}`)
	return e
}

func TestListDerivesEveryStateAndNeverFakesZero(t *testing.T) {
	e := seed(t)
	w := e.get("/sessions")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	l := decode[list](t, w)
	by := map[string]Session{}
	for _, s := range l.Sessions {
		by[s.ID] = s
	}
	want := map[string]string{"s_work": "working", "s_wait": "waiting", "s_idle": "idle", "s_old": "stale", "s_dead": "stale"}
	for id, st := range want {
		if by[id].State != st {
			t.Errorf("%s: %q, want %q", id, by[id].State, st)
		}
	}
	if _, listed := by["s_end"]; listed {
		t.Error("an ended session is not listed without include_ended")
	}
	if r := by["s_old"]; r.StaleReason == nil || *r.StaleReason != "no_events" || r.StaleSince == nil {
		t.Errorf("s_old stale reason: %+v", r)
	}
	if r := by["s_dead"]; r.StaleReason == nil || *r.StaleReason != "not_alive" {
		t.Errorf("s_dead: %+v", r.StaleReason)
	}
	if r := by["s_wait"]; r.NeedsYou.Reason == nil || *r.NeedsYou.Reason != "approval" || r.NeedsYou.Since == nil {
		t.Errorf("s_wait needs_you: %+v", r.NeedsYou)
	}
	if r := by["s_idle"]; r.StaleReason != nil || r.LastActivityAt == nil {
		t.Errorf("an idle session shows its last activity: %+v", r)
	}
	for id, s := range by { // nothing metered was seen: every figure is null, never 0
		if s.Tokens.Metered || s.Tokens.Input != nil || s.Tokens.Hour != nil || s.Context.Used != nil || s.Context.Pct != nil || s.Context.Source != "unknown" || s.OneLiner != nil {
			t.Errorf("%s fakes metered data: %+v", id, s)
		}
		if s.Context.Breakdown != nil || s.Assignment != nil || len(s.Spark) != 30 || s.Kind != "process" {
			t.Errorf("%s: %+v", id, s)
		}
		if s.Capabilities.Nudge || s.Capabilities.Restart || s.Capabilities.Interrupt.Available || s.Capabilities.Interrupt.Reason == nil {
			t.Errorf("%s offers a control that does not exist yet: %+v", id, s.Capabilities)
		}
	}
	if by["s_new"].Pending.Known || by["s_wait"].Pending.Known {
		t.Error("pending.known is false when Tether never said")
	}
	wantSum := map[string]int{"working": 1, "waiting": 1, "idle": 2, "stale": 2, "unknown": 0}
	// s_new has activity but no work state: quiet (idle), not unknown.
	for k, v := range wantSum {
		if l.Summary[k] != v {
			t.Errorf("summary %s = %d, want %d (%v)", k, l.Summary[k], v, l.Summary)
		}
	}
	if l.Collector["collecting"] != false || l.Collector["sessions"] == nil {
		t.Errorf("collector block: %v", l.Collector)
	}
	// Order: what needs you first, then working, idle, stale.
	if l.Sessions[0].State != "waiting" || l.Sessions[1].State != "working" || l.Sessions[len(l.Sessions)-1].State != "stale" {
		t.Errorf("order: %v", func() (o []string) {
			for _, s := range l.Sessions {
				o = append(o, s.ID+":"+s.State)
			}
			return
		}())
	}
}

func TestListFiltersAndValidation(t *testing.T) {
	e := seed(t)
	if l := decode[list](t, e.get("/sessions?state=stale")); len(l.Sessions) != 2 || l.Summary["working"] != 1 {
		t.Errorf("state filter must narrow the list but not the summary: %d %v", len(l.Sessions), l.Summary)
	}
	if l := decode[list](t, e.get("/sessions?q=codex")); len(l.Sessions) != 1 || l.Sessions[0].ID != "s_idle" {
		t.Errorf("q: %+v", l.Sessions)
	}
	if l := decode[list](t, e.get("/sessions?q=%2Fwork%2Fother")); len(l.Sessions) != 1 {
		t.Errorf("q by workroot: %d", len(l.Sessions))
	}
	if l := decode[list](t, e.get("/sessions?include_ended=1")); len(l.Sessions) != 7 || l.Summary["idle"] != 2 {
		t.Errorf("include_ended: %d %v", len(l.Sessions), l.Summary)
	}
	g := decode[list](t, e.get("/sessions?group=workroot"))
	for i := 1; i < len(g.Sessions); i++ {
		if g.Sessions[i-1].Workroot > g.Sessions[i].Workroot {
			t.Error("group=workroot must sort by workroot")
		}
	}
	for _, p := range []string{"/sessions?state=bogus", "/sessions?group=bogus", "/sessions?include_ended=2"} {
		if e.get(p).Code != 400 {
			t.Errorf("GET %s must be 400", p)
		}
	}
}

func TestDetailAndNotFound(t *testing.T) {
	e := seed(t)
	if s := decode[Session](t, e.get("/sessions/s_work")); s.ID != "s_work" || s.State != "working" || s.Agent.Name != "session s_work" || s.Agent.URN != nil {
		t.Errorf("detail: %+v", s)
	}
	if s := decode[Session](t, e.get("/sessions/s_end")); s.State != "ended" || s.RuntimeMS < 0 {
		t.Errorf("an ended session opens with state ended: %+v", s)
	}
	for _, p := range []string{"/sessions/nope", "/sessions/nope/events", "/sessions/nope/tokens", "/sessions/nope/context-series", "/sessions/nope/history", "/sessions/bad%20id"} {
		if w := e.get(p); w.Code != 404 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
}

func TestSettingsDefaultsValidateAndTakeEffect(t *testing.T) {
	e := seed(t)
	if s := decode[Settings](t, e.get("/settings")); s.StaleAfterS != 900 || s.HandoffTimeoutS != 300 || len(s.NudgePresets) != 3 {
		t.Errorf("defaults: %+v", s)
	}
	if s := decode[Session](t, e.get("/sessions/s_old")); s.State != "stale" {
		t.Fatalf("s_old %s", s.State)
	}
	ok := `{"stale_after_s":7200,"handoff_prompt":"p","handoff_timeout_s":120,"nudge_presets":["a"]}`
	if w := e.send("PUT", "/settings", ok); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if s := decode[Session](t, e.get("/sessions/s_old")); s.State != "working" {
		t.Errorf("stale_after_s must apply on the next read: %s", s.State)
	}
	if s := decode[Settings](t, e.get("/settings")); s.StaleAfterS != 7200 || s.NudgePresets[0] != "a" {
		t.Errorf("not persisted: %+v", s)
	}
	for name, body := range map[string]string{
		"too small": `{"stale_after_s":5,"handoff_prompt":"p","handoff_timeout_s":120,"nudge_presets":[]}`,
		"no prompt": `{"stale_after_s":900,"handoff_prompt":" ","handoff_timeout_s":120,"nudge_presets":[]}`,
		"timeout":   `{"stale_after_s":900,"handoff_prompt":"p","handoff_timeout_s":9999,"nudge_presets":[]}`,
		"presets":   `{"stale_after_s":900,"handoff_prompt":"p","handoff_timeout_s":120,"nudge_presets":["1","2","3","4","5","6","7","8","9","10","11","12","13"]}`,
		"blank":     `{"stale_after_s":900,"handoff_prompt":"p","handoff_timeout_s":120,"nudge_presets":[""]}`,
		"unknown":   `{"stale_after_s":900,"handoff_prompt":"p","handoff_timeout_s":120,"nudge_presets":[],"x":1}`,
		"not json":  `nope`,
	} {
		if w := e.send("PUT", "/settings", body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

func TestTimelineFiltersGroupsRunsAndShowsGaps(t *testing.T) {
	e := newEnv(t)
	e.now = t0.Add(time.Hour)
	for i, tool := range []string{"Read", "Read", "Grep"} { // one run: 10 s apart
		at := time.Duration(i) * 10 * time.Second
		e.frame("tool.call.started", "s1", at, `{"tool":"`+tool+`"}`)
		e.frame("tool.call.finished", "s1", at+time.Second, `{"tool":"`+tool+`"}`)
	}
	e.frame("message.sent", "s1", 2*time.Minute, `{}`)
	e.frame("tool.call.finished", "s1", 5*time.Minute, `{"tool":"Edit"}`) // alone
	e.frame("session.work_state_changed", "s1", 6*time.Minute, `{"from":"processing","to":"idle"}`)
	if _, err := e.store.AddGap(context.Background(), agentscollector.Gap{Start: t0.Add(3 * time.Minute), End: t0.Add(4 * time.Minute), Reason: "ahead", FromSeq: 5, ToSeq: 9}); err != nil {
		t.Fatal(err)
	}
	type resp struct {
		Events     []map[string]any `json:"events"`
		Gaps       []gapOut         `json:"gaps"`
		NextBefore *int64           `json:"next_before"`
	}
	flat := decode[resp](t, e.get("/sessions/s1/events"))
	if len(flat.Events) != 6 { // 4 finished calls + message + work state; started halves are hidden
		t.Fatalf("flat: %d %v", len(flat.Events), flat.Events)
	}
	if flat.Events[0]["summary"] != "Work state processing -> idle" || flat.Events[2]["summary"] != "Sent a message" && flat.Events[2]["type"] != "message" {
		t.Errorf("newest first, humanised: %v %v", flat.Events[0]["summary"], flat.Events[2])
	}
	for _, ev := range flat.Events {
		if ev["payload"] != nil || ev["card"] != nil {
			t.Errorf("payload and card are always null: %v", ev)
		}
		if strings.Contains(fmt.Sprint(ev["summary"]), "tool.call") {
			t.Errorf("raw kind leaked into the summary: %v", ev["summary"])
		}
	}
	if len(flat.Gaps) != 1 || flat.Gaps[0].Reason != "restart" || flat.Gaps[0].Backfilled {
		t.Errorf("gaps: %+v", flat.Gaps)
	}
	grouped := decode[resp](t, e.get("/sessions/s1/events?group_runs=1"))
	var runs []map[string]any
	for _, ev := range grouped.Events {
		if ev["type"] == "tool_run" {
			runs = append(runs, ev)
		}
	}
	if len(runs) != 1 || runs[0]["calls"] != float64(3) || !strings.Contains(runs[0]["summary"].(string), "3 tool calls (read x2, grep x1)") {
		t.Fatalf("one run of three expected: %v", runs)
	}
	if len(grouped.Events) != 4 { // work state, Edit alone, message, run
		t.Errorf("grouped: %d", len(grouped.Events))
	}
	if n := len(decode[resp](t, e.get("/sessions/s1/events?filter=tools")).Events); n != 4 {
		t.Errorf("filter=tools: %d", n)
	}
	if n := len(decode[resp](t, e.get("/sessions/s1/events?filter=lifecycle")).Events); n != 1 {
		t.Errorf("filter=lifecycle: %d", n)
	}
	if n := len(decode[resp](t, e.get("/sessions/s1/events?q=grep")).Events); n != 1 {
		t.Errorf("q=grep: %d", n)
	}
	page := decode[resp](t, e.get("/sessions/s1/events?limit=2"))
	if len(page.Events) != 2 || page.NextBefore == nil {
		t.Fatalf("page 1: %+v", page)
	}
	next := decode[resp](t, e.get(fmt.Sprintf("/sessions/s1/events?limit=100&before_seq=%d", *page.NextBefore)))
	if len(next.Events) != 4 || next.NextBefore != nil {
		t.Errorf("page 2: %d %v", len(next.Events), next.NextBefore)
	}
	for _, p := range []string{"filter=bogus", "limit=0", "limit=101", "before_seq=x", "group_runs=2"} {
		if e.get("/sessions/s1/events?"+p).Code != 400 {
			t.Errorf("?%s must be 400", p)
		}
	}
}

func TestContextTokensAndHistory(t *testing.T) {
	e := newEnv(t)
	e.now = t0.Add(time.Hour)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	e.frame("session.work_state_changed", "m1", 10*time.Minute, `{"to":"processing","provider":"claude","logical_agent_id":"builder","agent_urn":"msg://agent/x/builder","agent_name":"Builder"}`)
	e.frame("session.work_state_changed", "u1", 10*time.Minute, `{"to":"processing","provider":"pty"}`)
	e.frame("session.ended", "old1", 5*time.Minute, `{"session_state":"ended","reason":"restart","logical_agent_id":"builder","replaced_by":"m1"}`)
	for i, v := range []int64{100000, 300000, 150000} {
		if err := e.store.SetDerived(context.Background(), "m1", agentscollector.Derived{CtxUsed: v, CtxWindow: 1000000, CtxModel: "claude-sonnet-5-5", CtxSeen: true, At: at(30 + i*10),
			Compactions: []agentsderive.Marker{{At: at(50), Before: 300000, After: 150000, Inferred: true}}, LastClear: nil}); err != nil {
			t.Fatal(err)
		}
	}
	for _, min := range []int{40, 50} { // 100k + 50k tokens in the last hour, 20k in the half before
		e.frame("session.usage", "m1", time.Duration(min)*time.Minute, `{"input":100,"output":50,"cache_read":1000,"cache_write":10}`)
	}
	e.frame("session.usage", "m1", 20*time.Minute, `{"input":10,"output":10,"cache_read":0,"cache_write":0}`)
	e.frame("session.usage", "other", 45*time.Minute, `{"input":300,"output":0}`)

	s := decode[Session](t, e.get("/sessions/m1"))
	if s.Agent.Name != "Builder" || deref(s.Agent.URN) != "msg://agent/x/builder" || deref(s.Agent.LogicalAgentID) != "builder" {
		t.Errorf("agent identity: %+v", s.Agent)
	}
	if s.Context.Used == nil || *s.Context.Used != 150000 || *s.Context.Pct != 15 || s.Context.Source != "stream" || len(s.Compactions) != 1 || !s.Compactions[0].Inferred {
		t.Errorf("context: %+v %+v", s.Context, s.Compactions)
	}
	if !s.Tokens.Metered || *s.Tokens.Input != 210 || s.Tokens.Hour == nil || s.Tokens.Hour.Input != 210 {
		t.Errorf("tokens: %+v", s.Tokens)
	}
	type series struct {
		Points      []point      `json:"points"`
		Compactions []Compaction `json:"compactions"`
		Clears      []Clear      `json:"clears"`
		Warn        int          `json:"warn_pct"`
	}
	cs := decode[series](t, e.get("/sessions/m1/context-series"))
	if len(cs.Points) != 3 || cs.Points[1].Used != 300000 || cs.Points[1].Pct != 30 || cs.Warn != 90 || len(cs.Compactions) != 1 {
		t.Errorf("series: %+v", cs)
	}
	if cs.Points[0].At > cs.Points[1].At {
		t.Error("points are oldest first")
	}
	cut := decode[series](t, e.get(fmt.Sprintf("/sessions/m1/context-series?from=%d&to=%d", at(45).UnixMilli(), at(70).UnixMilli())))
	if len(cut.Points) != 1 {
		t.Errorf("from/to: %+v", cut.Points)
	}
	un := decode[series](t, e.get("/sessions/u1/context-series"))
	if len(un.Points) != 0 || len(un.Compactions) != 0 {
		t.Errorf("an unmetered session plots nothing: %+v", un)
	}
	for _, p := range []string{"step=zz", "from=x", "from=10&to=5"} {
		if e.get("/sessions/m1/context-series?"+p).Code != 400 {
			t.Errorf("?%s must be 400", p)
		}
	}

	tk := decode[map[string]any](t, e.get("/sessions/m1/tokens"))
	hour, sess := tk["hour"].(map[string]any), tk["session"].(map[string]any)
	if tk["metered"] != true || hour["input"] != float64(210) || sess["input"] != float64(210) || hour["tool_calls"] == nil {
		t.Errorf("tokens: %v", tk)
	}
	if sh, _ := tk["share"].(float64); sh < 0.5 || sh > 0.55 { // 320 of 320+300 input+output
		t.Errorf("share: %v", tk["share"])
	}
	if tk["trend"] != "up" && tk["trend"] != "down" && tk["trend"] != "flat" {
		t.Errorf("trend: %v", tk["trend"])
	}
	if u := decode[map[string]any](t, e.get("/sessions/u1/tokens")); u["metered"] != false || u["hour"] != nil || u["session"] != nil || u["share"] != nil || u["trend"] != nil {
		t.Errorf("unmetered tokens must be null, not zero: %v", u)
	}

	h := decode[struct {
		Sessions []map[string]any `json:"sessions"`
	}](t, e.get("/sessions/m1/history"))
	if len(h.Sessions) != 1 || h.Sessions[0]["session_id"] != "old1" || h.Sessions[0]["end_reason"] != "restart" || h.Sessions[0]["replaced_by"] != "m1" {
		t.Errorf("history: %v", h.Sessions)
	}
	if len(decode[struct {
		Sessions []any `json:"sessions"`
	}](t, e.get("/sessions/u1/history")).Sessions) != 0 {
		t.Error("a session Tether never tied to an agent has no history")
	}
}

// sse reads "id: / data:" frames from a streaming response until stop returns true or time runs out.
type sseFrame struct {
	id   string
	data map[string]any
}

func readSSE(t *testing.T, resp *http.Response, stop func([]sseFrame) bool) []sseFrame {
	t.Helper()
	var out []sseFrame
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		var id string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				var m map[string]any
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m)
				out = append(out, sseFrame{id, m})
				id = ""
				if stop != nil && stop(out) {
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatalf("stream timed out; got %v", out)
	}
	return out
}

func serve(e *env) *httptest.Server {
	mux := http.NewServeMux()
	e.srv.Mount(mux, func(h http.Handler) http.Handler { return h })
	return httptest.NewServer(mux)
}

func TestStreamSnapshotsReplaysAndRefusesBadIDs(t *testing.T) {
	e := newEnv(t)
	e.now = t0.Add(time.Hour)
	e.frame("session.work_state_changed", "s_work", 55*time.Minute, `{"to":"processing"}`)
	ts := serve(e)
	defer ts.Close()
	url := ts.URL + agentsapi.Prefix + "/stream?once=1"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	fr := readSSE(t, resp, nil)
	resp.Body.Close()
	if len(fr) != 2 || fr[0].data["type"] != "collector" || fr[1].data["type"] != "summary" || fr[0].id != "" {
		t.Fatalf("a fresh connection gets collector and summary snapshots: %v", fr)
	}
	// Bus events and a gap are replayable, in one id sequence.
	e.bus("deploy.state_changed", "cerberus", `{"resource":"tachyon","state":"succeeded","version":"v1"}`)
	if _, err := e.store.AddGap(context.Background(), agentscollector.Gap{Start: t0, End: t0.Add(time.Minute), Reason: "purged", FromSeq: 1, ToSeq: 2}); err != nil {
		t.Fatal(err)
	}
	e.bus("alert.raised", "cerberus", `{"name":"nanite health"}`)
	resp, _ = http.Get(ts.URL + agentsapi.Prefix + "/stream?once=1&last_event_id=0")
	all := readSSE(t, resp, nil)
	resp.Body.Close()
	if len(all) != 3 || all[0].data["type"] != "bus" || all[1].data["type"] != "gap" || all[2].data["type"] != "bus" {
		t.Fatalf("replay: %v", all)
	}
	if all[0].id != "1" || all[1].id != "2" || all[2].id != "3" {
		t.Errorf("ids: %v %v %v", all[0].id, all[1].id, all[2].id)
	}
	if ev := all[0].data["event"].(map[string]any); ev["type"] != "deploy" || ev["subject"] != "tachyon deployed (v1)" || all[0].data["at"] == nil {
		t.Errorf("bus payload: %v", all[0].data)
	}
	req, _ := http.NewRequest("GET", ts.URL+agentsapi.Prefix+"/stream?once=1", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, _ = http.DefaultClient.Do(req)
	after := readSSE(t, resp, nil)
	resp.Body.Close()
	if len(after) != 2 || after[0].id != "2" {
		t.Errorf("resume after 1: %v", after)
	}
	for _, f := range after {
		if f.data["type"] == "summary" || f.data["type"] == "session" || f.data["type"] == "collector" {
			t.Error("snapshots do not replay on resume")
		}
	}
	for _, bad := range []string{"x", "-1"} {
		req, _ = http.NewRequest("GET", ts.URL+agentsapi.Prefix+"/stream?once=1", nil)
		req.Header.Set("Last-Event-ID", bad)
		resp, _ = http.DefaultClient.Do(req)
		if resp.StatusCode != 400 {
			t.Errorf("Last-Event-ID %q: %d", bad, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// Pruned history is announced as a gap so the client refetches.
	e.exec(`DELETE FROM agents_stream WHERE id<3`)
	resp, _ = http.Get(ts.URL + agentsapi.Prefix + "/stream?once=1&last_event_id=1")
	pruned := readSSE(t, resp, nil)
	resp.Body.Close()
	if len(pruned) < 1 || pruned[0].data["type"] != "gap" || pruned[0].data["reason"] != "purged" {
		t.Errorf("pruned: %v", pruned)
	}
}

func TestStreamAnnouncesLiveChanges(t *testing.T) {
	e := seed(t)
	ts := serve(e)
	defer ts.Close()
	resp, err := http.Get(ts.URL + agentsapi.Prefix + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(300 * time.Millisecond) // after the connection and the baseline
		e.frame("session.work_state_changed", "s_idle", 59*time.Minute, `{"from":"idle","to":"processing"}`)
		e.bus("deploy.state_changed", "cerberus", `{"resource":"nanite","state":"failed"}`)
	}()
	var types = map[string]bool{}
	frames := readSSE(t, resp, func(fr []sseFrame) bool {
		for _, f := range fr {
			types[fmt.Sprint(f.data["type"])] = true
		}
		return types["session"] && types["summary"] && types["bus"]
	})
	var sess map[string]any
	for _, f := range frames {
		if f.data["type"] == "session" {
			sess = f.data["session"].(map[string]any)
		}
		if f.data["type"] == "bus" && f.id == "" {
			t.Error("bus events carry an SSE id")
		}
	}
	if sess["id"] != "s_idle" || sess["state"] != "working" {
		t.Errorf("session upsert: %v", sess)
	}
}

func TestStreamAnnouncesTimeTransitions(t *testing.T) {
	e := newEnv(t)
	e.now = t0.Add(10 * time.Minute)
	e.frame("session.work_state_changed", "s1", 5*time.Minute, `{"to":"processing"}`) // 5 min old: working
	ts := serve(e)
	defer ts.Close()
	resp, err := http.Get(ts.URL + agentsapi.Prefix + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(300 * time.Millisecond)
		e.now = t0.Add(time.Hour) // nothing arrived for an hour
	}()
	frames := readSSE(t, resp, func(fr []sseFrame) bool {
		for _, f := range fr {
			if f.data["type"] == "session" && f.data["session"].(map[string]any)["state"] == "stale" {
				return true
			}
		}
		return false
	})
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
}

func TestMountedRoutesAreExactlyTheDocumentedOnes(t *testing.T) {
	e := newEnv(t)
	hit := 0
	mux := http.NewServeMux()
	e.srv.Mount(mux, func(h http.Handler) http.Handler { hit++; return h })
	if hit != len(Routes) {
		t.Errorf("guarded %d of %d routes", hit, len(Routes))
	}
	for _, p := range []string{"/bus/events", "/collector"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "http://x"+agentsapi.Prefix+p, strings.NewReader("{}")))
		if w.Code != 404 && w.Code != 405 {
			t.Errorf("POST %s is not this package's: %d", p, w.Code)
		}
	}
}

package planner

import (
	"bufio"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ingestFixture(t *testing.T) (*HTTP, *Ingest, *torqueFake) {
	h, f := taskHTTP(t)
	return h, h.ingest(), f
}

func streamEvents(t *testing.T, h *HTTP) []string {
	t.Helper()
	rows, err := Events{DB: h.Store}.streamAfter(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Event)
	}
	return out
}

func TestTailDerivesLifecycleCommentDecisionAndLinkEvents(t *testing.T) {
	h, in, _ := ingestFixture(t)
	ctx := context.Background()
	if err := in.Tail(ctx); err != nil { // baseline: nothing to replay
		t.Fatal(err)
	}
	th := mk(t, h.Owned, "themes", "Agents", nil)
	other := mk(t, h.Owned, "themes", "Other", nil)
	dec := mk(t, h.Owned, "decisions", "Where?", Item{"priority_ids": []any{th["id"]}})
	ready, err := h.Owned.Action(ctx, "themes", th["id"].(string), revision(th), "ready", Item{}, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	withComment, err := h.Owned.Action(ctx, "themes", th["id"].(string), revision(ready), "comments", Item{"text": "Looks good to me"}, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Owned.AddRelation(ctx, PortfolioRef{"themes", th["id"].(string)}, "relates", PortfolioRef{"themes", other["id"].(string)}, revision(withComment), "operator:test"); err != nil {
		t.Fatal(err)
	}
	if err = in.Tail(ctx); err != nil {
		t.Fatal(err)
	}
	events, _, err := in.Events.Timeline(ctx, th["id"].(string), nil, "all", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, e := range events {
		kinds[e["kind"].(string)] += e["text"].(string) + ";"
	}
	if !strings.Contains(kinds["lifecycle"], "proposed → ready") || !strings.Contains(kinds["comment"], "operator:test: Looks good") || !strings.Contains(kinds["decision"], "Decision opened: Where?") || kinds["link"] == "" {
		t.Fatal("derived events:", kinds)
	}
	_ = dec
	// A second pass adds nothing; the stream starts with planner.changed and carries planner.item entries.
	before := len(events)
	if err = in.Tail(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "all", 0, 50); len(again) != before {
		t.Fatal("tail must be idempotent")
	}
	got := streamEvents(t, h)
	if got[0] != "planner.changed" || !strings.Contains(strings.Join(got, ","), "planner.item") || !strings.Contains(strings.Join(got, ","), "planner.event") {
		t.Fatal(got)
	}
	// Facet and paging.
	only, _, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "comments", 0, 50)
	if len(only) != 1 {
		t.Fatal("facet", only)
	}
	page, next, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "all", 0, 2)
	if len(page) != 2 || next == nil {
		t.Fatal("paging", page, next)
	}
	rest, end, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "all", next.(int64), 50)
	if len(rest) == 0 || end != nil || rest[0]["seq"].(int64) >= next.(int64) {
		t.Fatal("before_seq pages backwards", rest, end)
	}
}

func TestPollTorqueBaselineThenMovesAndBlockers(t *testing.T) {
	h, in, f := ingestFixture(t)
	ctx := context.Background()
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner"})
	th := mk(t, h.Owned, "themes", "T", Item{"workstream_ids": []any{ws["id"]}})
	if err := in.PollTorque(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "all", 0, 50); len(e) != 0 {
		t.Fatal("first sight is a baseline, not an event", e)
	}
	f.mu.Lock()
	f.status["CW-1"], f.status["CW-3"] = "review", "blocked"
	f.mu.Unlock()
	if err := in.PollTorque(ctx); err != nil {
		t.Fatal(err)
	}
	if err := in.PollTorque(ctx); err != nil { // unchanged: idempotent
		t.Fatal(err)
	}
	e, _, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "all", 0, 50)
	if len(e) != 2 {
		t.Fatal(e)
	}
	byKind := map[string]Item{}
	for _, x := range e {
		byKind[x["kind"].(string)] = x
	}
	if d := byKind["task"]["delta"].(map[string]any); number(d["doing"]) != -1 || number(d["review"]) != 1 || byKind["task"]["arrow"] != "up" {
		t.Fatal(byKind["task"])
	}
	if byKind["blocker"]["arrow"] != "down" || !strings.HasPrefix(byKind["blocker"]["text"].(string), "Blocked:") {
		t.Fatal(byKind["blocker"])
	}
	got := streamEvents(t, h)
	if !strings.Contains(strings.Join(got, ","), "planner.task") {
		t.Fatal(got)
	}
}

func TestPollPRsMatchesTheTaskInTheTitle(t *testing.T) {
	h, in, _ := ingestFixture(t)
	ctx := context.Background()
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner"})
	th := mk(t, h.Owned, "themes", "T", Item{"workstream_ids": []any{ws["id"]}})
	if err := in.PollTorque(ctx); err != nil {
		t.Fatal(err)
	}
	// The planner knows task ids once PollTorque has mapped them; use a task id of the right shape.
	if err := h.Store.Writer.Submit(ctx, "seed", func(ctx context.Context, tx *sql.Tx) error {
		if err := statePut(ctx, tx, "tasktheme:CW-20261030-0202", th["id"].(string)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO alerts(alert_id,seq,kind,severity,severity_base,title,source,first_at_ms,last_at_ms,subjects_json,created_ms,updated_ms) VALUES('a1',1,'pr.merged','info','info','CW-20261030-0202 usage index','forge',1000,2000,'[{"kind":"pr","ref":"hollis-labs/tachyon#229"}]',1,1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := in.PollPRs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := in.PollPRs(ctx); err != nil {
		t.Fatal(err)
	}
	e, _, _ := in.Events.Timeline(ctx, th["id"].(string), nil, "prs", 0, 10)
	if len(e) != 1 || !strings.HasPrefix(e[0]["text"].(string), "PR merged:") || e[0]["arrow"] != "up" {
		t.Fatal(e)
	}
}

func TestTimelineRouteMilestonesAndErrors(t *testing.T) {
	h, in, f := ingestFixture(t)
	ctx := context.Background()
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner"})
	th := mk(t, h.Owned, "themes", "T", Item{"workstream_ids": []any{ws["id"]}})
	_ = in.PollTorque(ctx)
	f.mu.Lock()
	f.status["CW-3"] = "done"
	f.mu.Unlock()
	_ = in.PollTorque(ctx)
	code, out := h.do(t, "GET", "/themes/"+th["id"].(string)+"/timeline", "")
	if code != 200 {
		t.Fatal(code, out)
	}
	ms := array(out["milestones"])
	states := []string{}
	for _, m := range ms {
		states = append(states, object(m)["key"].(string)+"="+object(m)["state"].(string))
	}
	// 2 of 4 lane tasks are done (CW-2 and CW-3): approved is next, first_done and half_done are reached.
	if strings.Join(states, ",") != "approved=next,first_done=reached,half_done=reached" {
		t.Fatal(states)
	}
	if len(array(out["events"])) != 1 {
		t.Fatal(out)
	}
	if code, _ = h.do(t, "GET", "/themes/"+th["id"].(string)+"/timeline?facet=bogus", ""); code != 400 {
		t.Fatal(code)
	}
	if code, _ = h.do(t, "GET", "/themes/"+uuid.NewString()+"/timeline", ""); code != 404 {
		t.Fatal(code)
	}
	if code, out = h.do(t, "GET", "/workstreams/"+ws["id"].(string)+"/timeline", ""); code != 200 || len(array(out["events"])) != 1 || out["milestones"] != nil {
		t.Fatal(code, out)
	}
}

func TestStreamReplaysAfterLastEventIDAndResyncs(t *testing.T) {
	h, in, _ := ingestFixture(t)
	ctx := context.Background()
	_ = in.Tail(ctx)
	mk(t, h.Owned, "ideas", "one", nil)
	mk(t, h.Owned, "ideas", "two", nil)
	_ = in.Tail(ctx)
	srv := httptest.NewServer(h)
	defer srv.Close()
	read := func(lastID string, n int) []string {
		req, _ := http.NewRequest("GET", srv.URL+Prefix+"/events", nil)
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		frames := []string{}
		for sc.Scan() && len(frames) < n {
			if strings.HasPrefix(sc.Text(), "event: ") {
				frames = append(frames, strings.TrimPrefix(sc.Text(), "event: "))
			}
		}
		return frames
	}
	if got := read("0", 3); strings.Join(got, ",") != "planner.changed,planner.item,planner.item" {
		t.Fatal("replay from the start:", got)
	}
	if got := read("999999", 1); got[0] != "resync" {
		t.Fatal("a cursor beyond the ring resyncs:", got)
	}
	if got := read("", 1); got[0] != "planner.changed" {
		t.Fatal("a fresh connection announces the current state:", got)
	}
}

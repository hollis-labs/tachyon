package planner

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The tables below are the derivation spec: change a rule, change a row.
func TestDeriveMomentumTable(t *testing.T) {
	c := DefaultMomentum()
	stale := float64(7 * 24)
	cases := []struct {
		name  string
		s     Signals
		label string
		text  string
	}{
		{"fresh output rises", Signals{RecentDone: 3, RecentPRs: 1, RecentActivity: 2, PriorDone: 1, HoursSinceLast: 2, StaleHours: stale}, "rising", "Rising: 3 tasks done and 1 PR merged and 2 updates in 24h."},
		{"a blocker weighs against", Signals{RecentDone: 3, RecentPRs: 1, PriorDone: 1, Blocked: 1, HoursSinceLast: 2, StaleHours: stale}, "rising", "Rising: 3 tasks done and 1 PR merged in 24h, 1 blocker weighs against."},
		{"output fell off", Signals{RecentActivity: 1, PriorDone: 2, PriorPRs: 1, HoursSinceLast: 20, StaleHours: stale}, "cooling", "Cooling: 1 update in 24h."},
		{"blocked and nothing moving", Signals{Blocked: 2, HoursSinceLast: 30, StaleHours: stale}, "cooling", "Cooling: nothing moved in 24h, 2 blockers weigh against."},
		{"steady pace", Signals{RecentDone: 1, RecentActivity: 2, PriorDone: 1, PriorActivity: 2, HoursSinceLast: 5, StaleHours: stale}, "steady", "Steady: 1 task done and 2 updates in 24h."},
		{"quiet but recent enough", Signals{HoursSinceLast: 60, StaleHours: stale}, "steady", "Steady: nothing moved in 24h."},
		{"silence is stalled", Signals{HoursSinceLast: 200, StaleHours: stale}, "stalled", "Stalled: no activity for 8 days."},
		{"never any activity", Signals{HoursSinceLast: -1, StaleHours: stale}, "stalled", "Stalled: no activity recorded yet."},
	}
	for _, tc := range cases {
		m := DeriveMomentum(c, tc.s)
		if m.Label != tc.label || m.Text != tc.text || m.Score < 0 || m.Score > 1 || len(m.Signals) != 4 {
			t.Errorf("%s: got %s %q %.2f", tc.name, m.Label, m.Text, m.Score)
		}
	}
}

func TestDeriveNeedYouTable(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name string
		f    NeedFacts
		want string
	}{
		{"proposed theme awaits approval", NeedFacts{DB: "priorities", Status: "proposed", Idle: 0, StaleAfter: 7 * day}, "review"},
		{"tasks in review", NeedFacts{DB: "priorities", Status: "approved", ReviewTasks: 2, StaleAfter: 7 * day}, "review"},
		{"open decision and review", NeedFacts{DB: "priorities", Status: "ready", OpenDecision: true, StaleAfter: 7 * day}, "review,decide"},
		{"blocked task", NeedFacts{DB: "priorities", Status: "approved", BlockedTasks: 1, StaleAfter: 7 * day}, "blocked"},
		{"approved and idle too long", NeedFacts{DB: "priorities", Status: "approved", Idle: 8 * day, StaleAfter: 7 * day}, "stale"},
		{"approved and recently active", NeedFacts{DB: "priorities", Status: "approved", Idle: day, StaleAfter: 7 * day}, ""},
		{"paused theme is not stale", NeedFacts{DB: "priorities", Status: "paused", Idle: 30 * day, StaleAfter: 7 * day}, ""},
		{"proposed decision", NeedFacts{DB: "decisions", Status: "proposed"}, "decide"},
		{"accepted decision", NeedFacts{DB: "decisions", Status: "accepted"}, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(DeriveNeedYou(tc.f), ","); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestSettingsDefaultsAndOverrides(t *testing.T) {
	h, _ := taskHTTP(t)
	ctx := context.Background()
	if s := LoadSettings(ctx, h.Store); s.StaleDays != 7 || s.RetentionDays != 180 || s.Momentum.WindowHours != 24 {
		t.Fatal(s)
	}
	put := func(k, v string) {
		if err := h.Store.Writer.Submit(ctx, "seed", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("planner.stale_days", "3")
	put("planner.timeline_retention_days", "5") // below the 7-day floor: ignored
	put("planner.momentum", `{"window_hours":48,"done_task":2,"pr_merged":1,"activity":0.2,"blocker":1,"rising":3,"cooling":1}`)
	s := LoadSettings(ctx, h.Store)
	if s.StaleDays != 3 || s.RetentionDays != 180 || s.Momentum.WindowHours != 48 || s.Momentum.DoneTask != 2 {
		t.Fatal(s)
	}
	put("planner.momentum", `{"window_hours":0}`)
	if LoadSettings(ctx, h.Store).Momentum.WindowHours != 24 {
		t.Fatal("an out-of-range momentum config keeps the defaults")
	}
}

func TestNeedYouMomentumRoutesFilterAndTimelineMomentum(t *testing.T) {
	h, in, f := ingestFixture(t)
	ctx := context.Background()
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner"})
	active := mk(t, h.Owned, "themes", "Active", Item{"workstream_ids": []any{ws["id"]}})
	if _, err := h.Owned.Action(ctx, "themes", active["id"].(string), revision(active), "ready", Item{}, "operator:test"); err != nil {
		t.Fatal(err)
	}
	proposed := mk(t, h.Owned, "themes", "Proposed", nil)
	dec := mk(t, h.Owned, "decisions", "Open?", Item{"priority_ids": []any{active["id"]}})
	_ = in.Tail(ctx)
	_ = in.PollTorque(ctx)
	f.mu.Lock()
	f.status["CW-1"], f.status["CW-3"] = "review", "blocked"
	f.mu.Unlock()
	_ = in.PollTorque(ctx)

	code, out := h.do(t, "GET", "/need-you", "")
	if code != 200 {
		t.Fatal(code, out)
	}
	by := object(out["by_reason"])
	if number(by["review"]) < 2 || number(by["decide"]) != 2 || number(by["blocked"]) != 1 || number(object(out["by_lens"])["decisions"]) != 1 || number(out["stale_days"]) != 7 {
		t.Fatal("need-you", out)
	}
	seen := map[string]string{}
	for _, it := range array(out["items"]) {
		seen[object(it)["id"].(string)] = strings.Join(toStrings(array(object(it)["reasons"])), ",")
	}
	if seen[active["id"].(string)] != "review,decide,blocked" || seen[dec["id"].(string)] != "decide" || seen[proposed["id"].(string)] != "review" {
		t.Fatal(seen)
	}
	// list filter and additive fields
	code, _ = h.do(t, "GET", "/themes?need_you=bogus", "")
	if code != 400 {
		t.Fatal(code)
	}
	_, w := listRaw(h, "/themes?need_you=blocked")
	if !strings.Contains(w, active["id"].(string)) || strings.Contains(w, proposed["id"].(string)) || !strings.Contains(w, `"momentum"`) || !strings.Contains(w, `"need_you"`) {
		t.Fatal(w)
	}
	// momentum
	code, out = h.do(t, "GET", "/momentum", "")
	if code != 200 || out["label"] == nil || number(object(out["by_label"])["stalled"])+number(object(out["by_label"])["steady"])+number(object(out["by_label"])["cooling"])+number(object(out["by_label"])["rising"]) != 1 {
		t.Fatal(code, out)
	}
	if ing := object(out["ingest"]); ing["degraded"] != true || object(ing["sources"])["torque"] != "ok" {
		t.Fatal("no worker pass has run, so ingest is degraded:", ing)
	}
	// timeline carries momentum with signals and series
	code, out = h.do(t, "GET", "/themes/"+active["id"].(string)+"/timeline", "")
	m := object(out["momentum"])
	if code != 200 || m["label"] == nil || len(array(m["signals"])) != 4 || len(array(m["series"])) != 7 {
		t.Fatal(code, out)
	}
	// the stream announces need_you after a pass
	in.announceNeedYou(ctx)
	if got := streamEvents(t, h); !strings.Contains(strings.Join(got, ","), "planner.need_you") {
		t.Fatal(got)
	}
}

func toStrings(a []any) []string {
	out := []string{}
	for _, v := range a {
		out = append(out, v.(string))
	}
	return out
}

func listRaw(h *HTTP, path string) (int, string) {
	req := httptest.NewRequest("GET", Prefix+path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

// MomentumConfig holds the tunable weights and thresholds of the momentum label. They are
// placeholders ("tune after seeing data"); app_settings key planner.momentum (JSON) overrides them.
type MomentumConfig struct {
	WindowHours int     `json:"window_hours"`
	DoneTask    float64 `json:"done_task"`
	PRMerged    float64 `json:"pr_merged"`
	Activity    float64 `json:"activity"`
	Blocker     float64 `json:"blocker"` // weight per currently blocked task, applied as a negative
	Rising      float64 `json:"rising"`  // recent score at or above this, and above the prior window, is rising
	Cooling     float64 `json:"cooling"` // recent score below this, with a prior window above it, is cooling
}

// PlannerSettings are the app_settings the planner derivations read, with their defaults.
type PlannerSettings struct {
	Momentum      MomentumConfig
	StaleDays     int
	RetentionDays int
}

func DefaultMomentum() MomentumConfig {
	return MomentumConfig{WindowHours: 24, DoneTask: 1.0, PRMerged: 0.8, Activity: 0.1, Blocker: 0.5, Rising: 2.0, Cooling: 1.0}
}

// LoadSettings reads planner.stale_days (1-365), planner.timeline_retention_days (7-3650) and the
// planner.momentum JSON from app_settings; anything missing or out of range keeps the default.
func LoadSettings(ctx context.Context, db *localstore.Store) PlannerSettings {
	s := PlannerSettings{Momentum: DefaultMomentum(), StaleDays: plannerapi.DefaultStaleDays, RetentionDays: plannerapi.RetentionDays}
	if db == nil {
		return s
	}
	get := func(key string) string {
		var v string
		_ = db.Reader.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
		return v
	}
	if n, err := strconv.Atoi(get("planner.stale_days")); err == nil && n >= 1 && n <= 365 {
		s.StaleDays = n
	}
	if n, err := strconv.Atoi(get("planner.timeline_retention_days")); err == nil && n >= 7 && n <= 3650 {
		s.RetentionDays = n
	}
	if raw := get("planner.momentum"); raw != "" {
		c := s.Momentum
		if json.Unmarshal([]byte(raw), &c) == nil && c.WindowHours >= 1 && c.WindowHours <= 24*30 {
			s.Momentum = c
		}
	}
	return s
}

// Signals are the counts momentum is derived from. Recent* is the latest window, Prior* the one before.
type Signals struct {
	RecentDone, RecentPRs, RecentActivity int
	PriorDone, PriorPRs, PriorActivity    int
	Blocked                               int
	HoursSinceLast                        float64 // -1: no event ever
	StaleHours                            float64
}

// Momentum is the derived label with the signals that produced it.
type Momentum struct {
	Label   string
	Score   float64
	Text    string
	Signals []Item
}

func (c MomentumConfig) score(done, prs, activity int) float64 {
	return float64(done)*c.DoneTask + float64(prs)*c.PRMerged + float64(activity)*c.Activity
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// DeriveMomentum is a pure function: weighted signals of the recent window against the prior one, with
// blockers weighing against and a long silence meaning stalled. No dates and no forecast.
//
//	stalled  no event for StaleHours (or ever)
//	rising   recent score >= Rising and above the prior window
//	cooling  recent score < Cooling while the prior window was at least Cooling, or blockers cancel it
//	steady   otherwise
func DeriveMomentum(c MomentumConfig, s Signals) Momentum {
	recent := c.score(s.RecentDone, s.RecentPRs, s.RecentActivity)
	prior := c.score(s.PriorDone, s.PriorPRs, s.PriorActivity)
	net := recent - float64(s.Blocked)*c.Blocker
	m := Momentum{Score: clamp01(net / (c.Rising * 1.5))}
	window := fmt.Sprintf("%dh", c.WindowHours)
	m.Signals = []Item{
		{"kind": "done_tasks", "count": s.RecentDone, "window": window, "weight": c.DoneTask, "effect": effect(s.RecentDone > 0)},
		{"kind": "prs_merged", "count": s.RecentPRs, "window": window, "weight": c.PRMerged, "effect": effect(s.RecentPRs > 0)},
		{"kind": "activity", "count": s.RecentActivity, "window": window, "weight": c.Activity, "effect": effect(s.RecentActivity > 0)},
		{"kind": "blockers", "count": s.Blocked, "window": "now", "weight": -c.Blocker, "effect": map[bool]string{true: "down", false: "none"}[s.Blocked > 0]},
	}
	switch {
	case s.HoursSinceLast < 0 || s.HoursSinceLast >= s.StaleHours:
		m.Label = "stalled"
	case net >= c.Rising && net > prior:
		m.Label = "rising"
	case (net < c.Cooling && prior >= c.Cooling) || (s.Blocked > 0 && net <= 0):
		m.Label = "cooling"
	default:
		m.Label = "steady"
	}
	parts := []string{}
	if s.RecentDone > 0 {
		parts = append(parts, plural(s.RecentDone, "task", "tasks")+" done")
	}
	if s.RecentPRs > 0 {
		parts = append(parts, plural(s.RecentPRs, "PR", "PRs")+" merged")
	}
	if s.RecentActivity > 0 {
		parts = append(parts, plural(s.RecentActivity, "update", "updates"))
	}
	text := strings.ToUpper(m.Label[:1]) + m.Label[1:]
	switch {
	case m.Label == "stalled" && s.HoursSinceLast < 0:
		text += ": no activity recorded yet."
	case m.Label == "stalled":
		text += fmt.Sprintf(": no activity for %d days.", int(s.HoursSinceLast/24))
	case len(parts) == 0:
		text += fmt.Sprintf(": nothing moved in %s.", window)
	default:
		text += ": " + strings.Join(parts, " and ") + " in " + window + "."
	}
	if s.Blocked > 0 && m.Label != "stalled" {
		verb := map[bool]string{true: "weighs", false: "weigh"}[s.Blocked == 1]
		text = strings.TrimSuffix(text, ".") + fmt.Sprintf(", %s %s against.", plural(s.Blocked, "blocker", "blockers"), verb)
	}
	m.Text = text
	return m
}

func effect(up bool) string { return map[bool]string{true: "up", false: "none"}[up] }

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return float64(int(f*100+0.5)) / 100
}

// NeedFacts are the facts need-you reasons are derived from for one item.
type NeedFacts struct {
	DB           string
	Status       string
	ReviewTasks  int
	BlockedTasks int
	OpenDecision bool
	Idle         time.Duration // since the last event; -1 when none
	StaleAfter   time.Duration
}

// DeriveNeedYou returns the reasons an item needs the operator, in the order review, decide, blocked,
// stale. The derivation lives here, in one function:
//
//	review   a theme waiting for approval (proposed or ready), or tasks in the Review lane
//	decide   an open decision linked to the theme; a proposed decision itself
//	blocked  any blocked task
//	stale    an approved theme in flight with no event for StaleAfter (an inactivity flag, not a forecast)
func DeriveNeedYou(f NeedFacts) []string {
	out := []string{}
	if (f.DB == "priorities" && (f.Status == "proposed" || f.Status == "ready")) || f.ReviewTasks > 0 {
		out = append(out, "review")
	}
	if f.OpenDecision || (f.DB == "decisions" && f.Status == "proposed") {
		out = append(out, "decide")
	}
	if f.BlockedTasks > 0 {
		out = append(out, "blocked")
	}
	if f.DB == "priorities" && f.Status == "approved" && f.Idle >= f.StaleAfter {
		out = append(out, "stale")
	}
	return out
}

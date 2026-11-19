// Package alertsrules is the server-side alert rule evaluator (Alerts AL2). The rule pack is JSON data in
// the same schema as the console UI's rules/types.ts plus alert.needsYou; the first enabled matching rule
// wins. It must pass internal/alertsapi/rulepack/conformance.json, the same table the TS evaluator runs
// until it is deleted. No code runs from a pack: templates read a closed scope.
package alertsrules

import (
	"encoding/json"
	"fmt"
	"slices"
)

var severities = []string{"debug", "info", "notice", "warning", "error", "critical"}

var icons = []string{"info", "task", "agent", "message", "approval", "pr", "warning", "error"}

func sevIdx(s string) int { return slices.Index(severities, s) }

type Pack struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Label   string `json:"label"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Enabled     *bool       `json:"enabled"`
	Match       Match       `json:"match"`
	Action      string      `json:"action"`
	Alert       *AlertSpec  `json:"alert"`
	Coalesce    *CoalesceSp `json:"coalesce"`
	Escalate    *EscalateSp `json:"escalate"`
}

type Match struct {
	Kinds        []string                   `json:"kinds"`
	Sources      []string                   `json:"sources"`
	MinSeverity  string                     `json:"minSeverity"`
	Severities   []string                   `json:"severities"`
	Categories   []string                   `json:"categories"`
	Labels       []string                   `json:"labels"`
	SubjectTypes []string                   `json:"subjectTypes"`
	Payload      map[string]json.RawMessage `json:"payload"`
}

type AlertSpec struct {
	Severity string `json:"severity"`
	Icon     string `json:"icon"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	NeedsYou bool   `json:"needsYou"`
}

type CoalesceSp struct {
	Key       string `json:"key"`
	WindowSec *int   `json:"windowSec"`
}

type EscalateSp struct {
	AfterSec int    `json:"afterSec"`
	To       string `json:"to"`
}

// ParsePack validates an untrusted pack. An invalid rule is skipped and reported; the rest still load. A pack
// with an unsupported version loads no rules. needsYou on a rule with no kinds (a catch-all) is cleared and
// reported: unknown kinds are never "needs you". Callers keep the previous pack when problems is non-empty
// and they want strictness; AL2 only reports.
func ParsePack(raw []byte) (Pack, []string) {
	var p Pack
	var problems []string
	if err := json.Unmarshal(raw, &p); err != nil {
		return Pack{}, []string{"pack is not valid JSON: " + err.Error()}
	}
	if p.Version != 1 {
		return Pack{ID: p.ID, Label: p.Label}, []string{fmt.Sprintf("unsupported pack version %d", p.Version)}
	}
	if p.ID == "" {
		p.ID = "unnamed"
	}
	seen := map[string]bool{}
	var keep []Rule
	for i, r := range p.Rules {
		id := r.ID
		if id == "" {
			id = fmt.Sprintf("#%d", i)
		}
		bad := func(why string) { problems = append(problems, "rule "+id+": "+why) }
		switch {
		case r.ID == "":
			bad("missing id")
		case seen[r.ID]:
			bad("duplicate id")
		case r.Match.MinSeverity != "" && sevIdx(r.Match.MinSeverity) < 0:
			bad("bad match.minSeverity")
		case r.Action == "drop":
			seen[r.ID] = true
			keep = append(keep, r)
		case r.Action != "" && r.Action != "alert":
			bad("action must be alert or drop")
		case r.Alert == nil || r.Alert.Title == "":
			bad("alert.title required")
		case r.Alert.Severity != "" && r.Alert.Severity != "inherit" && sevIdx(r.Alert.Severity) < 0:
			bad("bad alert.severity")
		case r.Alert.Icon != "" && !slices.Contains(icons, r.Alert.Icon):
			bad("bad alert.icon")
		case r.Coalesce != nil && r.Coalesce.Key == "":
			bad("coalesce.key required")
		case r.Escalate != nil && (r.Escalate.AfterSec <= 0 || sevIdx(r.Escalate.To) < 0):
			bad("bad escalate")
		default:
			if r.Alert.NeedsYou && len(r.Match.Kinds) == 0 {
				bad("needsYou needs match.kinds; cleared")
				r.Alert.NeedsYou = false
			}
			seen[r.ID] = true
			keep = append(keep, r)
		}
	}
	p.Rules = keep
	return p, problems
}

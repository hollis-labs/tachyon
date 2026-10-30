package alertsrules

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Caps on rendered text (design section 4). The TS evaluator caps at 140; the server is stricter.
const (
	TitleMax   = 120
	SummaryMax = 300
)

// Event is the console envelope the TS evaluator takes. The ingest adapters build it from normalized events
// (subject kind -> Type, ref -> ID).
type Event struct {
	ID       string         `json:"id"`
	TS       int64          `json:"ts"`
	Kind     string         `json:"kind"`
	Source   Source         `json:"source"`
	Subjects []Subject      `json:"subjects"`
	Severity string         `json:"severity"`
	Payload  map[string]any `json:"payload"`
	Class    *Class         `json:"class"`
}

type Source struct {
	ID      string `json:"id"`
	Machine string `json:"machine"`
}

type Subject struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Class struct {
	Category string   `json:"category"`
	Labels   []string `json:"labels"`
}

// Alert is what a matching alert rule produces. Escalate is nil unless the target severity is higher.
type Alert struct {
	Severity    string      `json:"severity"`
	Icon        string      `json:"icon"`
	Title       string      `json:"title"`
	Summary     string      `json:"summary"`
	CoalesceKey string      `json:"coalesce_key"`
	WindowSec   *int        `json:"coalesce_window_sec,omitempty"`
	NeedsYou    bool        `json:"needs_you"`
	Escalate    *EscalateAt `json:"escalate"`
}

type EscalateAt struct {
	AfterSec int    `json:"after_sec"`
	To       string `json:"to"`
}

// Result: RuleID is the first enabled matching rule (nil when none matches); Alert is nil for a drop or no match.
type Result struct {
	RuleID *string `json:"rule_id"`
	Alert  *Alert  `json:"alert"`
}

// Evaluate runs the pack over one event.
func (p Pack) Evaluate(e Event) Result {
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Enabled != nil && !*r.Enabled || !r.Match.matches(e) {
			continue
		}
		id := r.ID
		if r.Action == "drop" || r.Alert == nil {
			return Result{RuleID: &id}
		}
		return Result{RuleID: &id, Alert: r.toAlert(e)}
	}
	return Result{}
}

func (r *Rule) toAlert(e Event) *Alert {
	a := r.Alert
	sev := a.Severity
	if sev == "" || sev == "inherit" {
		sev = e.Severity
	}
	icon := a.Icon
	if icon == "" {
		icon = genericIcon(sev)
	}
	sc := scopeOf(e)
	title := oneLine(render(a.Title, sc), TitleMax)
	if title == "" {
		title = oneLine(e.Kind, TitleMax)
	}
	out := &Alert{Severity: sev, Icon: icon, Title: title, NeedsYou: a.NeedsYou}
	if a.Detail != "" {
		out.Summary = oneLine(render(a.Detail, sc), SummaryMax)
	}
	if r.Coalesce != nil {
		out.CoalesceKey = render(r.Coalesce.Key, sc)
		if out.CoalesceKey != "" {
			out.WindowSec = r.Coalesce.WindowSec
		}
	}
	if r.Escalate != nil && sevIdx(r.Escalate.To) > sevIdx(sev) {
		out.Escalate = &EscalateAt{AfterSec: r.Escalate.AfterSec, To: r.Escalate.To}
	}
	return out
}

func genericIcon(sev string) string {
	switch sev {
	case "error", "critical":
		return "error"
	case "warning":
		return "warning"
	}
	return "info"
}

func (m Match) matches(e Event) bool {
	if len(m.Kinds) > 0 && !slices.ContainsFunc(m.Kinds, func(k string) bool { return kindMatches(k, e.Kind) }) {
		return false
	}
	if len(m.Sources) > 0 && !slices.Contains(m.Sources, e.Source.ID) {
		return false
	}
	if m.MinSeverity != "" && sevIdx(e.Severity) < sevIdx(m.MinSeverity) {
		return false
	}
	if len(m.Severities) > 0 && !slices.Contains(m.Severities, e.Severity) {
		return false
	}
	if len(m.Categories) > 0 && !(e.Class != nil && e.Class.Category != "" && slices.Contains(m.Categories, e.Class.Category)) {
		return false
	}
	if len(m.Labels) > 0 && !(e.Class != nil && slices.ContainsFunc(m.Labels, func(l string) bool { return slices.Contains(e.Class.Labels, l) })) {
		return false
	}
	if len(m.SubjectTypes) > 0 && !slices.ContainsFunc(e.Subjects, func(s Subject) bool { return slices.Contains(m.SubjectTypes, s.Type) }) {
		return false
	}
	for path, raw := range m.Payload {
		v, _ := getPath(map[string]any{"payload": e.Payload}, "payload."+path)
		if !condHolds(raw, v) {
			return false
		}
	}
	return true
}

// kindMatches: "pr.*" matches any kind with the "pr." prefix; otherwise exact.
func kindMatches(pattern, kind string) bool {
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(kind, pattern[:len(pattern)-1])
	}
	return pattern == kind
}

func getPath(cur any, path string) (any, bool) {
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// scalarEq compares a JSON scalar from the pack with a decoded event value (numbers are float64).
func scalarEq(want, got any) bool {
	if gf, ok := got.(float64); ok {
		wf, ok := want.(float64)
		return ok && wf == gf
	}
	return want == got && want != nil
}

func condHolds(raw json.RawMessage, v any) bool {
	var c any
	if json.Unmarshal(raw, &c) != nil {
		return false
	}
	obj, ok := c.(map[string]any)
	if !ok {
		return scalarEq(c, v)
	}
	if ex, has := obj["exists"].(bool); has && (v != nil) != ex {
		return false
	}
	if eq, has := obj["eq"]; has && !scalarEq(eq, v) {
		return false
	}
	if in, has := obj["in"].([]any); has && !slices.ContainsFunc(in, func(w any) bool { return scalarEq(w, v) }) {
		return false
	}
	for key, cmp := range map[string]func(a, b float64) bool{"gte": func(a, b float64) bool { return a >= b }, "lte": func(a, b float64) bool { return a <= b }} {
		if w, has := obj[key].(float64); has {
			f, isNum := v.(float64)
			if !isNum || !cmp(f, w) {
				return false
			}
		}
	}
	return true
}

func scopeOf(e Event) map[string]any {
	var agent, subj *Subject
	for i := range e.Subjects {
		if e.Subjects[i].Type == "agent" && agent == nil {
			agent = &e.Subjects[i]
		}
		if e.Subjects[i].Type != "agent" && subj == nil {
			subj = &e.Subjects[i]
		}
	}
	if subj == nil && len(e.Subjects) > 0 {
		subj = &e.Subjects[0]
	}
	first := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	sub := map[string]any{"label": "", "id": "", "type": ""}
	if subj != nil {
		sub = map[string]any{"label": first(subj.Label, subj.ID), "id": subj.ID, "type": subj.Type}
	}
	who := first(e.Source.ID)
	ag := ""
	if agent != nil {
		ag = first(agent.Label, agent.ID)
		who = ag
	}
	if e.Source.Machine != "" {
		who += " · " + e.Source.Machine
	}
	payload := map[string]any{}
	if e.Payload != nil {
		payload = e.Payload
	}
	return map[string]any{"payload": payload, "subject": sub, "agent": ag, "source": who, "kind": e.Kind, "severity": e.Severity}
}

var tplRE = regexp.MustCompile(`\{\{\s*([\w.,\s]+?)\s*(?:\|([^{}]*))?\}\}`)

// render substitutes {{path}}, {{a,b}} (first non-empty) and {{path|fallback}} over a closed scope.
// Non-scalars render empty. The result is single-spaced and trimmed.
func render(tpl string, scope map[string]any) string {
	out := tplRE.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := tplRE.FindStringSubmatch(m)
		for _, path := range strings.Split(sub[1], ",") {
			v, _ := getPath(scope, strings.TrimSpace(path))
			switch t := v.(type) {
			case string:
				if s := strings.TrimSpace(t); s != "" {
					return s
				}
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(t)
			}
		}
		return strings.TrimSpace(sub[2])
	})
	return collapse(out)
}

// collapse makes untrusted text one line: whitespace runs become one space, other control characters go.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
		case unicode.IsControl(r):
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

func oneLine(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

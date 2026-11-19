package alertsapi

import (
	"strings"
	"unicode"
)

// SubjectView is what an alert is about, already readable (display fields; the raw Subjects stay as they were).
type SubjectView struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Link is a chip on an alert row or drawer. Href is set only for an in-app route.
type Link struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
	Href  string `json:"href,omitempty"`
}

var sourceLabels = map[string]string{
	"approvals": "Approvals", "torque": "Torque", "tether": "Tether", "agents": "Agents", "activity": "Activity",
	"forge": "Pull requests", "cerberus": "Cerberus", "tachyon": "Tachyon",
}

var linkKinds = map[string]bool{"task": true, "theme": true, "session": true, "pr": true, "approval": true}

// Humanize turns an id-like word into plain words: snake_case and kebab-case become spaces, the first letter is upper case.
func Humanize(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(s))
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Decorate fills the optional display fields from what the alert already holds. Each stays empty when it cannot be
// said better than the existing title, source and subjects; consumers fall back to those.
func Decorate(a *Alert) {
	if a.Source != "" {
		if l, ok := sourceLabels[a.Source]; ok {
			a.SourceLabel = l
		} else {
			a.SourceLabel = Humanize(a.Source)
		}
	}
	if len(a.Subjects) > 0 {
		s := a.Subjects[0]
		for _, c := range a.Subjects {
			if c.Kind != "approval" {
				s = c
				break
			}
		}
		label := s.Label
		if label == "" {
			label = s.Ref
		}
		a.SubjectView = &SubjectView{Kind: s.Kind, ID: s.Ref, Label: label}
	}
	for _, s := range a.Subjects {
		if !linkKinds[s.Kind] || s.Ref == "" {
			continue
		}
		l := Link{Kind: s.Kind, Ref: s.Ref, Label: s.Label}
		if l.Label == "" {
			l.Label = s.Ref
		}
		if strings.HasPrefix(s.URL, "/") && !strings.HasPrefix(s.URL, "//") {
			l.Href = s.URL
		}
		a.Links = append(a.Links, l)
	}
	if a.Kind == "approval.requested" && a.Title != "" {
		a.DisplayTitle = "Needs your decision: " + a.Title
	}
}

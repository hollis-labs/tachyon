package objects

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Pin is the lens-wide pin: pinned items sort first, by pinned_at. It is a typed field on all
// seven portfolio records so a pin is one revision-checked write.
type Pin struct {
	Pinned   bool       `json:"pinned,omitempty"`
	PinnedAt *time.Time `json:"pinned_at,omitempty"`
}

// CaptureFields are shared by inbox items and ideas. Origin is descriptive
// provenance, not a provider identity or an instruction to fetch anything.
type CaptureFields struct {
	Common
	Pin
	Status       string `json:"status"`
	Priority     int    `json:"priority"`
	ProjectID    string `json:"project_id,omitempty"`
	WorkstreamID string `json:"workstream_id,omitempty"`
}
type InboxItem struct {
	CaptureFields
	Origin     string     `json:"origin,omitempty"`
	CapturedAt *time.Time `json:"captured_at,omitempty"`
}

func (InboxItem) ObjectKind() Kind { return InboxItemKind }

type Idea struct {
	CaptureFields
	Hypothesis      string `json:"hypothesis,omitempty"`
	ExpectedOutcome string `json:"expected_outcome,omitempty"`
}

func (Idea) ObjectKind() Kind { return IdeaKind }

// PlanningContext links records explicitly; associations do not inherit scope
// or change the associated object's lifecycle.
type PlanningContext struct {
	ProjectID    string `json:"project_id,omitempty"`
	WorkstreamID string `json:"workstream_id,omitempty"`
}
type Decision struct {
	Common
	Pin
	PlanningContext
	Status    string     `json:"status"`
	Context   string     `json:"context,omitempty"`
	Options   []string   `json:"options,omitempty"`
	Outcome   string     `json:"outcome,omitempty"`
	Rationale string     `json:"rationale,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Target    *Reference `json:"target,omitempty"`
}

func (Decision) ObjectKind() Kind { return DecisionKind }

type Risk struct {
	Common
	Pin
	PlanningContext
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	Likelihood  int        `json:"likelihood"` // 0 unknown, 1 lowest through 5 highest
	Impact      int        `json:"impact"`     // 0 unknown, 1 lowest through 5 highest
	Mitigation  string     `json:"mitigation,omitempty"`
	Contingency string     `json:"contingency,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	Target      *Reference `json:"target,omitempty"`
}

func (Risk) ObjectKind() Kind { return RiskKind }

type RoadmapItem struct {
	Common
	Pin
	PlanningContext
	Status              string     `json:"status"`
	Priority            int        `json:"priority"`
	Outcome             string     `json:"outcome,omitempty"`
	StrategicPriorityID string     `json:"strategic_priority_id,omitempty"`
	PlanID              string     `json:"plan_id,omitempty"`
	EpicID              string     `json:"epic_id,omitempty"`
	StartsAt            *time.Time `json:"starts_at,omitempty"`
	EndsAt              *time.Time `json:"ends_at,omitempty"`
}

func (RoadmapItem) ObjectKind() Kind { return RoadmapItemKind }

// StrategicPriority is an objective, distinct from the numeric priority rank.
type StrategicPriority struct {
	Common
	Pin
	PlanningContext
	Area            string     `json:"area,omitempty"`
	Horizon         string     `json:"horizon,omitempty"`
	DoneSummary     string     `json:"done_summary,omitempty"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	Outcome         string     `json:"outcome,omitempty"`
	SuccessCriteria []string   `json:"success_criteria,omitempty"`
	StartsAt        *time.Time `json:"starts_at,omitempty"`
	EndsAt          *time.Time `json:"ends_at,omitempty"`
}

func (StrategicPriority) ObjectKind() Kind { return StrategicPriorityKind }

func capturePlanningStatus(kind Kind, status string) bool {
	var allowed string
	switch kind {
	case InboxItemKind:
		allowed = "new triaged archived"
	case IdeaKind:
		allowed = "draft exploring accepted rejected promoted archived"
	case DecisionKind:
		allowed = "proposed accepted rejected superseded archived"
	case RiskKind:
		allowed = "open mitigating accepted resolved archived"
	case RoadmapItemKind:
		allowed = "draft planned active completed deferred cancelled archived"
	case StrategicPriorityKind:
		allowed = "proposed ready approved paused done archived draft active achieved"
	default:
		return false
	}
	for _, value := range strings.Fields(allowed) {
		if value == status {
			return true
		}
	}
	return false
}

func validatePlannerRecord(kind Kind, raw json.RawMessage) (string, []referenceLink, error) {
	var name, status, project, workstream string
	var priority int
	var dates []*time.Time
	var starts, ends *time.Time
	var target *Reference
	var criteria []string
	var links []referenceLink
	link := func(id string, kind Kind, relation string) {
		if id != "" {
			links = append(links, referenceLink{Link: Link{ToID: id, Relation: relation}, kind: kind})
		}
	}
	switch kind {
	case InboxItemKind:
		var d InboxItem
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, priority, project, workstream = d.Name, d.Status, d.Priority, d.ProjectID, d.WorkstreamID
		dates = append(dates, d.CapturedAt)
	case IdeaKind:
		var d Idea
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, priority, project, workstream = d.Name, d.Status, d.Priority, d.ProjectID, d.WorkstreamID
	case DecisionKind:
		var d Decision
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, project, workstream = d.Name, d.Status, d.ProjectID, d.WorkstreamID
		target, criteria = d.Target, d.Options
		dates = append(dates, d.DecidedAt)
	case RiskKind:
		var d Risk
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, priority, project, workstream = d.Name, d.Status, d.Priority, d.ProjectID, d.WorkstreamID
		target = d.Target
		dates = append(dates, d.DueAt)
		if d.Likelihood < 0 || d.Likelihood > 5 || d.Impact < 0 || d.Impact > 5 {
			return "", nil, fmt.Errorf("%w: risk likelihood and impact must be 0–5", ErrInvalid)
		}
	case RoadmapItemKind:
		var d RoadmapItem
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, priority, project, workstream = d.Name, d.Status, d.Priority, d.ProjectID, d.WorkstreamID
		starts, ends = d.StartsAt, d.EndsAt
		link(d.StrategicPriorityID, StrategicPriorityKind, "strategic_priority")
		link(d.PlanID, PlanKind, "plan")
		link(d.EpicID, EpicKind, "epic")
	case StrategicPriorityKind:
		var d StrategicPriority
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, priority, project, workstream = d.Name, d.Status, d.Priority, d.ProjectID, d.WorkstreamID
		starts, ends, criteria = d.StartsAt, d.EndsAt, d.SuccessCriteria
	default:
		return "", nil, ErrInvalid
	}
	if strings.TrimSpace(name) == "" || !capturePlanningStatus(kind, status) || priority < 0 || priority > 5 {
		return "", nil, fmt.Errorf("%w: name, recognized Planner status and priority 0–5 required", ErrInvalid)
	}
	for _, date := range append(dates, starts, ends) {
		if !validDate(date) {
			return "", nil, fmt.Errorf("%w: invalid Planner date", ErrInvalid)
		}
	}
	// Roadmap windows may be open-ended; two bounds must be ordered.
	if starts != nil && ends != nil && !ends.After(*starts) {
		return "", nil, fmt.Errorf("%w: end must follow start", ErrInvalid)
	}
	for _, value := range criteria {
		if strings.TrimSpace(value) == "" {
			return "", nil, fmt.Errorf("%w: options/criteria cannot be blank", ErrInvalid)
		}
	}
	if target != nil {
		if !validKind(target.Kind) || strings.TrimSpace(target.ID) == "" {
			return "", nil, fmt.Errorf("%w: complete typed target required", ErrInvalid)
		}
		link(target.ID, target.Kind, "target")
	}
	link(project, ProjectKind, "project")
	link(workstream, WorkstreamKind, "workstream")
	return name, links, nil
}

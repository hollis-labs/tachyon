package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Plan and Task are authored work descriptions. Status changes never start a
// run, dispatch an agent or transition a remote task. Assignments target these
// objects through Assignment.Target; assignees are not duplicated on each task.
type Plan struct {
	Common
	Status             string     `json:"status"`
	Priority           int        `json:"priority"` // 0 unspecified, 1 highest through 5 lowest
	ProjectID          string     `json:"project_id,omitempty"`
	WorkflowID         string     `json:"workflow_id,omitempty"`
	AcceptanceCriteria []string   `json:"acceptance_criteria,omitempty"`
	DueAt              *time.Time `json:"due_at,omitempty"`
}

func (Plan) ObjectKind() Kind { return PlanKind }

type Task struct {
	Common
	Status             string     `json:"status"`
	Priority           int        `json:"priority"`
	ProjectID          string     `json:"project_id,omitempty"`
	PlanID             string     `json:"plan_id,omitempty"`
	ParentID           string     `json:"parent_id,omitempty"` // task hierarchy; a plan uses plan_id
	DependsOn          []string   `json:"depends_on,omitempty"`
	AcceptanceCriteria []string   `json:"acceptance_criteria,omitempty"`
	DueAt              *time.Time `json:"due_at,omitempty"`
}

func (Task) ObjectKind() Kind { return TaskKind }

func planningKind(kind Kind) bool { return kind == PlanKind || kind == TaskKind }
func planningStatus(status string) bool {
	switch status {
	case "draft", "backlog", "todo", "queued", "doing", "review", "done", "blocked", "paused", "archived", "abandoned", "cancelled":
		return true
	}
	return false
}

func validatePlanningDefinition(kind Kind, raw json.RawMessage) (string, []referenceLink, error) {
	var common Common
	var status string
	var priority int
	var due *time.Time
	var acceptance []string
	var links []referenceLink
	link := func(id string, target Kind, relation string) {
		if id != "" {
			links = append(links, referenceLink{Link: Link{ToID: id, Relation: relation}, kind: target})
		}
	}
	switch kind {
	case PlanKind:
		var d Plan
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		common, status, priority, due, acceptance = d.Common, d.Status, d.Priority, d.DueAt, d.AcceptanceCriteria
		link(d.ProjectID, ProjectKind, "project")
		link(d.WorkflowID, WorkflowKind, "workflow")
	case TaskKind:
		var d Task
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		common, status, priority, due, acceptance = d.Common, d.Status, d.Priority, d.DueAt, d.AcceptanceCriteria
		link(d.ProjectID, ProjectKind, "project")
		link(d.PlanID, PlanKind, "plan")
		link(d.ParentID, TaskKind, "parent")
		if len(d.DependsOn) > 256 {
			return "", nil, fmt.Errorf("%w: at most 256 task dependencies", ErrInvalid)
		}
		seen := map[string]bool{}
		for _, id := range d.DependsOn {
			if strings.TrimSpace(id) == "" || seen[id] {
				return "", nil, fmt.Errorf("%w: empty or duplicate dependency", ErrInvalid)
			}
			seen[id] = true
			link(id, TaskKind, "depends_on")
		}
	default:
		return "", nil, ErrInvalid
	}
	if strings.TrimSpace(common.Name) == "" || !planningStatus(status) || priority < 0 || priority > 5 {
		return "", nil, fmt.Errorf("%w: name, recognized status and priority 0–5 required", ErrInvalid)
	}
	if due != nil && (due.IsZero() || due.Year() < 1 || due.Year() > 9999) {
		return "", nil, fmt.Errorf("%w: valid due_at required", ErrInvalid)
	}
	for _, criterion := range acceptance {
		if strings.TrimSpace(criterion) == "" {
			return "", nil, fmt.Errorf("%w: acceptance criteria cannot be blank", ErrInvalid)
		}
	}
	return common.Name, links, nil
}

// Graph validation shares the queued write transaction. Two simultaneous edits
// cannot both pass against a stale graph and commit a dependency/parent cycle.
// Tombstones retain their edges for history and still participate in this check.
func validatePlanningGraph(ctx context.Context, tx *sql.Tx, r Record, links []referenceLink) error {
	if r.Kind != TaskKind {
		return nil
	}
	for _, link := range links {
		if link.Relation != "depends_on" && link.Relation != "parent" {
			continue
		}
		if link.ToID == r.ID {
			return fmt.Errorf("%w: a task cannot reference itself", ErrInvalid)
		}
		var cycle bool
		err := tx.QueryRowContext(ctx, `WITH RECURSIVE reachable(id) AS (
 SELECT ? UNION SELECT l.to_id FROM object_links l JOIN reachable r ON l.from_id=r.id WHERE l.relation=?
) SELECT EXISTS(SELECT 1 FROM reachable WHERE id=?)`, link.ToID, link.Relation, r.ID).Scan(&cycle)
		if err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf("%w: cyclic task %s relationship", ErrInvalid, link.Relation)
		}
	}
	return nil
}

func projectPlanning(ctx context.Context, tx *sql.Tx, r Record) error {
	if !planningKind(r.Kind) {
		return nil
	}
	// Shared scalar fields; the full typed definition has already been checked.
	var d struct {
		Status    string     `json:"status"`
		Priority  int        `json:"priority"`
		ProjectID string     `json:"project_id"`
		PlanID    string     `json:"plan_id"`
		ParentID  string     `json:"parent_id"`
		DueAt     *time.Time `json:"due_at"`
	}
	if err := json.Unmarshal(r.Definition, &d); err != nil {
		return err
	}
	due := ""
	if d.DueAt != nil {
		due = timestamp(*d.DueAt)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO planner_items(object_id,kind,status,priority,project_id,plan_id,parent_id,due_at) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(object_id) DO UPDATE SET status=excluded.status,priority=excluded.priority,project_id=excluded.project_id,plan_id=excluded.plan_id,parent_id=excluded.parent_id,due_at=excluded.due_at`, r.ID, r.Kind, d.Status, d.Priority, d.ProjectID, d.PlanID, d.ParentID, due)
	return err
}

// PlannerFilter intersects exact filters, ordered by stable UUID with keyset
// pagination. Empty Kind lists both plans and tasks. AssignedTo matches agent or
// team IDs through non-deleted assignment objects (any assignment state).
type PlannerFilter struct {
	Kind           Kind
	Status         string
	Priority       *int
	ProjectID      string
	PlanID         string
	ParentID       string
	AssignedTo     string
	IncludeDeleted bool
	AfterID        string
	Limit          int
}

func (s *Service) ListPlanner(ctx context.Context, f PlannerFilter) ([]Record, error) {
	if f.Kind != "" && !planningKind(f.Kind) || f.Status != "" && !planningStatus(f.Status) || f.Priority != nil && (*f.Priority < 0 || *f.Priority > 5) {
		return nil, ErrInvalid
	}
	limit, err := pageLimit(f.Limit)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + joined + " JOIN planner_items p ON p.object_id=o.id WHERE o.id>?"
	args := []any{f.AfterID}
	for _, condition := range []struct{ column, value string }{
		{"p.kind", string(f.Kind)}, {"p.status", f.Status}, {"p.project_id", f.ProjectID}, {"p.plan_id", f.PlanID}, {"p.parent_id", f.ParentID},
	} {
		if condition.value != "" {
			query += " AND " + condition.column + "=?"
			args = append(args, condition.value)
		}
	}
	if f.Priority != nil {
		query += " AND p.priority=?"
		args = append(args, *f.Priority)
	}
	if !f.IncludeDeleted {
		query += " AND o.deleted_at IS NULL AND (o.ownership='tachyon' OR s.deleted_at IS NULL)"
	}
	if f.AssignedTo != "" {
		query += ` AND EXISTS(SELECT 1 FROM object_links target
 JOIN object_links assignee ON assignee.from_id=target.from_id AND assignee.relation='assignee'
 JOIN objects assignment ON assignment.id=target.from_id AND assignment.kind='assignment'
 LEFT JOIN object_sources assignment_source ON assignment_source.object_id=assignment.id
 WHERE target.to_id=o.id AND target.relation='target' AND assignee.to_id=?
 AND assignment.deleted_at IS NULL AND (assignment.ownership='tachyon' OR assignment_source.deleted_at IS NULL))`
		args = append(args, f.AssignedTo)
	}
	query += " ORDER BY o.id LIMIT ?"
	args = append(args, limit)
	rows, err := s.store.Reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

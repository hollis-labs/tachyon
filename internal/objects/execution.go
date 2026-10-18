package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ExecutionLinks are explicit local associations, never inferred from names,
// workspaces, provider-native session IDs or the current roster.
type ExecutionLinks struct {
	ProjectID    string `json:"project_id,omitempty"`
	WorkstreamID string `json:"workstream_id,omitempty"`
	AgentID      string `json:"agent_id,omitempty"`
	TeamID       string `json:"team_id,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	MachineID    string `json:"machine_id,omitempty"`
}
type Session struct {
	Common
	ExecutionLinks
	Status  string `json:"status"`
	Runtime string `json:"runtime,omitempty"`
	// NativeSession is a qualified runtime reference, separate from Source.Key
	// (e.g. Tether's session ID). Neither is a local foreign key.
	NativeSession *ExternalReference `json:"native_session,omitempty"`
	RunID         string             `json:"run_id,omitempty"`
	ParentID      string             `json:"parent_id,omitempty"`
	ResumedFromID string             `json:"resumed_from_id,omitempty"`
	OpenedAt      *time.Time         `json:"opened_at,omitempty"`
	StartedAt     *time.Time         `json:"started_at,omitempty"`
	EndedAt       *time.Time         `json:"ended_at,omitempty"`
	ExitCode      *int               `json:"exit_code,omitempty"`
}

func (Session) ObjectKind() Kind { return SessionKind }

// Run describes one execution attempt. A retry is a new record linked by
// RetryOfID. Sessions point to their run; no duplicate session array is stored.
type Run struct {
	Common
	ExecutionLinks
	Status           string     `json:"status"`
	WorkflowID       string     `json:"workflow_id,omitempty"`
	WorkflowRevision int64      `json:"workflow_revision,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
	RetryOfID        string     `json:"retry_of_id,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
}

func (Run) ObjectKind() Kind { return RunKind }

func executionKind(k Kind) bool { return k == SessionKind || k == RunKind }
func executionStatus(k Kind, status string) bool {
	if k == "" {
		return executionStatus(SessionKind, status) || executionStatus(RunKind, status)
	}
	if k == SessionKind {
		switch status {
		case "unknown", "created", "ready", "launching", "running", "paused", "detached", "orphaned", "completed", "failed", "killed":
			return true
		}
	}
	if k == RunKind {
		switch status {
		case "unknown", "planned", "queued", "running", "paused", "blocked", "completed", "failed", "cancelled":
			return true
		}
	}
	return false
}
func validateExecutionDefinition(k Kind, raw json.RawMessage) (string, []referenceLink, error) {
	var name, status string
	var associations ExecutionLinks
	var opened, started, ended *time.Time
	var links []referenceLink
	link := func(id string, kind Kind, relation string) {
		if id != "" {
			links = append(links, referenceLink{Link: Link{ToID: id, Relation: relation}, kind: kind})
		}
	}
	switch k {
	case SessionKind:
		var d Session
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, associations, opened, started, ended = d.Name, d.Status, d.ExecutionLinks, d.OpenedAt, d.StartedAt, d.EndedAt
		link(d.RunID, RunKind, "run")
		link(d.ParentID, SessionKind, "parent")
		link(d.ResumedFromID, SessionKind, "resumed_from")
		if d.NativeSession != nil {
			n := d.NativeSession
			if strings.TrimSpace(n.Provider) == "" || strings.TrimSpace(n.Instance) == "" || strings.TrimSpace(n.Collection) == "" || strings.TrimSpace(n.ExternalID) == "" {
				return "", nil, fmt.Errorf("%w: native session requires qualified identity", ErrInvalid)
			}
		}
	case RunKind:
		var d Run
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, associations, started, ended = d.Name, d.Status, d.ExecutionLinks, d.StartedAt, d.EndedAt
		link(d.WorkflowID, WorkflowKind, "workflow")
		link(d.ParentID, RunKind, "parent")
		link(d.RetryOfID, RunKind, "retry_of")
		if (d.WorkflowID == "" && d.WorkflowRevision != 0) || (d.WorkflowID != "" && d.WorkflowRevision < 1) {
			return "", nil, fmt.Errorf("%w: workflow requires its exact revision", ErrInvalid)
		}
	default:
		return "", nil, ErrInvalid
	}
	if strings.TrimSpace(name) == "" || !executionStatus(k, status) {
		return "", nil, fmt.Errorf("%w: execution name and recognized status required", ErrInvalid)
	}
	if !validDate(opened) || !validDate(started) || !validDate(ended) || (opened != nil && started != nil && started.Before(*opened)) || (opened != nil && ended != nil && ended.Before(*opened)) || (started != nil && ended != nil && ended.Before(*started)) {
		return "", nil, fmt.Errorf("%w: invalid execution timestamps", ErrInvalid)
	}
	link(associations.ProjectID, ProjectKind, "project")
	link(associations.WorkstreamID, WorkstreamKind, "workstream")
	link(associations.AgentID, AgentKind, "agent")
	link(associations.TeamID, TeamKind, "team")
	link(associations.AssignmentID, AssignmentKind, "assignment")
	link(associations.TaskID, TaskKind, "task")
	link(associations.MachineID, MachineKind, "machine")
	return name, links, nil
}

func validateExecutionGraph(ctx context.Context, tx *sql.Tx, r Record, links []referenceLink) error {
	if !executionKind(r.Kind) {
		return nil
	}
	if r.Kind == RunKind {
		var d Run
		if err := json.Unmarshal(r.Definition, &d); err != nil {
			return err
		}
		if d.WorkflowID != "" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM object_revisions v JOIN objects o ON o.id=v.object_id WHERE o.id=? AND o.kind='workflow' AND v.revision=?)`, d.WorkflowID, d.WorkflowRevision).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: workflow revision not found", ErrInvalid)
			}
		}
	}
	for _, l := range links {
		if l.Relation != "parent" && l.Relation != "resumed_from" && l.Relation != "retry_of" {
			continue
		}
		var cycle bool
		err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id) AS (
 SELECT ? UNION SELECT l.to_id FROM object_links l JOIN ancestors a ON l.from_id=a.id WHERE l.relation IN ('parent','resumed_from','retry_of')
) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=?)`, l.ToID, r.ID).Scan(&cycle)
		if err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf("%w: execution lineage cycle", ErrInvalid)
		}
	}
	return nil
}

func projectExecution(ctx context.Context, tx *sql.Tx, r Record) error {
	if !executionKind(r.Kind) {
		return nil
	}
	var d struct {
		ExecutionLinks
		Status     string     `json:"status"`
		RunID      string     `json:"run_id"`
		WorkflowID string     `json:"workflow_id"`
		StartedAt  *time.Time `json:"started_at"`
		EndedAt    *time.Time `json:"ended_at"`
	}
	if err := json.Unmarshal(r.Definition, &d); err != nil {
		return err
	}
	start, end := "", ""
	if d.StartedAt != nil {
		start = timestamp(*d.StartedAt)
	}
	if d.EndedAt != nil {
		end = timestamp(*d.EndedAt)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO execution_items(object_id,kind,status,project_id,workstream_id,agent_id,task_id,run_id,workflow_id,started_at,ended_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(object_id) DO UPDATE SET status=excluded.status,project_id=excluded.project_id,workstream_id=excluded.workstream_id,agent_id=excluded.agent_id,task_id=excluded.task_id,run_id=excluded.run_id,workflow_id=excluded.workflow_id,started_at=excluded.started_at,ended_at=excluded.ended_at`, r.ID, r.Kind, d.Status, d.ProjectID, d.WorkstreamID, d.AgentID, d.TaskID, d.RunID, d.WorkflowID, start, end)
	return err
}

type ExecutionFilter struct {
	Kind                                                        Kind
	Status                                                      string
	ProjectID, WorkstreamID, AgentID, TaskID, RunID, WorkflowID string
	IncludeDeleted                                              bool
	AfterID                                                     string
	Limit                                                       int
}

func (s *Service) ListExecutions(ctx context.Context, f ExecutionFilter) ([]Record, error) {
	if f.Kind != "" && !executionKind(f.Kind) || f.Status != "" && !executionStatus(f.Kind, f.Status) {
		return nil, ErrInvalid
	}
	limit, err := pageLimit(f.Limit)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + joined + " JOIN execution_items e ON e.object_id=o.id WHERE o.id>?"
	args := []any{f.AfterID}
	for _, c := range []struct{ field, value string }{{"kind", string(f.Kind)}, {"status", f.Status}, {"project_id", f.ProjectID}, {"workstream_id", f.WorkstreamID}, {"agent_id", f.AgentID}, {"task_id", f.TaskID}, {"run_id", f.RunID}, {"workflow_id", f.WorkflowID}} {
		if c.value != "" {
			query += " AND e." + c.field + "=?"
			args = append(args, c.value)
		}
	}
	if !f.IncludeDeleted {
		query += " AND o.deleted_at IS NULL AND (o.ownership='tachyon' OR s.deleted_at IS NULL)"
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

// EnsureSessionIdentityTx is for chat ingestion within the caller's queued
// transaction. Identity-only evidence cannot overwrite richer observations,
// advance their freshness, or restore a tombstone. The epoch observed_at on a
// new stub permits an already-running inventory read to enrich it safely.
// The caller must roll back its transaction on error and must not nest Submit.
func EnsureSessionIdentityTx(ctx context.Context, tx *sql.Tx, key SourceKey, actor string) (Record, error) {
	if key.Kind != SessionKind || strings.TrimSpace(key.ExternalID) == "" || actor == "" {
		return Record{}, ErrInvalid
	}
	if err := validateScope(key.SourceScope); err != nil {
		return Record{}, err
	}
	r, err := lookup(ctx, tx, key)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Record{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Record{}, err
	}
	raw, _ := json.Marshal(Session{Common: Common{Name: key.ExternalID}, Status: "unknown"})
	r = Record{ID: id.String(), Kind: SessionKind, Ownership: "source", Local: json.RawMessage(`{}`), Metadata: json.RawMessage(`{}`), Source: &Source{Key: key, ObservedAt: time.Unix(0, 0).UTC(), Definition: raw}}
	if err = persist(ctx, tx, &r, "identity_observed", actor); err != nil {
		return Record{}, err
	}
	return r, nil
}

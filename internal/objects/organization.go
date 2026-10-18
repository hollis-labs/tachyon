package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Organization records describe work; changing them never dispatches execution.
// Membership in epics and sprints is explicit on each plan/task, not inherited.
type Epic struct {
	Common
	Status       string     `json:"status"`
	Priority     int        `json:"priority"`
	ProjectID    string     `json:"project_id,omitempty"`
	WorkstreamID string     `json:"workstream_id,omitempty"`
	Outcome      string     `json:"outcome,omitempty"`
	DueAt        *time.Time `json:"due_at,omitempty"`
}

func (Epic) ObjectKind() Kind { return EpicKind }

type Sprint struct {
	Common
	Status       string     `json:"status"`
	ProjectID    string     `json:"project_id,omitempty"`
	WorkstreamID string     `json:"workstream_id,omitempty"`
	Goal         string     `json:"goal,omitempty"`
	StartsAt     *time.Time `json:"starts_at,omitempty"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
}

func (Sprint) ObjectKind() Kind { return SprintKind }

type Workstream struct {
	Common
	Status    string `json:"status"`
	ProjectID string `json:"project_id,omitempty"`
	// WorkflowID is an explicit local mapping. Provider workflow IDs belong in
	// provider properties and must not be interpreted as local foreign keys.
	WorkflowID string `json:"workflow_id,omitempty"`
}

func (Workstream) ObjectKind() Kind { return WorkstreamKind }

type Collection struct {
	Common
	Status       string `json:"status"`
	ProjectID    string `json:"project_id,omitempty"`
	WorkstreamID string `json:"workstream_id,omitempty"`
	// Ordered, unique typed references. Collections cannot contain collections.
	// An object can belong to multiple collections; tombstones retain membership.
	Members []Reference `json:"members,omitempty"`
}

func (Collection) ObjectKind() Kind { return CollectionKind }

func validPlanningStatus(kind Kind, status string) bool {
	switch kind {
	case "":
		return planningStatus(status) || validPlanningStatus(SprintKind, status) || validPlanningStatus(WorkstreamKind, status)
	case PlanKind, TaskKind, EpicKind:
		return planningStatus(status)
	case SprintKind:
		return status == "draft" || status == "planned" || status == "active" || status == "completed" || status == "cancelled" || status == "archived"
	case WorkstreamKind:
		return status == "draft" || status == "active" || status == "paused" || status == "closed" || status == "archived"
	case CollectionKind:
		return status == "active" || status == "archived"
	}
	return false
}

func validDate(t *time.Time) bool {
	return t == nil || (!t.IsZero() && t.Year() >= 1 && t.Year() <= 9999)
}

func validateOrganizationDefinition(kind Kind, raw json.RawMessage) (string, []referenceLink, error) {
	var name, status, project, workstream string
	var links []referenceLink
	link := func(id string, target Kind, relation string) {
		if id != "" {
			links = append(links, referenceLink{Link: Link{ToID: id, Relation: relation}, kind: target})
		}
	}
	switch kind {
	case EpicKind:
		var d Epic
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, project, workstream = d.Name, d.Status, d.ProjectID, d.WorkstreamID
		if d.Priority < 0 || d.Priority > 5 || !validDate(d.DueAt) {
			return "", nil, fmt.Errorf("%w: epic requires priority 0–5 and valid due_at", ErrInvalid)
		}
	case SprintKind:
		var d Sprint
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, project, workstream = d.Name, d.Status, d.ProjectID, d.WorkstreamID
		if !validDate(d.StartsAt) || !validDate(d.EndsAt) || (d.StartsAt == nil) != (d.EndsAt == nil) || (d.StartsAt != nil && !d.EndsAt.After(*d.StartsAt)) {
			return "", nil, fmt.Errorf("%w: sprint dates must both be absent or end after start", ErrInvalid)
		}
	case WorkstreamKind:
		var d Workstream
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, project = d.Name, d.Status, d.ProjectID
		link(d.WorkflowID, WorkflowKind, "workflow")
	case CollectionKind:
		var d Collection
		if err := strict(raw, &d); err != nil {
			return "", nil, err
		}
		name, status, project, workstream = d.Name, d.Status, d.ProjectID, d.WorkstreamID
		if len(d.Members) > 1000 {
			return "", nil, fmt.Errorf("%w: at most 1000 collection members", ErrInvalid)
		}
		seen := map[string]bool{}
		for i, m := range d.Members {
			if strings.TrimSpace(m.ID) == "" || seen[m.ID] || !validKind(m.Kind) || m.Kind == CollectionKind {
				return "", nil, fmt.Errorf("%w: collection members must be unique typed references, excluding collections", ErrInvalid)
			}
			seen[m.ID] = true
			links = append(links, referenceLink{Link: Link{ToID: m.ID, Relation: "member", Slot: fmt.Sprintf("%06d", i)}, kind: m.Kind})
		}
	default:
		return "", nil, ErrInvalid
	}
	if strings.TrimSpace(name) == "" || !validPlanningStatus(kind, status) {
		return "", nil, fmt.Errorf("%w: name and recognized organization status required", ErrInvalid)
	}
	link(project, ProjectKind, "project")
	link(workstream, WorkstreamKind, "workstream")
	return name, links, nil
}

// SetCollectionMembers atomically replaces/reorders membership using the same
// expected revision contract as Patch. Null/empty means remove all members.
func (s *Service) SetCollectionMembers(ctx context.Context, id string, expected int64, members []Reference, actor string) (Record, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if r.Kind != CollectionKind {
		return Record{}, ErrInvalid
	}
	// Generic Patch null releases a source overlay. This operation promises to
	// clear membership, so retain an explicit empty array even on sourced records.
	if members == nil {
		members = []Reference{}
	}
	raw, err := json.Marshal(map[string]any{"members": members})
	if err != nil {
		return Record{}, err
	}
	return s.Patch(ctx, id, expected, Patch{Definition: raw}, actor)
}

type CollectionMember struct {
	Position  int       `json:"position"`
	Reference Reference `json:"reference"`
	Record    Record    `json:"record"`
}
type CollectionPage struct {
	CollectionID string             `json:"collection_id"`
	Revision     int64              `json:"revision"`
	Members      []CollectionMember `json:"members"`
	NextOffset   *int               `json:"next_offset,omitempty"`
}

// ListCollectionMembers preserves collection order and includes tombstones.
// Each page reads a consistent database snapshot. Pass the returned revision on
// subsequent pages to detect membership edits; member contents are current at
// each page's read time. Expected revision 0 is allowed only on the first page.
func (s *Service) ListCollectionMembers(ctx context.Context, id string, expected int64, offset, limit int) (CollectionPage, error) {
	var page CollectionPage
	limit, err := pageLimit(limit)
	if err != nil {
		return page, err
	}
	if offset < 0 || expected < 0 || (offset > 0 && expected == 0) {
		return page, ErrInvalid
	}
	tx, err := s.store.Reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	r, err := get(ctx, tx, id)
	if err != nil {
		return page, err
	}
	if r.Kind != CollectionKind {
		return page, ErrInvalid
	}
	if expected != 0 && r.Revision != expected {
		return page, ErrConflict
	}
	var d Collection
	if err = json.Unmarshal(r.Definition, &d); err != nil {
		return page, err
	}
	page = CollectionPage{CollectionID: id, Revision: r.Revision, Members: []CollectionMember{}}
	end := offset + limit
	// Avoid overflowing on an arbitrary caller offset.
	if offset >= len(d.Members) {
		return page, tx.Commit()
	}
	if end < offset || end > len(d.Members) {
		end = len(d.Members)
	}
	for i := offset; i < end; i++ {
		member, err := get(ctx, tx, d.Members[i].ID)
		if err != nil {
			return CollectionPage{}, err
		}
		page.Members = append(page.Members, CollectionMember{Position: i, Reference: d.Members[i], Record: member})
	}
	if end < len(d.Members) {
		page.NextOffset = &end
	}
	return page, tx.Commit()
}

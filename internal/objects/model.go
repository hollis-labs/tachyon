// Package objects is Tachyon's transport-independent data service. Definitions
// describe desired configuration; observing or editing one never dispatches work.
package objects

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Kind string

const (
	AgentKind      Kind = "agent"
	TeamKind       Kind = "team"
	AssignmentKind Kind = "assignment"
	WorkflowKind   Kind = "workflow"
	ProjectKind    Kind = "project"
	MachineKind    Kind = "machine"
	RepositoryKind Kind = "repository"
	PlanKind       Kind = "plan"
	TaskKind       Kind = "task"
	EpicKind       Kind = "epic"
	SprintKind     Kind = "sprint"
	CollectionKind Kind = "collection"
	WorkstreamKind Kind = "workstream"
)

var (
	ErrNotFound         = errors.New("object not found")
	ErrConflict         = errors.New("object revision conflict")
	ErrInvalid          = errors.New("invalid object")
	ErrStaleObservation = errors.New("stale source observation")
)

type Definition interface{ ObjectKind() Kind }
type Common struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
}
type Agent struct {
	Common
	Runtime      string   `json:"runtime,omitempty"`
	Model        string   `json:"model,omitempty"`
	Enabled      bool     `json:"enabled"`
	Roles        []string `json:"roles,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	ProjectID    string   `json:"project_id,omitempty"`
	MachineID    string   `json:"machine_id,omitempty"`
}

func (Agent) ObjectKind() Kind { return AgentKind }

type TeamSlot struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	AgentID string `json:"agent_id,omitempty"`
	Min     int    `json:"min"`
	Max     int    `json:"max"`
}
type Team struct {
	Common
	Slots       []TeamSlot `json:"slots,omitempty"`
	Coordinator string     `json:"coordinator,omitempty"`
	ProjectID   string     `json:"project_id,omitempty"`
	WorkflowID  string     `json:"workflow_id,omitempty"`
}

func (Team) ObjectKind() Kind { return TeamKind }

type Reference struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
}

// ExternalReference is an explicitly unresolved external target, not a local FK.
// Provider and instance distinguish identical IDs on different hosts/accounts.
type ExternalReference struct {
	Provider   string `json:"provider"`
	Instance   string `json:"instance"`
	Collection string `json:"collection"`
	ExternalID string `json:"external_id"`
}
type Assignment struct {
	Common
	AgentID        string             `json:"agent_id,omitempty"`
	TeamID         string             `json:"team_id,omitempty"`
	Target         *Reference         `json:"target,omitempty"`
	ExternalTarget *ExternalReference `json:"external_target,omitempty"`
	Role           string             `json:"role,omitempty"`
	State          string             `json:"state"` // draft/active/completed/cancelled; not observed process state
}

func (Assignment) ObjectKind() Kind { return AssignmentKind }

type WorkflowStep struct {
	ID        string         `json:"id"`
	Verb      string         `json:"verb"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Inputs    map[string]any `json:"inputs,omitempty"`
}
type Workflow struct {
	Common
	Steps     []WorkflowStep `json:"steps,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
}

func (Workflow) ObjectKind() Kind { return WorkflowKind }

type Project struct {
	Common
	Guidelines    string   `json:"guidelines,omitempty"`
	EntryPoints   []string `json:"entry_points,omitempty"`
	RepositoryIDs []string `json:"repository_ids,omitempty"`
}

func (Project) ObjectKind() Kind { return ProjectKind }

type Machine struct {
	Common
	Hostname     string `json:"hostname,omitempty"`
	Platform     string `json:"platform,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Environment  string `json:"environment,omitempty"`
	Address      string `json:"address,omitempty"`
}

func (Machine) ObjectKind() Kind { return MachineKind }

type Repository struct {
	Common
	Host          string         `json:"host,omitempty"`
	Owner         string         `json:"owner,omitempty"`
	Slug          string         `json:"slug,omitempty"`
	URL           string         `json:"url,omitempty"`
	DefaultBranch string         `json:"default_branch,omitempty"`
	Visibility    string         `json:"visibility,omitempty"`
	Archived      bool           `json:"archived"`
	Settings      map[string]any `json:"settings,omitempty"`
}

func (Repository) ObjectKind() Kind { return RepositoryKind }

type SourceScope struct {
	Provider   string `json:"provider"`
	Instance   string `json:"instance"`
	Collection string `json:"collection"`
	Kind       Kind   `json:"kind"`
}
type SourceKey struct {
	SourceScope
	ExternalID string `json:"external_id"`
}
type Observation struct {
	Key        SourceKey       `json:"key"`
	Version    string          `json:"version,omitempty"` // opaque upstream version, never numerically ordered
	ObservedAt time.Time       `json:"observed_at"`       // poll start, not response completion time
	Deleted    bool            `json:"deleted"`
	Definition json.RawMessage `json:"definition"`
}
type Source struct {
	Key        SourceKey       `json:"key"`
	Version    string          `json:"version,omitempty"`
	ObservedAt time.Time       `json:"observed_at"`
	DeletedAt  *time.Time      `json:"deleted_at,omitempty"`
	Definition json.RawMessage `json:"definition"`
}
type Record struct {
	ID         string          `json:"id"`
	Kind       Kind            `json:"kind"`
	Ownership  string          `json:"ownership"`
	Name       string          `json:"name"`
	Revision   int64           `json:"revision"`
	Local      json.RawMessage `json:"local"`      // authored definition, or sparse overrides on a source record
	Metadata   json.RawMessage `json:"metadata"`   // arbitrary Tachyon-owned JSON object
	Definition json.RawMessage `json:"definition"` // effective observed + local configuration
	Source     *Source         `json:"source,omitempty"`
	DeletedAt  *time.Time      `json:"deleted_at,omitempty"` // local deletion is never undone by sync
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func (r Record) Deleted() bool {
	return r.DeletedAt != nil || r.Ownership == "source" && r.Source != nil && r.Source.DeletedAt != nil
}

type Revision struct {
	Record     Record    `json:"record"`
	Operation  string    `json:"operation"`
	Actor      string    `json:"actor"`
	RecordedAt time.Time `json:"recorded_at"`
}
type Link struct {
	FromID   string `json:"from_id"`
	Relation string `json:"relation"`
	Slot     string `json:"slot,omitempty"`
	ToID     string `json:"to_id"`
}
type Filter struct {
	Kind           Kind
	IncludeDeleted bool
	AfterID        string
	Limit          int
}

// Patch uses JSON merge-patch semantics (null removes a local key, arrays replace).
// Removing an override reveals the current source value. This never writes upstream.
type Patch struct {
	Definition json.RawMessage
	Metadata   json.RawMessage
}

func validKind(k Kind) bool {
	switch k {
	case AgentKind, TeamKind, AssignmentKind, WorkflowKind, ProjectKind, MachineKind, RepositoryKind, PlanKind, TaskKind, EpicKind, SprintKind, CollectionKind, WorkstreamKind:
		return true
	}
	return false
}
func validateScope(s SourceScope) error {
	if !validKind(s.Kind) || strings.TrimSpace(s.Provider) == "" || strings.TrimSpace(s.Instance) == "" || strings.TrimSpace(s.Collection) == "" {
		return fmt.Errorf("%w: complete source scope required", ErrInvalid)
	}
	return nil
}
func jsonObject(b json.RawMessage) (map[string]json.RawMessage, error) {
	if len(b) == 0 {
		b = json.RawMessage(`{}`)
	}
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("%w: JSON exceeds 1 MiB", ErrInvalid)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, fmt.Errorf("%w: JSON object required", ErrInvalid)
	}
	return m, nil
}
func canonical(b json.RawMessage) (json.RawMessage, error) {
	if _, err := jsonObject(b); err != nil {
		return nil, err
	}
	if len(b) == 0 {
		b = json.RawMessage(`{}`)
	}
	var m map[string]any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := decoder.Decode(&m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}
func strict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

type referenceLink struct {
	Link
	kind Kind
}

func validateDefinition(kind Kind, raw json.RawMessage) (string, []referenceLink, error) {
	if _, err := jsonObject(raw); err != nil {
		return "", nil, err
	}
	var common Common
	// Extract only common fields here; strict decoding of the full type follows.
	if err := json.Unmarshal(raw, &common); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(common.Name) == "" {
		return "", nil, fmt.Errorf("%w: name required", ErrInvalid)
	}
	var links []referenceLink
	link := func(id string, k Kind, relation, slot string) {
		if id != "" {
			links = append(links, referenceLink{Link: Link{ToID: id, Relation: relation, Slot: slot}, kind: k})
		}
	}
	var err error
	switch kind {
	case PlanKind, TaskKind:
		return validatePlanningDefinition(kind, raw)
	case EpicKind, SprintKind, CollectionKind, WorkstreamKind:
		return validateOrganizationDefinition(kind, raw)
	case AgentKind:
		var d Agent
		err = strict(raw, &d)
		link(d.ProjectID, ProjectKind, "project", "")
		link(d.MachineID, MachineKind, "machine", "")
	case ProjectKind:
		var d Project
		err = strict(raw, &d)
		for _, id := range d.RepositoryIDs {
			link(id, RepositoryKind, "repository", "")
		}
	case MachineKind:
		var d Machine
		err = strict(raw, &d)
	case RepositoryKind:
		var d Repository
		err = strict(raw, &d)
	case TeamKind:
		var d Team
		err = strict(raw, &d)
		link(d.ProjectID, ProjectKind, "project", "")
		link(d.WorkflowID, WorkflowKind, "workflow", "")
		seen := map[string]bool{}
		for _, slot := range d.Slots {
			if strings.TrimSpace(slot.Name) == "" || strings.TrimSpace(slot.Role) == "" || seen[slot.Name] || slot.Min < 0 || slot.Max < 1 || slot.Min > slot.Max {
				return "", nil, fmt.Errorf("%w: invalid or duplicate team slot", ErrInvalid)
			}
			seen[slot.Name] = true
			link(slot.AgentID, AgentKind, "member", slot.Name)
		}
		if d.Coordinator != "" && !seen[d.Coordinator] {
			return "", nil, fmt.Errorf("%w: coordinator slot missing", ErrInvalid)
		}
	case AssignmentKind:
		var d Assignment
		err = strict(raw, &d)
		if (d.AgentID == "") == (d.TeamID == "") || (d.Target == nil) == (d.ExternalTarget == nil) {
			return "", nil, fmt.Errorf("%w: one assignee and one target required", ErrInvalid)
		}
		switch d.State {
		case "draft", "active", "completed", "cancelled":
		default:
			return "", nil, fmt.Errorf("%w: assignment state", ErrInvalid)
		}
		link(d.AgentID, AgentKind, "assignee", "")
		link(d.TeamID, TeamKind, "assignee", "")
		if d.Target != nil {
			if !validKind(d.Target.Kind) || d.Target.ID == "" {
				return "", nil, ErrInvalid
			}
			link(d.Target.ID, d.Target.Kind, "target", "")
		}
		if d.ExternalTarget != nil {
			e := d.ExternalTarget
			if e.Provider == "" || e.Instance == "" || e.Collection == "" || e.ExternalID == "" {
				return "", nil, ErrInvalid
			}
		}
	case WorkflowKind:
		var d Workflow
		err = strict(raw, &d)
		link(d.ProjectID, ProjectKind, "project", "")
		steps := map[string]WorkflowStep{}
		for _, step := range d.Steps {
			if step.ID == "" || step.Verb == "" {
				return "", nil, ErrInvalid
			}
			if _, ok := steps[step.ID]; ok {
				return "", nil, fmt.Errorf("%w: duplicate step", ErrInvalid)
			}
			steps[step.ID] = step
		}
		visiting, done := map[string]bool{}, map[string]bool{}
		var visit func(string) bool
		visit = func(id string) bool {
			if done[id] {
				return true
			}
			step, ok := steps[id]
			if !ok || visiting[id] {
				return false
			}
			visiting[id] = true
			for _, dep := range step.DependsOn {
				if !visit(dep) {
					return false
				}
			}
			visiting[id] = false
			done[id] = true
			return true
		}
		for id := range steps {
			if !visit(id) {
				return "", nil, fmt.Errorf("%w: missing or cyclic workflow dependency", ErrInvalid)
			}
		}
	default:
		return "", nil, fmt.Errorf("%w: unknown kind", ErrInvalid)
	}
	return common.Name, links, err
}

// merge applies RFC 7396 object merge; overlay preserves explicit null while
// combining observed and authored data, so an override can mask an observed key.
func merge(base, patch json.RawMessage, removeNull bool) (json.RawMessage, error) {
	a, err := jsonObject(base)
	if err != nil {
		return nil, err
	}
	b, err := jsonObject(patch)
	if err != nil {
		return nil, err
	}
	for k, v := range b {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) && removeNull {
			delete(a, k)
			continue
		}
		if len(v) > 0 && v[0] == '{' {
			old := a[k]
			if len(old) == 0 || old[0] != '{' {
				old = json.RawMessage(`{}`)
			}
			v, err = merge(old, v, removeNull)
			if err != nil {
				return nil, err
			}
		}
		a[k] = v
	}
	return json.Marshal(a)
}

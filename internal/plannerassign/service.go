// Package plannerassign implements the Planner assigned_to relation (PL2) on the existing `assignment`
// object kind. An assignment is an object with `assignee` and `target` links; planner.list already
// filters AssignedTo through them. Nothing here writes Torque's own claim or executor fields.
package plannerassign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

const (
	StateActive    = "active"
	StateCompleted = "completed"
	StateCancelled = "cancelled"
	MaxNote        = 300
	maxScan        = 5000
)

// ErrAssignee means the agent or team does not exist in the object store.
var ErrAssignee = errors.New("unknown assignee")

type Service struct {
	Objects *objects.Service
	Owned   *planner.Owned
	// TorqueInstance names the Torque instance in external task targets.
	TorqueInstance string
}

func New(o *objects.Service, torqueInstance string) *Service {
	if torqueInstance == "" {
		torqueInstance = "torque"
	}
	return &Service{Objects: o, Owned: &planner.Owned{Objects: o}, TorqueInstance: torqueInstance}
}

// Target is {db,id} for a planner item or {kind:"torque.task",ref} for a Torque task.
type Target struct {
	DB   string `json:"db,omitempty"`
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

// Create is the request to start an assignment.
type Create struct {
	Target  Target `json:"target"`
	AgentID string `json:"agent_id,omitempty"`
	TeamID  string `json:"team_id,omitempty"`
	Note    string `json:"note,omitempty"`
}

// View is the wire shape of an assignment.
type View struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	AgentID   string `json:"agent_id"`
	TeamID    string `json:"team_id"`
	Label     string `json:"label,omitempty"`
	Target    Target `json:"target"`
	Note      string `json:"note"`
	Since     string `json:"since"`
	EndedAt   string `json:"ended_at,omitempty"`
	Primary   bool   `json:"primary,omitempty"`
	Revision  int64  `json:"revision"`
	assignee  string
	targetObj string
}

func (s *Service) Create(ctx context.Context, c Create, actor string) (View, error) {
	if (c.AgentID == "") == (c.TeamID == "") {
		return View{}, fmt.Errorf("%w: exactly one of agent_id and team_id", objects.ErrInvalid)
	}
	if len(c.Note) > MaxNote {
		return View{}, fmt.Errorf("%w: note is at most %d characters", objects.ErrInvalid, MaxNote)
	}
	who, label, err := s.assignee(ctx, c.AgentID, c.TeamID)
	if err != nil {
		return View{}, err
	}
	d := objects.Assignment{Common: objects.Common{Name: label + " -> " + targetName(c.Target), Description: c.Note}, State: StateActive}
	if c.AgentID != "" {
		d.AgentID = who
	} else {
		d.TeamID = who
	}
	switch {
	case c.Target.Kind == "torque.task" && c.Target.Ref != "" && c.Target.DB == "" && c.Target.ID == "":
		d.ExternalTarget = &objects.ExternalReference{Provider: "torque", Instance: s.TorqueInstance, Collection: "tasks", ExternalID: c.Target.Ref}
	case c.Target.DB != "" && c.Target.ID != "" && c.Target.Kind == "" && c.Target.Ref == "":
		rec, err := s.Owned.Resolve(ctx, c.Target.DB, c.Target.ID)
		if err != nil {
			return View{}, err
		}
		d.Target = &objects.Reference{ID: rec.ID, Kind: rec.Kind}
	default:
		return View{}, fmt.Errorf("%w: target must be {db,id} or {kind:torque.task,ref}", objects.ErrInvalid)
	}
	rec, err := s.Objects.Create(ctx, d, nil, actor)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, rec, nil)
}

// End moves an active assignment to completed or cancelled. Ending an ended assignment conflicts.
func (s *Service) End(ctx context.Context, id, state, actor string) (View, error) {
	rec, err := s.Objects.Get(ctx, id)
	if err != nil || rec.Kind != objects.AssignmentKind || rec.Deleted() {
		return View{}, objects.ErrNotFound
	}
	var d objects.Assignment
	if err := json.Unmarshal(rec.Definition, &d); err != nil {
		return View{}, err
	}
	if d.State != StateActive {
		return View{}, objects.ErrConflict
	}
	patch, _ := json.Marshal(map[string]string{"state": state})
	out, err := s.Objects.Patch(ctx, id, rec.Revision, objects.Patch{Definition: patch}, actor)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, out, nil)
}

// Filter narrows List. Target is "<db>/<id>" or a Torque task id; Agent is a catalog id or an object id.
type Filter struct{ Agent, Target, State string }

func (s *Service) List(ctx context.Context, f Filter) ([]View, error) {
	if f.State != "" && f.State != StateActive && f.State != StateCompleted && f.State != StateCancelled {
		return nil, fmt.Errorf("%w: state", objects.ErrInvalid)
	}
	names, err := s.names(ctx)
	if err != nil {
		return nil, err
	}
	wantAgent := ""
	if f.Agent != "" {
		wantAgent = names.objectFor(f.Agent)
		if wantAgent == "" {
			return []View{}, nil
		}
	}
	wantTarget, wantRef := "", ""
	if f.Target != "" {
		if db, id, ok := strings.Cut(f.Target, "/"); ok {
			rec, err := s.Owned.Resolve(ctx, db, id)
			if err != nil {
				return []View{}, nil
			}
			wantTarget = rec.ID
		} else {
			wantRef = f.Target
		}
	}
	recs, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, r := range recs {
		v, err := s.view(ctx, r, names)
		if err != nil {
			continue
		}
		switch {
		case f.State != "" && v.State != f.State,
			wantAgent != "" && v.assignee != wantAgent,
			wantTarget != "" && v.targetObj != wantTarget,
			wantRef != "" && v.Target.Ref != wantRef:
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	markPrimary(out)
	return out, nil
}

// Assignees returns the active assignees of one planner item (by object id) or Torque task (by id), earliest first.
func (s *Service) Assignees(ctx context.Context, target string) ([]View, error) {
	all, err := s.List(ctx, Filter{State: StateActive})
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, v := range all {
		if v.targetObj == target || v.Target.Ref == target {
			out = append(out, v)
		}
	}
	markPrimary(out)
	return out, nil
}

func markPrimary(v []View) {
	seen := map[string]bool{}
	for i := range v {
		key := v[i].targetObj + "|" + v[i].Target.Ref
		if v[i].State == StateActive && !seen[key] {
			v[i].Primary, seen[key] = true, true
		}
	}
}

func targetName(t Target) string {
	if t.Ref != "" {
		return t.Ref
	}
	return t.DB + "/" + t.ID
}

func (s *Service) all(ctx context.Context) ([]objects.Record, error) {
	var out []objects.Record
	after := ""
	for len(out) < maxScan {
		page, err := s.Objects.List(ctx, objects.Filter{Kind: objects.AssignmentKind, AfterID: after, Limit: 500})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].ID
	}
	return out, nil
}

// nameIndex maps agent and team objects to the ids clients use.
type nameIndex struct {
	label map[string]string // object id -> label
	pub   map[string]string // object id -> public id
	alias map[string]string // any accepted id -> object id
}

func (n *nameIndex) objectFor(id string) string { return n.alias[id] }

func (s *Service) names(ctx context.Context) (*nameIndex, error) {
	n := &nameIndex{label: map[string]string{}, pub: map[string]string{}, alias: map[string]string{}}
	for _, kind := range []objects.Kind{objects.AgentKind, objects.TeamKind} {
		after := ""
		for {
			page, err := s.Objects.List(ctx, objects.Filter{Kind: kind, AfterID: after, Limit: 500})
			if err != nil {
				return nil, err
			}
			for _, r := range page {
				pub := r.ID
				if r.Source != nil && r.Source.Key.ExternalID != "" {
					pub = r.Source.Key.ExternalID
				}
				n.label[r.ID], n.pub[r.ID] = r.Name, pub
				n.alias[r.ID], n.alias[pub] = r.ID, r.ID
			}
			if len(page) < 500 {
				break
			}
			after = page[len(page)-1].ID
		}
	}
	return n, nil
}

func (s *Service) assignee(ctx context.Context, agent, team string) (string, string, error) {
	n, err := s.names(ctx)
	if err != nil {
		return "", "", err
	}
	id := agent + team
	obj := n.objectFor(id)
	if obj == "" {
		return "", "", fmt.Errorf("%w: %s", ErrAssignee, id)
	}
	rec, err := s.Objects.Get(ctx, obj)
	if err != nil || (agent != "" && rec.Kind != objects.AgentKind) || (team != "" && rec.Kind != objects.TeamKind) {
		return "", "", fmt.Errorf("%w: %s", ErrAssignee, id)
	}
	return obj, n.label[obj], nil
}

func (s *Service) view(ctx context.Context, r objects.Record, n *nameIndex) (View, error) {
	var d objects.Assignment
	if err := json.Unmarshal(r.Definition, &d); err != nil {
		return View{}, err
	}
	if n == nil {
		var err error
		if n, err = s.names(ctx); err != nil {
			return View{}, err
		}
	}
	v := View{ID: r.ID, State: d.State, Note: d.Description, Since: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), Revision: r.Revision}
	if d.State == StateCompleted || d.State == StateCancelled {
		v.EndedAt = r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	v.assignee = d.AgentID + d.TeamID
	v.Label = n.label[v.assignee]
	if d.AgentID != "" {
		v.AgentID = n.pub[d.AgentID]
	} else {
		v.TeamID = n.pub[d.TeamID]
	}
	switch {
	case d.ExternalTarget != nil:
		v.Target = Target{Kind: "torque.task", Ref: d.ExternalTarget.ExternalID}
	case d.Target != nil:
		v.targetObj = d.Target.ID
		v.Target = Target{DB: planner.PublicDB(dbOf(d.Target.Kind)), ID: d.Target.ID}
	}
	return v, nil
}

var publicDBs = []string{"themes", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"}

func dbOf(k objects.Kind) string {
	for _, db := range publicDBs {
		if kind, ok := planner.DatabaseKind(db); ok && kind == k {
			return db
		}
	}
	return ""
}

func init() {
	planner.AssignedTo = func(ctx context.Context, o *objects.Service, assignee string) (map[string]bool, error) {
		return New(o, "").AssignedTargets(ctx, assignee)
	}
}

// AssignedTargets is the set of planner object ids an agent or team (catalog id or object id) is actively
// assigned to. An unknown assignee has none; a nil result is never returned, so callers can tell "filter on
// nothing" from "no filter".
func (s *Service) AssignedTargets(ctx context.Context, assignee string) (map[string]bool, error) {
	views, err := s.List(ctx, Filter{Agent: assignee, State: StateActive})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, v := range views {
		if v.targetObj != "" {
			out[v.targetObj] = true
		}
	}
	return out, nil
}

// Assign starts an assignment unless the same assignee already holds an active one on the target, in which
// case that one is returned (assign is safe to repeat).
func (s *Service) Assign(ctx context.Context, c Create, actor string) (View, error) {
	if (c.AgentID == "") == (c.TeamID == "") {
		return View{}, fmt.Errorf("%w: exactly one of agent_id and team_id", objects.ErrInvalid)
	}
	existing, err := s.activeFor(ctx, c.AgentID+c.TeamID, c.Target)
	if err != nil {
		return View{}, err
	}
	if len(existing) > 0 {
		return existing[0], nil
	}
	return s.Create(ctx, c, actor)
}

// Unassign cancels the assignee's active assignments on the target and returns them. None is not an error.
func (s *Service) Unassign(ctx context.Context, c Create, actor string) ([]View, error) {
	if (c.AgentID == "") == (c.TeamID == "") {
		return nil, fmt.Errorf("%w: exactly one of agent_id and team_id", objects.ErrInvalid)
	}
	active, err := s.activeFor(ctx, c.AgentID+c.TeamID, c.Target)
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, v := range active {
		ended, err := s.End(ctx, v.ID, StateCancelled, actor)
		if err != nil {
			return nil, err
		}
		out = append(out, ended)
	}
	return out, nil
}

func (s *Service) activeFor(ctx context.Context, assignee string, t Target) ([]View, error) {
	f := Filter{Agent: assignee, State: StateActive}
	switch {
	case t.Kind == "torque.task" && t.Ref != "":
		f.Target = t.Ref
	case t.DB != "" && t.ID != "":
		f.Target = t.DB + "/" + t.ID
	default:
		return nil, fmt.Errorf("%w: target must be {db,id} or {kind:torque.task,ref}", objects.ErrInvalid)
	}
	return s.List(ctx, f)
}

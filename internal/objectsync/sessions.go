package objectsync

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

type TetherSessions struct {
	remote
	scope objects.SourceScope
}

func NewTetherSessions(addr, tokenFile string) (*TetherSessions, error) {
	p, err := NewTetherProjects(addr, tokenFile)
	if err != nil {
		return nil, err
	}
	scope := p.scope
	scope.Collection, scope.Kind = "sessions", objects.SessionKind
	return &TetherSessions{remote: p.remote, scope: scope}, nil
}
func (*TetherSessions) Name() string                 { return "tether-sessions" }
func (s *TetherSessions) Scope() objects.SourceScope { return s.scope }

// Only explicit safe DTO fields cross this boundary. Workspace, process IDs,
// environment/configuration, logs and credentials are not persisted here.
type tetherSession struct {
	ID             string  `json:"id"`
	LaunchID       string  `json:"launch_id"`
	ProjectID      string  `json:"project_id"`
	LogicalAgentID string  `json:"logical_agent_id"`
	ProviderID     string  `json:"provider_id"`
	ProviderKind   string  `json:"provider_kind"`
	State          string  `json:"state"`
	ExitCode       *int    `json:"exit_code"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	EndedAt        *string `json:"ended_at"`
	SessionGroupID string  `json:"session_group_id"`
	WorkstreamID   string  `json:"workstream_id"`
}

func (s *TetherSessions) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	c := collect(s.scope, at)
	cursor := ""
	for page := 1; page <= 100; page++ {
		path := "/sessions?limit=1000"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var envelope map[string]json.RawMessage
		if _, err := s.get(ctx, path, "", &envelope); err != nil {
			return Snapshot{}, err
		}
		for key := range envelope {
			if key != "sessions" && key != "next_cursor" {
				return Snapshot{}, scanError("unrecognized Tether session envelope")
			}
		}
		raw, ok := envelope["sessions"]
		if !ok || string(raw) == "null" {
			return Snapshot{}, scanError("missing session inventory")
		}
		var rows []tetherSession
		var next string
		if err := json.Unmarshal(raw, &rows); err != nil {
			return Snapshot{}, scanError("invalid session inventory")
		}
		if raw, ok := envelope["next_cursor"]; ok {
			if string(raw) == "null" || json.Unmarshal(raw, &next) != nil {
				return Snapshot{}, scanError("invalid session cursor")
			}
		}
		if len(rows) > 1000 || (len(rows) == 1000 && next == "") {
			return Snapshot{}, scanError("inconsistent session page")
		}
		for _, r := range rows {
			switch r.State {
			case "created", "ready", "launching", "running", "detached", "orphaned", "completed", "failed", "killed":
			default:
				return Snapshot{}, scanError("unknown Tether session state")
			}
			opened, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
			if err != nil || opened.IsZero() {
				return Snapshot{}, scanError("invalid session creation timestamp")
			}
			if _, err = time.Parse(time.RFC3339Nano, r.UpdatedAt); err != nil {
				return Snapshot{}, scanError("invalid session update timestamp")
			}
			var ended *time.Time
			if r.EndedAt != nil {
				t, err := time.Parse(time.RFC3339Nano, *r.EndedAt)
				if err != nil || t.Before(opened) {
					return Snapshot{}, scanError("invalid session end timestamp")
				}
				ended = &t
			}
			d := objects.Session{Common: objects.Common{Name: r.ID, Properties: map[string]any{"tether": map[string]any{
				"launch_ref": r.LaunchID, "project_ref": r.ProjectID, "agent_ref": r.LogicalAgentID, "workstream_ref": r.WorkstreamID, "group_ref": r.SessionGroupID, "provider_kind": r.ProviderKind, "created_at": r.CreatedAt, "updated_at": r.UpdatedAt,
			}}}, Status: r.State, Runtime: r.ProviderID, OpenedAt: &opened, EndedAt: ended, ExitCode: r.ExitCode}
			if err = c.add(r.ID, r.UpdatedAt, false, d); err != nil {
				return Snapshot{}, err
			}
		}
		if next != "" {
			stamp, err := time.Parse(time.RFC3339Nano, next)
			if err != nil || len(rows) == 0 || next != rows[len(rows)-1].CreatedAt {
				return Snapshot{}, scanError("invalid session continuation")
			}
			if cursor != "" {
				previous, _ := time.Parse(time.RFC3339Nano, cursor)
				if !stamp.Before(previous) {
					return Snapshot{}, scanError("nonadvancing session cursor")
				}
			}
		}
		if err := progress(Progress{Pages: page, Items: len(c.snapshot.Observations), Checkpoint: "session_inventory_observed"}); err != nil {
			return Snapshot{}, err
		}
		// Pagination is timestamp-only upstream, without an ID tie-break. Even
		// exhausted pages are not proof of completeness or deletion authority.
		if next == "" {
			return c.snapshot, nil
		}
		cursor = next
	}
	return Snapshot{}, scanError("session inventory exceeded page limit")
}

// Resolve only exact same-instance source IDs/declared aliases. Late-arriving
// targets resolve on a later scan, while sparse local overrides continue to win.
func (s *TetherSessions) Resolve(ctx context.Context, snapshot Snapshot, service *objects.Service) (Snapshot, error) {
	indexes := map[objects.Kind]map[string]string{}
	for _, kind := range []objects.Kind{objects.AgentKind, objects.ProjectKind, objects.WorkstreamKind} {
		index := map[string]string{}
		indexes[kind] = index
		add := func(alias, id string) {
			if alias == "" {
				return
			}
			if old, ok := index[alias]; ok && old != id {
				index[alias] = ""
			} else {
				index[alias] = id
			}
		}
		f := objects.Filter{Kind: kind, Limit: 500}
		for {
			rows, err := service.List(ctx, f)
			if err != nil {
				return Snapshot{}, err
			}
			for _, r := range rows {
				src := r.Source
				if src == nil || src.DeletedAt != nil || src.Key.Provider != "tether" || src.Key.Instance != s.scope.Instance {
					continue
				}
				want := map[objects.Kind]string{objects.AgentKind: "catalog/agents", objects.ProjectKind: "registry/projects", objects.WorkstreamKind: "workstreams"}[kind]
				if src.Key.Collection != want {
					continue
				}
				add(src.Key.ExternalID, r.ID)
				var d struct {
					Properties struct {
						Tether struct {
							URN         string `json:"urn"`
							ExternalIDs []struct {
								Substrate  string `json:"substrate"`
								ExternalID string `json:"external_id"`
							} `json:"external_ids"`
						} `json:"tether"`
					} `json:"properties"`
				}
				if err := json.Unmarshal(src.Definition, &d); err != nil {
					return Snapshot{}, err
				}
				if kind == objects.AgentKind {
					add(d.Properties.Tether.URN, r.ID)
				}
				if kind == objects.ProjectKind {
					for _, alias := range d.Properties.Tether.ExternalIDs {
						if alias.Substrate == "tether" {
							add(alias.ExternalID, r.ID)
						}
					}
				}
			}
			if len(rows) < f.Limit {
				break
			}
			f.AfterID = rows[len(rows)-1].ID
		}
	}
	for i := range snapshot.Observations {
		var d objects.Session
		if err := json.Unmarshal(snapshot.Observations[i].Definition, &d); err != nil {
			return Snapshot{}, err
		}
		facts := d.Properties["tether"].(map[string]any)
		for _, target := range []struct {
			name string
			kind objects.Kind
			id   *string
		}{{"project", objects.ProjectKind, &d.ProjectID}, {"agent", objects.AgentKind, &d.AgentID}, {"workstream", objects.WorkstreamKind, &d.WorkstreamID}} {
			ref, _ := facts[target.name+"_ref"].(string)
			if ref == "" {
				continue
			}
			facts[target.name+"_resolution"] = "missing"
			if id, ok := indexes[target.kind][ref]; ok {
				if id == "" {
					facts[target.name+"_resolution"] = "ambiguous"
				} else {
					*target.id = id
					facts[target.name+"_resolution"] = "resolved"
				}
			}
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Observations[i].Definition = raw
	}
	return snapshot, nil
}

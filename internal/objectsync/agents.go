package objectsync

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

// TetherAgents imports reusable catalog definitions, not running sessions or
// launch configurations. Catalog IDs remain the key when a registry URN is added.
type TetherAgents struct {
	remote
	scope objects.SourceScope
}

func NewTetherAgents(addr, tokenFile string) (*TetherAgents, error) {
	p, err := NewTetherProjects(addr, tokenFile)
	if err != nil {
		return nil, err
	}
	scope := p.scope
	scope.Collection, scope.Kind = "catalog/agents", objects.AgentKind
	return &TetherAgents{remote: p.remote, scope: scope}, nil
}
func (*TetherAgents) Name() string                 { return "tether-agents" }
func (s *TetherAgents) Scope() objects.SourceScope { return s.scope }

// Deliberately exclude env, spec, provider overrides and connection configs.
// Permissions below are descriptive provider facts, not Tachyon authorization.
type tetherAgent struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	URN           string   `json:"urn"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model"`
	Enabled       *bool    `json:"enabled"`
	Roles         []string `json:"roles"`
	Skills        []string `json:"skills"`
	SystemPrompt  string   `json:"system_prompt"`
	AgentPrompt   string   `json:"agent_prompt"`
	Extends       string   `json:"extends"`
	Abstract      bool     `json:"abstract"`
	Body          string   `json:"body"`
	Prompts       []string `json:"prompts"`
	ContextFiles  []string `json:"context_files"`
	BootFragments []string `json:"boot_fragments"`
	Permissions   struct {
		Network        bool   `json:"network"`
		DefaultSandbox string `json:"default_sandbox"`
		PermissionMode string `json:"permission_mode"`
	} `json:"permissions"`
	MCPScopes []string `json:"mcp_scopes"`
	MCP       struct {
		// No omitempty: null inherits, [] grants none.
		Servers     []string `json:"servers"`
		ExtractRefs *bool    `json:"extract_refs"`
	} `json:"mcp"`
	EditableAgents   []string `json:"editable_agents"`
	MachineAuthority bool     `json:"machine_authority"`
	MachineAccess    string   `json:"machine_access"`
}
type tetherAgentIdentity struct {
	URN     string `json:"urn"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Status  string `json:"status"`
}

func (s *TetherAgents) inventory(ctx context.Context, path string, out any) error {
	var envelope map[string]json.RawMessage
	if _, err := s.get(ctx, path, "", &envelope); err != nil {
		return err
	}
	raw, ok := envelope["agents"]
	if !ok || len(envelope) != 1 || string(raw) == "null" {
		return scanError("unrecognized Tether agent inventory envelope")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return scanError("invalid Tether agent inventory")
	}
	return nil
}

func (s *TetherAgents) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	var definitions []tetherAgent
	if err := s.inventory(ctx, "/catalog/agents", &definitions); err != nil {
		return Snapshot{}, err
	}
	if len(definitions) > maxItems {
		return Snapshot{}, scanError("agent inventory exceeds bounded scan capacity")
	}
	if err := progress(Progress{Pages: 1, Items: len(definitions), Checkpoint: "catalog_complete"}); err != nil {
		return Snapshot{}, err
	}
	var identities []tetherAgentIdentity
	if err := s.inventory(ctx, "/registry/agents?status=*&include=external_ids", &identities); err != nil {
		return Snapshot{}, err
	}
	if len(identities) > maxItems {
		return Snapshot{}, scanError("agent registry exceeds bounded scan capacity")
	}
	registry := map[string]tetherAgentIdentity{}
	for _, identity := range identities {
		if identity.Kind != "agent" || strings.TrimSpace(identity.URN) == "" {
			return Snapshot{}, scanError("invalid Tether agent registry identity")
		}
		if _, exists := registry[identity.URN]; exists {
			return Snapshot{}, scanError("duplicate Tether agent registry identity")
		}
		registry[identity.URN] = identity
	}
	c := collect(s.scope, at)
	for _, a := range definitions {
		name := a.Name
		if strings.TrimSpace(name) == "" {
			name = a.ID
		}
		facts := map[string]any{
			"catalog_id": a.ID, "urn": a.URN, "extends": a.Extends, "abstract": a.Abstract,
			"agent_prompt": a.AgentPrompt, "body": a.Body, "prompts": a.Prompts,
			"context_files": a.ContextFiles, "boot_fragments": a.BootFragments,
			"permissions": a.Permissions, "mcp_scopes": a.MCPScopes, "mcp": a.MCP,
			"editable_agents": a.EditableAgents, "machine_authority": a.MachineAuthority, "machine_access": a.MachineAccess,
		}
		if identity, ok := registry[a.URN]; ok {
			facts["registry_status"] = identity.Status
			if identity.Project != "" {
				facts["project_ref"], facts["project_resolution"] = identity.Project, "missing"
			}
		}
		d := objects.Agent{Common: objects.Common{Name: name, Description: a.Description, Properties: map[string]any{"tether": facts}},
			Enabled: a.Enabled == nil || *a.Enabled, Runtime: a.Provider, Model: a.Model, Roles: a.Roles, Skills: a.Skills, Instructions: a.SystemPrompt}
		// Catalog presence owns definition lifecycle. Registry status is only a
		// correlation fact; disabled definitions are not deleted definitions.
		if err := c.add(a.ID, "", false, d); err != nil {
			return Snapshot{}, err
		}
	}
	c.snapshot.Authoritative = true
	if err := progress(Progress{Pages: 2, Items: len(definitions), Checkpoint: "inventory_complete"}); err != nil {
		return Snapshot{}, err
	}
	return c.snapshot, nil
}

// Resolve uses only exact IDs from source observations in the same Tether
// instance. Local display names and property overrides cannot create aliases.
// Targets that appear later resolve on the next scan. No per-launch machine
// choice is promoted into a reusable definition's default placement.
func (s *TetherAgents) Resolve(ctx context.Context, snapshot Snapshot, service *objects.Service) (Snapshot, error) {
	index := map[string]string{}
	add := func(alias, id string) {
		if alias == "" {
			return
		}
		if previous, exists := index[alias]; exists && previous != id {
			index[alias] = ""
		} else {
			index[alias] = id
		}
	}
	filter := objects.Filter{Kind: objects.ProjectKind, Limit: 500}
	for {
		rows, err := service.List(ctx, filter)
		if err != nil {
			return Snapshot{}, err
		}
		for _, row := range rows {
			source := row.Source
			if source == nil || source.DeletedAt != nil || source.Key.Provider != "tether" || source.Key.Instance != s.scope.Instance || source.Key.Collection != "registry/projects" {
				continue
			}
			add(source.Key.ExternalID, row.ID)
			var d struct {
				Properties struct {
					Tether struct {
						ExternalIDs []struct {
							Substrate  string `json:"substrate"`
							ExternalID string `json:"external_id"`
						} `json:"external_ids"`
					} `json:"tether"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(source.Definition, &d); err != nil {
				return Snapshot{}, err
			}
			for _, alias := range d.Properties.Tether.ExternalIDs {
				if alias.Substrate == "tether" {
					add(alias.ExternalID, row.ID)
				}
			}
		}
		if len(rows) < filter.Limit {
			break
		}
		filter.AfterID = rows[len(rows)-1].ID
	}
	for i := range snapshot.Observations {
		var d objects.Agent
		if err := json.Unmarshal(snapshot.Observations[i].Definition, &d); err != nil {
			return Snapshot{}, err
		}
		facts, _ := d.Properties["tether"].(map[string]any)
		ref, _ := facts["project_ref"].(string)
		if ref == "" {
			continue
		}
		d.ProjectID = ""
		facts["project_resolution"] = "missing"
		if id, exists := index[ref]; exists {
			if id == "" {
				facts["project_resolution"] = "ambiguous"
			} else {
				d.ProjectID, facts["project_resolution"] = id, "resolved"
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

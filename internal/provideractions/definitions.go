package provideractions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/tachyon/internal/objects"
	"gopkg.in/yaml.v3"
)

// DefinitionOptions records authored requirements, never effective grants.
// Skill digests cover packages; the provider verifies their content at resolve.
type DefinitionOptions struct {
	PermissionProfile string            `json:"permission_profile"`
	ToolCapabilities  map[string]string `json:"tool_capabilities,omitempty"`
	Skills            []agentdef.Skill  `json:"skills,omitempty"`
}
type DefinitionRequest struct {
	ID               string            `json:"id"`
	ObjectID         string            `json:"object_id"`
	ExpectedRevision int64             `json:"expected_revision"`
	Options          DefinitionOptions `json:"options"`
}
type DefinitionArtifact struct {
	File           CatalogFile        `json:"file"`
	Pin            mesh.DefinitionRef `json:"definition_ref"`
	ArtifactDigest string             `json:"artifact_digest"`
}

// Derived from retained bytes on every read, so old publications remain
// reviewable after agent edits without regenerating from current object state.
func publicationArtifact(payload json.RawMessage) *DefinitionArtifact {
	var bundle CatalogBundle
	if json.Unmarshal(payload, &bundle) != nil || len(bundle.Files) != 1 || !strings.HasPrefix(bundle.Files[0].Path, "team-definitions/") {
		return nil
	}
	file := bundle.Files[0]
	d, err := agentdef.Parse([]byte(file.Content))
	if err != nil {
		return nil
	}
	semantic, err := agentdef.Digest(d)
	if err != nil {
		return nil
	}
	return &DefinitionArtifact{File: file, Pin: mesh.DefinitionRef{ID: d.DefinitionID, Revision: d.Revision, Digest: semantic}, ArtifactDigest: agentdef.ArtifactDigest([]byte(file.Content))}
}

func buildDefinition(row objects.Record, options DefinitionOptions) (DefinitionArtifact, error) {
	var out DefinitionArtifact
	var a objects.Agent
	if row.Kind != objects.AgentKind || row.Ownership != "tachyon" || row.Deleted() || json.Unmarshal(row.Definition, &a) != nil || !a.Enabled {
		return out, invalid("Select an enabled, active Tachyon-owned agent.")
	}
	if strings.TrimSpace(a.Instructions) == "" || strings.TrimSpace(options.PermissionProfile) == "" {
		return out, invalid("Agent instructions and an explicit harness permission profile are required.")
	}
	if len(options.ToolCapabilities) != len(a.Tools) || len(options.Skills) != len(a.Skills) {
		return out, invalid("Map every local tool explicitly and pin every declared skill package.")
	}
	tools := make([]string, 0, len(a.Tools))
	seen := map[string]bool{}
	for _, tool := range a.Tools {
		capability := strings.TrimSpace(options.ToolCapabilities[tool])
		if capability == "" || seen[capability] {
			return out, invalid("Tool capability mappings must be complete and distinct.")
		}
		seen[capability] = true
		tools = append(tools, capability)
	}
	skills := make([]agentdef.Skill, 0, len(a.Skills))
	for _, name := range a.Skills {
		found := false
		for _, skill := range options.Skills {
			if skill.Name == name {
				skills = append(skills, skill)
				found = true
				break
			}
		}
		if !found {
			return out, invalid("Each declared skill needs its named package content pin.")
		}
	}
	id := "tachyon-" + row.ID
	purpose := strings.TrimSpace(a.Description)
	if purpose == "" {
		purpose = a.Name
	}
	d := agentdef.Definition{
		SchemaVersion: agentdef.SchemaVersion, DefinitionID: id, Revision: "pending", Name: id, Title: a.Name, Description: purpose,
		Behavior: agentdef.Behavior{Purpose: purpose}, Body: a.Instructions,
		Requirements:   agentdef.Requirements{Tools: tools, Skills: skills},
		HarnessProfile: agentdef.HarnessProfile{Permissions: agentdef.PermissionProfile{Profile: options.PermissionProfile}},
		Continuity:     agentdef.Continuity{Mode: agentdef.Ephemeral},
		Provenance:     map[string]string{"tachyon.agent_id": row.ID, "tachyon.agent_revision": fmt.Sprint(row.Revision)},
	}
	if err := d.Validate(); err != nil {
		return out, invalid("Invalid definition requirements: " + err.Error())
	}
	semantic, err := agentdef.Digest(&d)
	if err != nil {
		return out, err
	}
	// The full semantic hash avoids rebinding a revision label when options change.
	d.Revision = fmt.Sprintf("r%d-%s", row.Revision, strings.TrimPrefix(semantic, "sha256:"))
	front, err := yaml.Marshal(d)
	if err != nil {
		return out, err
	}
	content := "---\n" + string(front) + "---\n\n" + a.Instructions + "\n"
	if len(content) > 128<<10 {
		return out, invalid("Definition exceeds 128 KiB.")
	}
	out = DefinitionArtifact{File: CatalogFile{Path: "team-definitions/" + id + "/" + d.Revision + ".md", Content: content}, Pin: mesh.DefinitionRef{ID: id, Revision: d.Revision, Digest: semantic}, ArtifactDigest: agentdef.ArtifactDigest([]byte(content))}
	return out, nil
}

// PrepareDefinitionPublication reuses the catalog plan/approval/install flow.
// Installing a file does not add it to Tether's launch allowlist or activate it.
func (s *Service) PrepareDefinitionPublication(ctx context.Context, in DefinitionRequest, actor string) (Receipt, error) {
	if err := actorRequired(actor); err != nil {
		return Receipt{}, err
	}
	if !validID(in.ID) || !validID(in.ObjectID) || in.ExpectedRevision < 1 {
		return Receipt{}, invalid("Action, agent and reviewed revision are required.")
	}
	encoded, _ := json.Marshal(in)
	inputDigest := digest(append([]byte("definition-publication\n"), encoded...))
	if old, err := s.Get(ctx, in.ID); err == nil {
		if old.inputDigest != inputDigest {
			return Receipt{}, conflict("This action UUID belongs to another request.")
		}
		return old, nil
	}
	row, err := objects.New(s.Store).Get(ctx, in.ObjectID)
	if err != nil {
		return Receipt{}, err
	}
	if row.Revision != in.ExpectedRevision {
		return Receipt{}, conflict("Agent changed; review its current revision.")
	}
	artifact, err := buildDefinition(row, in.Options)
	if err != nil {
		return Receipt{}, err
	}
	payload, _ := json.Marshal(CatalogBundle{Name: "tachyon-" + row.ID, Files: []CatalogFile{artifact.File}})
	bound, err := s.Bind()
	if err != nil {
		return Receipt{}, unavailable("Tether configuration or credential is unavailable.")
	}
	return s.retain(ctx, in.ID, row, "catalog.plan", payload, inputDigest, "", bound, actor)
}

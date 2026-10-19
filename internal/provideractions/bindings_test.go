package provideractions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/tachyon/internal/objects"
)

type bindingProvider struct {
	*fakeBound
	files map[string]string
	read  func(string)
}

func (f *bindingProvider) CatalogFile(_ context.Context, path string) (CatalogFile, error) {
	if f.read != nil {
		f.read(path)
	}
	content, ok := f.files[path]
	if !ok {
		return CatalogFile{}, errors.New("missing file")
	}
	return CatalogFile{Path: path, Content: content}, nil
}
func bindingFixture(t *testing.T) (*Service, objects.Record, objects.Record, *bindingProvider, DefinitionOptions, DefinitionArtifact) {
	t.Helper()
	s, team, f := fixture(t)
	agent, err := objects.New(s.Store).Create(ctx, objects.Agent{Common: objects.Common{Name: "Worker", Description: "Implement reviewed changes"}, Instructions: "Use the planner.\r\nReport evidence.", Enabled: true, Runtime: "codex", Model: "test-model", Tools: []string{"planner-read"}, Skills: []string{"review"}}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	options := DefinitionOptions{PermissionProfile: "reviewed-worker", ToolCapabilities: map[string]string{"planner-read": "planner:read"}, Skills: []agentdef.Skill{{Name: "review", Content: agentdef.Ref{URI: "skill:review", Digest: "sha256:" + strings.Repeat("c", 64)}}}}
	artifact, err := buildDefinition(agent, options)
	if err != nil {
		t.Fatal(err)
	}
	policy := fmt.Sprintf("version: 1\nlaunches:\n  - launch: worker-launch\n    runtime: codex\n    model: test-model\n    source: catalog:%s\n    definition:\n      id: %s\n      revision: %s\n      digest: %s\n", artifact.File.Path, artifact.Pin.ID, artifact.Pin.Revision, artifact.Pin.Digest)
	provider := &bindingProvider{fakeBound: f, files: map[string]string{artifact.File.Path: artifact.File.Content, "teams-policy.yaml": policy}}
	s.Bind = func() (Bound, error) { return provider, nil }
	return s, agent, team, provider, options, artifact
}
func bindAgent(t *testing.T, s *Service, a objects.Record, options DefinitionOptions) LaunchBinding {
	t.Helper()
	b, err := s.PrepareBinding(ctx, BindingRequest{ID: uuid.NewString(), AgentID: a.ID, ExpectedRevision: a.Revision, LaunchID: "worker-launch", Options: options}, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ConfirmBinding(ctx, BindingConfirm{ID: b.ID, Digest: b.Digest}, "operator:confirm")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func bindTeam(t *testing.T, s *Service, row objects.Record, b LaunchBinding) objects.Record {
	t.Helper()
	var team objects.Team
	_ = json.Unmarshal(row.Definition, &team)
	team.Slots[1].AgentID = b.AgentID
	team.Slots[1].LaunchBindingID = b.ID
	raw, _ := json.Marshal(team)
	updated, err := objects.New(s.Store).Patch(ctx, row.ID, row.Revision, objects.Patch{Definition: raw}, "test")
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
func TestDefinitionPublicationUsesSharedPinsAndExplicitRequirements(t *testing.T) {
	s, a, _, f, options, artifact := bindingFixture(t)
	parsed, err := agentdef.Parse([]byte(artifact.File.Content))
	if err != nil {
		t.Fatal(err)
	}
	pin, err := agentdef.Digest(parsed)
	if err != nil || pin != artifact.Pin.Digest || parsed.Requirements.Tools[0] != "planner:read" || parsed.HarnessProfile.Permissions.Profile != options.PermissionProfile || parsed.Continuity.Mode != agentdef.Ephemeral {
		t.Fatal(parsed, err)
	}
	in := DefinitionRequest{ID: uuid.NewString(), ObjectID: a.ID, ExpectedRevision: a.Revision, Options: options}
	receipt, err := s.PrepareDefinitionPublication(ctx, in, "operator:test")
	if err != nil || f.calls.Load() != 0 || receipt.Verb != "catalog.plan" {
		t.Fatal(receipt, err)
	}
	if receipt.DefinitionArtifact == nil || receipt.DefinitionArtifact.Pin != artifact.Pin || receipt.DefinitionArtifact.ArtifactDigest != artifact.ArtifactDigest {
		t.Fatal("publication receipt lost its exact definition evidence", receipt)
	}
	var bundle CatalogBundle
	_ = json.Unmarshal(receipt.Payload, &bundle)
	if len(bundle.Files) != 1 || bundle.Files[0] != artifact.File {
		t.Fatal(bundle)
	}
	replay, err := s.PrepareDefinitionPublication(ctx, in, "operator:test")
	if err != nil || replay.Digest != receipt.Digest {
		t.Fatal(replay, err)
	}
	options.PermissionProfile = "other-worker"
	different, err := buildDefinition(a, options)
	if err != nil || different.Pin.Revision == artifact.Pin.Revision || different.File.Path == artifact.File.Path {
		t.Fatal(different, err)
	}
	options.ToolCapabilities = nil
	if _, err = buildDefinition(a, options); err == nil {
		t.Fatal("implicit tool grants accepted")
	}
}
func TestBindingReadbackFormationAndPersistentProvenance(t *testing.T) {
	s, a, team, f, options, artifact := bindingFixture(t)
	b := bindAgent(t, s, a, options)
	if b.Evidence.Activation != "catalog_observed_runtime_unverified" || b.ConfirmedBy != "operator:confirm" || f.calls.Load() != 0 {
		t.Fatal(b)
	}
	team = bindTeam(t, s, team, b)
	r := prepare(t, s, team)
	var payload struct {
		Team teams.Team `json:"team"`
	}
	_ = json.Unmarshal(r.Payload, &payload)
	if payload.Team.Slots[1].Definition != artifact.Pin || len(r.BindingDependencies) != 1 || r.BindingDependencies[0].AgentRevision != a.Revision {
		t.Fatal(r)
	}
	restarted := &Service{Store: s.Store, Bind: s.Bind}
	persisted, err := restarted.Get(ctx, r.ID)
	if err != nil || persisted.BindingDependencies[0].BindingID != b.ID {
		t.Fatal(persisted, err)
	}
	page, err := restarted.Bindings(ctx, BindingFilter{AgentID: a.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Digest != b.Digest {
		t.Fatal(page, err)
	}
	encoded, _ := json.Marshal(b)
	if strings.Contains(string(encoded), f.identity) {
		t.Fatal("credential fingerprint exposed")
	}
	if _, err = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); err != nil || f.calls.Load() != 1 {
		t.Fatal(err)
	}
}
func TestBindingReadbackRejectsDriftAndUnsupportedPlacement(t *testing.T) {
	for _, mode := range []string{"content", "policy_pin", "runtime", "model", "launch", "source", "duplicate", "placement", "read_race"} {
		t.Run(mode, func(t *testing.T) {
			s, a, _, f, options, artifact := bindingFixture(t)
			switch mode {
			case "content":
				f.files[artifact.File.Path] += "\nUnreviewed instructions.\n"
			case "policy_pin":
				f.files["teams-policy.yaml"] = strings.ReplaceAll(f.files["teams-policy.yaml"], artifact.Pin.Digest, "sha256:"+strings.Repeat("d", 64))
			case "runtime":
				f.files["teams-policy.yaml"] = strings.ReplaceAll(f.files["teams-policy.yaml"], "runtime: codex", "runtime: claude")
			case "model":
				f.files["teams-policy.yaml"] = strings.ReplaceAll(f.files["teams-policy.yaml"], "model: test-model", "model: other-model")
			case "launch":
				f.files["teams-policy.yaml"] = strings.ReplaceAll(f.files["teams-policy.yaml"], "worker-launch", "another-launch")
			case "source":
				f.files["teams-policy.yaml"] = strings.ReplaceAll(f.files["teams-policy.yaml"], "catalog:"+artifact.File.Path, "catalog:other.md")
			case "duplicate":
				f.files["teams-policy.yaml"] += "version: 1\n"
			case "placement":
				machine, err := objects.New(s.Store).Create(ctx, objects.Machine{Common: objects.Common{Name: "Machine"}}, nil, "test")
				if err != nil {
					t.Fatal(err)
				}
				a, err = objects.New(s.Store).Patch(ctx, a.ID, a.Revision, objects.Patch{Definition: json.RawMessage(fmt.Sprintf(`{"machine_id":%q}`, machine.ID))}, "test")
				if err != nil {
					t.Fatal(err)
				}
			case "read_race":
				f.read = func(string) {
					f.read = nil
					if _, err := objects.New(s.Store).Patch(ctx, a.ID, a.Revision, objects.Patch{Definition: json.RawMessage(`{"enabled":false}`)}, "test"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.PrepareBinding(ctx, BindingRequest{ID: uuid.NewString(), AgentID: a.ID, ExpectedRevision: a.Revision, LaunchID: "worker-launch", Options: options}, "operator:test"); err == nil || f.calls.Load() != 0 {
				t.Fatal("unsafe binding accepted", err)
			}
		})
	}
}
func TestBindingGatesFirstDispatchButPreservesUnknownRecovery(t *testing.T) {
	for _, mode := range []string{"edit", "disable", "retire", "revoke", "credential"} {
		t.Run(mode, func(t *testing.T) {
			s, a, team, f, options, _ := bindingFixture(t)
			b := bindAgent(t, s, a, options)
			team = bindTeam(t, s, team, b)
			r := prepare(t, s, team)
			var err error
			switch mode {
			case "edit":
				_, err = objects.New(s.Store).Patch(ctx, a.ID, a.Revision, objects.Patch{Definition: json.RawMessage(`{"instructions":"Changed"}`)}, "test")
			case "disable":
				_, err = objects.New(s.Store).Patch(ctx, a.ID, a.Revision, objects.Patch{Definition: json.RawMessage(`{"enabled":false}`)}, "test")
			case "retire":
				_, err = objects.New(s.Store).SetDeleted(ctx, a.ID, a.Revision, true, "test")
			case "revoke":
				_, err = s.RevokeBinding(ctx, b.ID, "operator:test")
			case "credential":
				f.identity = "rotated"
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); err == nil || f.calls.Load() != 0 {
				t.Fatal("stale binding dispatched", err)
			}
		})
	}
	s, a, team, f, options, _ := bindingFixture(t)
	b := bindAgent(t, s, a, options)
	team = bindTeam(t, s, team, b)
	r := prepare(t, s, team)
	var sent []string
	f.form = func(_ string, raw json.RawMessage) (json.RawMessage, error) {
		sent = append(sent, string(raw))
		if len(sent) == 1 {
			return nil, errors.New("lost response")
		}
		return json.RawMessage(`{"run":{"id":"r1"}}`), nil
	}
	if result, err := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); err != nil || result.State != "unknown" {
		t.Fatal(result, err)
	}
	if _, err := s.RevokeBinding(ctx, b.ID, "operator:test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest, Retry: true}, "operator:test"); err != nil || len(sent) != 2 || sent[0] != sent[1] {
		t.Fatal(err, sent)
	}
}
func TestBindingConfirmationAndMemberAddGates(t *testing.T) {
	s, a, team, f, options, _ := bindingFixture(t)
	in := BindingRequest{ID: uuid.NewString(), AgentID: a.ID, ExpectedRevision: a.Revision, LaunchID: "worker-launch", Options: options}
	b, err := s.PrepareBinding(ctx, in, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmBinding(ctx, BindingConfirm{ID: b.ID, Digest: "sha256:" + strings.Repeat("0", 64)}, "operator:test"); err == nil {
		t.Fatal("wrong digest")
	}
	if _, err = s.ConfirmBinding(ctx, BindingConfirm{ID: b.ID, Digest: b.Digest}, ""); err == nil {
		t.Fatal("missing actor")
	}
	b, err = s.ConfirmBinding(ctx, BindingConfirm{ID: b.ID, Digest: b.Digest}, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	team = bindTeam(t, s, team, b)
	r := prepare(t, s, team)
	if _, err = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); err != nil {
		t.Fatal(err)
	}
	add, err := s.PrepareRuntime(ctx, RuntimeRequest{ID: uuid.NewString(), ObjectID: team.ID, ExpectedRevision: team.Revision, FormationID: r.ID, Operation: "member_add", Slot: "worker"}, "operator:test")
	if err != nil || len(add.BindingDependencies) != 1 {
		t.Fatal(add, err)
	}
	if _, err = s.RevokeBinding(ctx, b.ID, "operator:test"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, ApplyRequest{ID: add.ID, Digest: add.Digest}, "operator:test"); err == nil || f.calls.Load() != 1 {
		t.Fatal("revoked member launched", err)
	}
	if _, err = s.PrepareRuntime(ctx, RuntimeRequest{ID: uuid.NewString(), ObjectID: team.ID, ExpectedRevision: team.Revision, FormationID: r.ID, Operation: "dissolve"}, "operator:test"); err != nil {
		t.Fatal("revocation blocked cleanup", err)
	}
}

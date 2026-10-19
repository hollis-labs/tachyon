package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/util/svcerr"
	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/tachyon/internal/objects"
	"gopkg.in/yaml.v3"
)

type BindingRequest struct {
	ID               string            `json:"id"`
	AgentID          string            `json:"agent_id"`
	ExpectedRevision int64             `json:"expected_revision"`
	LaunchID         string            `json:"launch_id"`
	Options          DefinitionOptions `json:"options"`
}
type BindingConfirm struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type BindingEvidence struct {
	Definition     mesh.DefinitionRef `json:"definition_ref"`
	Source         string             `json:"source"`
	ArtifactDigest string             `json:"artifact_digest"`
	PolicyDigest   string             `json:"policy_digest"`
	LaunchID       string             `json:"launch_id"`
	Runtime        string             `json:"runtime"`
	Model          string             `json:"model,omitempty"`
	Options        DefinitionOptions  `json:"options"`
	// Catalog evidence does not establish which policy the daemon has loaded.
	Activation string `json:"activation"`
}
type LaunchBinding struct {
	ID                    string          `json:"id"`
	AgentID               string          `json:"agent_id"`
	AgentRevision         int64           `json:"agent_revision"`
	Provider              string          `json:"provider"`
	Target                string          `json:"target"`
	Digest                string          `json:"digest"`
	Evidence              BindingEvidence `json:"evidence"`
	State                 string          `json:"state"`
	Actor                 string          `json:"actor"`
	ConfirmedBy           string          `json:"confirmed_by,omitempty"`
	RevokedBy             string          `json:"revoked_by,omitempty"`
	CreatedAt             string          `json:"created_at"`
	ConfirmedAt           string          `json:"confirmed_at,omitempty"`
	RevokedAt             string          `json:"revoked_at,omitempty"`
	identity, inputDigest string
}
type BindingDependency struct {
	Slot          string `json:"slot"`
	BindingID     string `json:"binding_id"`
	AgentID       string `json:"agent_id"`
	AgentRevision int64  `json:"agent_revision"`
	Digest        string `json:"digest"`
}
type BindingFilter struct {
	AgentID  string `json:"agent_id"`
	BeforeID string `json:"before_id,omitempty"`
}
type BindingPage struct {
	Items        []LaunchBinding `json:"items"`
	NextBeforeID string          `json:"next_before_id,omitempty"`
}

const bindingColumns = `id,agent_id,agent_revision,target,credential_identity,input_digest,digest,evidence,state,actor,confirmed_by,revoked_by,created_at,confirmed_at,revoked_at`

func scanBinding(row scanner) (LaunchBinding, error) {
	var b LaunchBinding
	var raw []byte
	err := row.Scan(&b.ID, &b.AgentID, &b.AgentRevision, &b.Target, &b.identity, &b.inputDigest, &b.Digest, &raw, &b.State, &b.Actor, &b.ConfirmedBy, &b.RevokedBy, &b.CreatedAt, &b.ConfirmedAt, &b.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return b, svcerr.New(svcerr.CodeNotFound, "Launch binding not found.")
	}
	if err != nil {
		return b, err
	}
	b.Provider = "tether"
	return b, json.Unmarshal(raw, &b.Evidence)
}
func (s *Service) Binding(ctx context.Context, id string) (LaunchBinding, error) {
	if !validID(id) {
		return LaunchBinding{}, invalid("A binding UUID is required.")
	}
	return scanBinding(s.Store.Reader.QueryRowContext(ctx, "SELECT "+bindingColumns+" FROM agent_launch_bindings WHERE id=?", id))
}
func (s *Service) Bindings(ctx context.Context, in BindingFilter) (BindingPage, error) {
	out := BindingPage{Items: []LaunchBinding{}}
	if !validID(in.AgentID) || in.BeforeID != "" && !validID(in.BeforeID) {
		return out, invalid("Agent and cursor UUIDs are required.")
	}
	query := "SELECT " + bindingColumns + " FROM agent_launch_bindings WHERE agent_id=?"
	args := []any{in.AgentID}
	if in.BeforeID != "" {
		previous, err := s.Binding(ctx, in.BeforeID)
		if err != nil || previous.AgentID != in.AgentID {
			return out, invalid("Cursor does not belong to this agent.")
		}
		query += " AND (created_at,id)<(?,?)"
		args = append(args, previous.CreatedAt, previous.ID)
	}
	rows, err := s.Store.Reader.QueryContext(ctx, query+" ORDER BY created_at DESC,id DESC LIMIT 26", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, b)
	}
	if len(out.Items) > 25 {
		out.Items = out.Items[:25]
		out.NextBeforeID = out.Items[24].ID
	}
	return out, rows.Err()
}

type catalogFileReader interface {
	CatalogFile(context.Context, string) (CatalogFile, error)
}

func (t *tether) CatalogFile(ctx context.Context, path string) (CatalogFile, error) {
	// Only fixed policy and generated definition paths are used by this service.
	raw, err := t.request(ctx, http.MethodGet, "/catalog/files?"+url.Values{"path": {path}}.Encode(), "", nil)
	if err != nil {
		return CatalogFile{}, err
	}
	var out CatalogFile
	if json.Unmarshal(raw, &out) != nil || out.Path != path || len(out.Content) > 256<<10 {
		return out, unavailable("Tether returned an unexpected catalog file.")
	}
	return out, nil
}

type policyLaunch struct {
	Launch     string `yaml:"launch"`
	Runtime    string `yaml:"runtime"`
	Model      string `yaml:"model,omitempty"`
	Source     string `yaml:"source,omitempty"`
	Definition struct {
		ID       string `yaml:"id"`
		Revision string `yaml:"revision"`
		Digest   string `yaml:"digest"`
	} `yaml:"definition"`
}

func launchEvidence(raw string, id string, artifact DefinitionArtifact, a objects.Agent, options DefinitionOptions) (BindingEvidence, error) {
	var policy struct {
		Version          int            `yaml:"version"`
		SessionFormation bool           `yaml:"session_formation"`
		Adoption         bool           `yaml:"adoption"`
		Launches         []policyLaunch `yaml:"launches"`
	}
	var out BindingEvidence
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	decoder.KnownFields(true)
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF || policy.Version != 1 {
		return out, invalid("Unsupported Tether activation policy snapshot.")
	}
	seenLaunch, seenPin := map[string]bool{}, map[mesh.DefinitionRef]bool{}
	for _, l := range policy.Launches {
		pin := mesh.DefinitionRef{ID: l.Definition.ID, Revision: l.Definition.Revision, Digest: l.Definition.Digest}
		if !catalogID.MatchString(l.Launch) || !catalogID.MatchString(l.Runtime) || pin.ID == "" || pin.Revision == "" || !pinDigest.MatchString(pin.Digest) || seenLaunch[l.Launch] || seenPin[pin] {
			return out, invalid("Invalid or ambiguous activation policy snapshot.")
		}
		seenLaunch[l.Launch], seenPin[pin] = true, true
		if l.Launch != id {
			continue
		}
		if pin != artifact.Pin || l.Source != "catalog:"+artifact.File.Path {
			return out, invalid("Launch policy must reference this exact agent definition pin and source.")
		}
		if a.Runtime != "" && a.Runtime != l.Runtime || a.Model != "" && a.Model != l.Model {
			return out, invalid("Selected launch runtime/model differs from the saved agent.")
		}
		out = BindingEvidence{Definition: pin, Source: l.Source, ArtifactDigest: artifact.ArtifactDigest, PolicyDigest: digest([]byte(raw)), LaunchID: l.Launch, Runtime: l.Runtime, Model: l.Model, Options: options, Activation: "catalog_observed_runtime_unverified"}
	}
	if out.LaunchID == "" {
		return out, invalid("Selected launch is not present in the activation policy.")
	}
	return out, nil
}

// PrepareBinding does readback only; it neither edits host policy nor launches.
func (s *Service) PrepareBinding(ctx context.Context, in BindingRequest, actor string) (LaunchBinding, error) {
	var out LaunchBinding
	if err := actorRequired(actor); err != nil {
		return out, err
	}
	if !validID(in.ID) || !validID(in.AgentID) || in.ExpectedRevision < 1 || !catalogID.MatchString(in.LaunchID) {
		return out, invalid("Binding UUID, agent revision and explicit launch ID are required.")
	}
	encoded, _ := json.Marshal(in)
	inputDigest := digest(encoded)
	if old, err := s.Binding(ctx, in.ID); err == nil {
		if old.inputDigest != inputDigest {
			return out, conflict("Binding UUID belongs to another request.")
		}
		return old, nil
	}
	row, err := objects.New(s.Store).Get(ctx, in.AgentID)
	if err != nil {
		return out, err
	}
	if row.Revision != in.ExpectedRevision {
		return out, conflict("Agent changed; review its current revision.")
	}
	artifact, err := buildDefinition(row, in.Options)
	if err != nil {
		return out, err
	}
	var a objects.Agent
	_ = json.Unmarshal(row.Definition, &a)
	if a.ProjectID != "" || a.MachineID != "" {
		return out, invalid("Explicit project/machine placement is not supported by Tether team launch inputs yet; it cannot be silently ignored.")
	}
	bound, err := s.Bind()
	if err != nil {
		return out, unavailable("Tether configuration or credential is unavailable.")
	}
	reader, ok := bound.(catalogFileReader)
	if !ok {
		return out, unavailable("Provider catalog readback is unavailable.")
	}
	readCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	file, err := reader.CatalogFile(readCtx, artifact.File.Path)
	if err != nil {
		return out, err
	}
	if file.Path != artifact.File.Path || len(file.Content) > 128<<10 {
		return out, invalid("Unexpected definition source.")
	}
	actual, err := agentdef.Parse([]byte(file.Content))
	if err != nil {
		return out, invalid("Provider definition failed agentdef validation.")
	}
	semantic, err := agentdef.Digest(actual)
	if err != nil || actual.DefinitionID != artifact.Pin.ID || actual.Revision != artifact.Pin.Revision || semantic != artifact.Pin.Digest {
		return out, invalid("Provider definition does not match this agent revision and its reviewed requirements.")
	}
	artifact.ArtifactDigest = agentdef.ArtifactDigest([]byte(file.Content))
	policy, err := reader.CatalogFile(readCtx, "teams-policy.yaml")
	if err != nil {
		return out, err
	}
	if policy.Path != "teams-policy.yaml" || len(policy.Content) > 256<<10 {
		return out, invalid("Unexpected policy source.")
	}
	evidence, err := launchEvidence(policy.Content, in.LaunchID, artifact, a, in.Options)
	if err != nil {
		return out, err
	}
	raw, _ := json.Marshal(evidence)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	// Cover identity, revision and evidence together in the operator review digest.
	reviewed, _ := json.Marshal(struct {
		AgentID  string
		Revision int64
		Target   string
		Evidence BindingEvidence
	}{row.ID, row.Revision, bound.Target(), evidence})
	err = s.Store.Writer.Submit(ctx, "launch_binding_prepare", func(ctx context.Context, tx *sql.Tx) error {
		if err := agentRevisionTx(ctx, tx, row.ID, row.Revision); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_launch_bindings(id,agent_id,agent_revision,target,credential_identity,input_digest,digest,evidence,state,actor,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, in.ID, row.ID, row.Revision, bound.Target(), bound.Identity(), inputDigest, digest(reviewed), raw, "prepared", actor, stamp)
		return err
	})
	if err != nil {
		return out, err
	}
	out, err = s.Binding(ctx, in.ID)
	if err == nil && out.inputDigest != inputDigest {
		return LaunchBinding{}, conflict("Binding UUID belongs to another request.")
	}
	return out, err
}
func agentRevisionTx(ctx context.Context, tx *sql.Tx, id string, revision int64) error {
	var current int64
	var definition []byte
	var ownership, kind string
	var deleted sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT revision,local_json,ownership,kind,deleted_at FROM objects WHERE id=?", id).Scan(&current, &definition, &ownership, &kind, &deleted); err != nil {
		return err
	}
	var a objects.Agent
	if current != revision || ownership != "tachyon" || kind != "agent" || deleted.Valid || json.Unmarshal(definition, &a) != nil || !a.Enabled {
		return conflict("Bound agent changed, was disabled or retired; review a new binding.")
	}
	return nil
}
func (s *Service) ConfirmBinding(ctx context.Context, in BindingConfirm, actor string) (LaunchBinding, error) {
	if err := actorRequired(actor); err != nil {
		return LaunchBinding{}, err
	}
	if !validID(in.ID) || !pinDigest.MatchString(in.Digest) {
		return LaunchBinding{}, invalid("Binding UUID and reviewed digest are required.")
	}
	bound, err := s.Bind()
	if err != nil {
		return LaunchBinding{}, unavailable("Tether configuration or credential is unavailable.")
	}
	err = s.Store.Writer.Submit(ctx, "launch_binding_confirm", func(ctx context.Context, tx *sql.Tx) error {
		b, err := scanBinding(tx.QueryRowContext(ctx, "SELECT "+bindingColumns+" FROM agent_launch_bindings WHERE id=?", in.ID))
		if err != nil {
			return err
		}
		if b.Digest != in.Digest || b.Target != bound.Target() || b.identity != bound.Identity() {
			return conflict("Binding digest, provider target or credential changed.")
		}
		if b.State == "revoked" {
			return conflict("Binding was revoked; create a new review.")
		}
		if err := agentRevisionTx(ctx, tx, b.AgentID, b.AgentRevision); err != nil {
			return err
		}
		if b.State == "active" {
			return nil
		}
		created, err := time.Parse(time.RFC3339Nano, b.CreatedAt)
		if err != nil || time.Since(created) > 30*time.Minute {
			return conflict("Binding preview expired; read the catalog again under a new UUID.")
		}
		_, err = tx.ExecContext(ctx, "UPDATE agent_launch_bindings SET state='active',confirmed_by=?,confirmed_at=? WHERE id=?", actor, time.Now().UTC().Format(time.RFC3339Nano), in.ID)
		return err
	})
	if err != nil {
		return LaunchBinding{}, err
	}
	return s.Binding(ctx, in.ID)
}
func (s *Service) RevokeBinding(ctx context.Context, id, actor string) (LaunchBinding, error) {
	if err := actorRequired(actor); err != nil {
		return LaunchBinding{}, err
	}
	if !validID(id) {
		return LaunchBinding{}, invalid("Binding UUID is required.")
	}
	err := s.Store.Writer.Submit(ctx, "launch_binding_revoke", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE agent_launch_bindings SET state='revoked',revoked_by=?,revoked_at=? WHERE id=? AND state<>'revoked'", actor, time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	})
	if err != nil {
		return LaunchBinding{}, err
	}
	return s.Binding(ctx, id)
}

func (s *Service) teamBindings(ctx context.Context, row objects.Record, bound Bound) (map[string]LaunchBinding, []BindingDependency, error) {
	var team objects.Team
	if json.Unmarshal(row.Definition, &team) != nil {
		return nil, nil, invalid("Invalid team.")
	}
	bindings := map[string]LaunchBinding{}
	dependencies := []BindingDependency{}
	for _, slot := range team.Slots {
		if slot.AgentID == "" && slot.LaunchBindingID == "" {
			continue
		}
		if slot.AgentID == "" || !validID(slot.LaunchBindingID) {
			return nil, nil, invalid("Each owned-agent slot needs its explicit launch binding UUID.")
		}
		b, err := s.Binding(ctx, slot.LaunchBindingID)
		if err != nil {
			return nil, nil, err
		}
		if b.AgentID != slot.AgentID || b.State != "active" || b.Target != bound.Target() || b.identity != bound.Identity() {
			return nil, nil, conflict("Slot binding is inactive or belongs to a different agent, target or credential.")
		}
		bindings[slot.Name] = b
		dependencies = append(dependencies, BindingDependency{Slot: slot.Name, BindingID: b.ID, AgentID: b.AgentID, AgentRevision: b.AgentRevision, Digest: b.Digest})
	}
	return bindings, dependencies, nil
}
func validateBindingDependenciesTx(ctx context.Context, tx *sql.Tx, deps []BindingDependency, target, identity string) error {
	for _, dep := range deps {
		b, err := scanBinding(tx.QueryRowContext(ctx, "SELECT "+bindingColumns+" FROM agent_launch_bindings WHERE id=?", dep.BindingID))
		if err != nil {
			return err
		}
		if b.State != "active" || b.AgentID != dep.AgentID || b.AgentRevision != dep.AgentRevision || b.Digest != dep.Digest || b.Target != target || b.identity != identity {
			return conflict("Formation launch binding changed or was revoked; prepare a new review.")
		}
		if err := agentRevisionTx(ctx, tx, dep.AgentID, dep.AgentRevision); err != nil {
			return err
		}
	}
	return nil
}

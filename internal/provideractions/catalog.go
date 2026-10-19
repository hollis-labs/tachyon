package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

var catalogID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type PublishRequest struct {
	ID               string `json:"id"`
	ObjectID         string `json:"object_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Provider         string `json:"provider"`
	Extends          string `json:"extends"`
}
type CatalogApplyRequest struct {
	ID           string `json:"id"`
	PlanActionID string `json:"plan_action_id"`
}
type CatalogFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type CatalogBundle struct {
	Name  string        `json:"name"`
	Files []CatalogFile `json:"files"`
}
type CatalogPlan struct {
	ID           string    `json:"plan_id"`
	Digest       string    `json:"digest"`
	State        string    `json:"state"`
	ExpiresAt    time.Time `json:"expires_at"`
	ValidationOK bool      `json:"validation_ok"`
	Files        []struct {
		Path      string `json:"path"`
		Action    string `json:"action"`
		NewSHA256 string `json:"new_sha256,omitempty"`
	} `json:"files"`
	ValidationError  string   `json:"validation_error,omitempty"`
	Privileged       []string `json:"privileged,omitempty"`
	RunningSessions  []string `json:"running_sessions,omitempty"`
	Diff             string   `json:"diff"`
	ApprovalRequired string   `json:"approval_required,omitempty"`
	BackupID         string   `json:"backup_id,omitempty"`
	Activation       string   `json:"activation,omitempty"`
	RestartRequired  []string `json:"restart_required,omitempty"`
}
type catalogApplyPayload struct {
	ID     string `json:"plan_id"`
	Digest string `json:"digest"`
}
type catalogReader interface {
	CatalogPlan(context.Context, string) (CatalogPlan, error)
}

func (s *Service) PreparePublication(ctx context.Context, in PublishRequest, actor string) (Receipt, error) {
	if e := actorRequired(actor); e != nil {
		return Receipt{}, e
	}
	if !validID(in.ID) || !validID(in.ObjectID) || in.ExpectedRevision < 1 || !catalogID.MatchString(in.Provider) || !catalogID.MatchString(in.Extends) {
		return Receipt{}, invalid("Choose a saved agent revision and explicit catalog provider and base profile IDs.")
	}
	raw, _ := json.Marshal(in)
	inputDigest := digest(append([]byte("publish\n"), raw...))
	if old, e := s.Get(ctx, in.ID); e == nil {
		if old.inputDigest != inputDigest {
			return Receipt{}, conflict("This action UUID belongs to another request.")
		}
		return old, nil
	}
	row, e := objects.New(s.Store).Get(ctx, in.ObjectID)
	if e != nil || row.Kind != objects.AgentKind || row.Ownership != "tachyon" || row.Deleted() {
		return Receipt{}, invalid("Select an active owned agent.")
	}
	if row.Revision != in.ExpectedRevision {
		return Receipt{}, conflict("Reload the saved agent before reviewing publication.")
	}
	var agent objects.Agent
	if json.Unmarshal(row.Definition, &agent) != nil {
		return Receipt{}, invalid("Invalid saved agent.")
	}
	if len(agent.Tools) > 0 {
		return Receipt{}, invalid("This agent has local tool declarations. Map them to a Tether MCP policy before publication; this adapter does not silently turn tool names into grants.")
	}
	id := "tachyon-" + row.ID
	if in.Extends == id {
		return Receipt{}, invalid("An agent profile cannot extend itself.")
	}
	// A separate owned catalog profile avoids overwriting a provider observation.
	// JSON is a YAML subset and preserves multiline instruction strings exactly.
	profile := map[string]any{"id": id, "name": agent.Name, "description": agent.Description, "extends": in.Extends, "provider": in.Provider, "model": agent.Model, "roles": agent.Roles, "skills": agent.Skills, "body": agent.Instructions, "enabled": agent.Enabled}
	content, _ := json.MarshalIndent(profile, "", "  ")
	payload, _ := json.Marshal(CatalogBundle{Name: id, Files: []CatalogFile{{Path: "agents/" + id + ".yaml", Content: string(content) + "\n"}}})
	if len(payload) > 256<<10 {
		return Receipt{}, invalid("Publication bundle is too large.")
	}
	bound, e := s.Bind()
	if e != nil {
		return Receipt{}, unavailable("Tether configuration or credential is unavailable.")
	}
	return s.retain(ctx, in.ID, row, "catalog.plan", payload, inputDigest, "", bound, actor)
}
func (s *Service) PrepareCatalogApply(ctx context.Context, in CatalogApplyRequest, actor string) (Receipt, error) {
	if e := actorRequired(actor); e != nil {
		return Receipt{}, e
	}
	if !validID(in.ID) || !validID(in.PlanActionID) {
		return Receipt{}, invalid("Action and catalog-plan receipt UUIDs are required.")
	}
	raw, _ := json.Marshal(in)
	inputDigest := digest(append([]byte("catalog-apply\n"), raw...))
	if old, e := s.Get(ctx, in.ID); e == nil {
		if old.inputDigest != inputDigest {
			return Receipt{}, conflict("Action UUID is already used.")
		}
		return old, nil
	}
	origin, e := s.Get(ctx, in.PlanActionID)
	if e != nil || origin.Verb != "catalog.plan" || origin.State != "succeeded" {
		return Receipt{}, invalid("Select a confirmed catalog-plan receipt.")
	}
	var plan CatalogPlan
	if json.Unmarshal(origin.Result, &plan) != nil || !catalogID.MatchString(plan.ID) || plan.Digest == "" || !plan.ValidationOK {
		return Receipt{}, invalid("The provider plan is invalid or incomplete. Prepare a corrected publication.")
	}
	row, e := objects.New(s.Store).Get(ctx, origin.ObjectID)
	if e != nil || row.Revision != origin.ObjectRevision || row.Deleted() {
		return Receipt{}, conflict("Agent changed after catalog planning. Prepare a new publication.")
	}
	bound, e := s.Bind()
	if e != nil {
		return Receipt{}, unavailable("Tether configuration or credential is unavailable.")
	}
	if bound.Target() != origin.Target || bound.Identity() != origin.identity {
		return Receipt{}, conflict("Provider identity changed after catalog planning.")
	}
	payload, _ := json.Marshal(catalogApplyPayload{ID: plan.ID, Digest: plan.Digest})
	return s.retain(ctx, in.ID, row, "catalog.apply", payload, inputDigest, origin.ID, bound, actor)
}
func (t *tether) CatalogPlan(ctx context.Context, id string) (CatalogPlan, error) {
	var plan CatalogPlan
	if !catalogID.MatchString(id) {
		return plan, invalid("Invalid provider plan ID.")
	}
	raw, e := t.request(ctx, "GET", "/catalog/plans/"+id, "", nil)
	if e != nil {
		return plan, &Failure{Message: "Catalog plan cannot be read. Resolve connectivity or permissions before applying.", NoEffect: true}
	}
	if json.Unmarshal(raw, &plan) != nil || plan.ID != id || plan.Digest == "" {
		return plan, &Failure{Message: "Provider returned a mismatched catalog plan.", NoEffect: true}
	}
	return plan, nil
}
func (t *tether) catalogExecute(ctx context.Context, verb string, payload json.RawMessage) (json.RawMessage, error) {
	if verb == "catalog.plan" {
		var bundle CatalogBundle
		if json.Unmarshal(payload, &bundle) != nil || len(bundle.Files) != 1 {
			return nil, invalid("Invalid publication payload.")
		}
		raw, e := t.request(ctx, "POST", "/catalog/plans", "", payload)
		if e != nil {
			return nil, e
		}
		var plan CatalogPlan
		expectedHash := strings.TrimPrefix(digest([]byte(bundle.Files[0].Content)), "sha256:")
		if json.Unmarshal(raw, &plan) != nil || !catalogID.MatchString(plan.ID) || plan.Digest == "" || len(plan.Files) != 1 || plan.Files[0].Path != bundle.Files[0].Path || plan.Files[0].NewSHA256 != expectedHash {
			return nil, &Failure{Message: "Catalog plan does not match the submitted file. Reconcile it before starting another plan."}
		}
		return json.Marshal(plan)
	}
	var in catalogApplyPayload
	if json.Unmarshal(payload, &in) != nil {
		return nil, invalid("Invalid catalog apply payload.")
	}
	plan, e := t.CatalogPlan(ctx, in.ID)
	if e != nil {
		return nil, e
	}
	if plan.Digest != in.Digest {
		return nil, &Failure{Message: "The provider plan digest changed. Reconcile the plan before continuing.", NoEffect: true}
	}
	if plan.State == "applied" {
		return json.Marshal(map[string]any{"plan_id": plan.ID, "applied": true, "backup_id": plan.BackupID, "activation": plan.Activation, "recovered": true})
	}
	if plan.State != "approved" || !plan.ValidationOK || !plan.ExpiresAt.After(time.Now()) {
		return nil, &Failure{Message: "Tether requires its own approval of this exact unexpired plan. Tachyon operator elevation does not grant provider approval.", NoEffect: true}
	}
	raw, e := t.request(ctx, "POST", "/catalog/plans/"+in.ID+"/apply", "", json.RawMessage(`{}`))
	if e != nil {
		return nil, e
	}
	var result struct {
		ID              string   `json:"plan_id"`
		Applied         bool     `json:"applied"`
		BackupID        string   `json:"backup_id"`
		Activation      string   `json:"activation"`
		Reloaded        bool     `json:"reloaded"`
		RestartRequired []string `json:"restart_required,omitempty"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ID != in.ID || !result.Applied {
		return nil, &Failure{Message: "Catalog installation was not confirmed. Reconcile this plan; do not blindly replay it."}
	}
	return json.Marshal(result)
}

// Reconcile uses provider reads only. Catalog apply has no safe POST retry:
// a timeout must not consume another approval or install the same plan twice.
func (s *Service) Reconcile(ctx context.Context, id, actor string) (Receipt, error) {
	if e := actorRequired(actor); e != nil {
		return Receipt{}, e
	}
	r, e := s.Get(ctx, id)
	if e != nil {
		return r, e
	}
	if r.Verb != "catalog.apply" {
		return r, invalid("This operation has no provider readback contract. Use its documented same-key recovery or inspect the provider.")
	}
	if r.State == "succeeded" {
		return r, nil
	}
	if r.State == "sending" && r.RetryAfter > time.Now().Unix() {
		return r, conflict("The request is still pending.")
	}
	bound, e := s.Bind()
	if e != nil {
		return r, unavailable("Tether configuration is unavailable.")
	}
	if bound.Identity() != r.identity || bound.Target() != r.Target {
		return r, conflict("Provider identity changed. Reconciliation must use the original caller.")
	}
	reader, ok := bound.(catalogReader)
	if !ok {
		return r, unavailable("Catalog readback is unavailable.")
	}
	var payload catalogApplyPayload
	_ = json.Unmarshal(r.Payload, &payload)
	plan, e := reader.CatalogPlan(ctx, payload.ID)
	if e != nil {
		return r, e
	}
	if plan.Digest != payload.Digest {
		return r, conflict("Provider plan digest does not match the reviewed request.")
	}
	if plan.State != "applied" {
		return r, conflict("Provider has not confirmed installation. The retained outcome remains unchanged; no apply was sent.")
	}
	r.State = "succeeded"
	r.Problem = ""
	r.RetryAfter = 0
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.Result, _ = json.Marshal(map[string]any{"plan_id": plan.ID, "applied": true, "backup_id": plan.BackupID, "activation": plan.Activation, "recovered": true})
	e = s.Store.Writer.Submit(ctx, "provider_catalog_reconcile", func(ctx context.Context, tx *sql.Tx) error {
		result, e := tx.ExecContext(ctx, "UPDATE provider_actions SET state=?,result=?,problem='',lease_until=0,updated_at=? WHERE id=? AND attempts=? AND (state<>'sending' OR lease_until<=?)", r.State, r.Result, r.UpdatedAt, r.ID, r.Attempts, time.Now().Unix())
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e == nil && n != 1 {
			return conflict("Action changed while reconciling. Reload its receipt.")
		}
		return e
	})
	return r, e
}

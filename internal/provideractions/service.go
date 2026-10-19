package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

type PrepareRequest struct {
	ID               string `json:"id"`
	ObjectID         string `json:"object_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Mission          string `json:"mission"`
}
type ApplyRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Retry  bool   `json:"retry,omitempty"`
}
type ListRequest struct {
	ObjectID string `json:"object_id"`
	BeforeID string `json:"before_id,omitempty"`
	Verb     string `json:"verb,omitempty"`
}
type Receipt struct {
	DefinitionArtifact    *DefinitionArtifact `json:"definition_artifact,omitempty"`
	BindingDependencies   []BindingDependency `json:"binding_dependencies"`
	ExecutionRunID        string              `json:"execution_run_id,omitempty"`
	ProjectionProblem     string              `json:"projection_problem,omitempty"`
	ID                    string              `json:"id"`
	ObjectID              string              `json:"object_id"`
	ObjectRevision        int64               `json:"object_revision"`
	OriginID              string              `json:"origin_action_id,omitempty"`
	Provider              string              `json:"provider"`
	Target                string              `json:"target"`
	Verb                  string              `json:"verb"`
	Payload               json.RawMessage     `json:"payload"`
	Digest                string              `json:"digest"`
	State                 string              `json:"state"`
	Attempts              int                 `json:"attempts"`
	RetryAfter            int64               `json:"retry_after,omitempty"`
	Result                json.RawMessage     `json:"result"`
	Problem               string              `json:"problem,omitempty"`
	Actor                 string              `json:"actor"`
	CreatedAt             string              `json:"created_at"`
	UpdatedAt             string              `json:"updated_at"`
	identity, inputDigest string
}
type Summary struct {
	ID             string `json:"id"`
	ObjectRevision int64  `json:"object_revision"`
	State          string `json:"state"`
	Verb           string `json:"verb"`
	CreatedAt      string `json:"created_at"`
}
type Page struct {
	Items        []Summary `json:"items"`
	NextBeforeID string    `json:"next_before_id,omitempty"`
}

// Bound is a single credential snapshot. A rotation cannot change the caller
// between checking its fingerprint and sending the pinned request.
type Bound interface {
	Target() string
	Identity() string
	Execute(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
}
type Service struct {
	Store *localstore.Store
	Bind  func() (Bound, error)
}

func invalid(s string) error     { return svcerr.New(svcerr.CodeInvalid, s) }
func conflict(s string) error    { return svcerr.New(svcerr.CodeConflict, s) }
func unavailable(s string) error { return svcerr.New(svcerr.CodeUnavailable, s) }
func validID(id string) bool     { u, e := uuid.Parse(id); return e == nil && u.String() == id }
func actorRequired(actor string) error {
	if actor == "" {
		return svcerr.New(svcerr.CodePermission, "Operator authorization is required.")
	}
	return nil
}

const columns = `id,object_id,object_revision,provider,target,verb,payload,digest,state,attempts,lease_until,result,problem,actor,created_at,updated_at,credential_identity,input_digest,origin_action_id,projection_problem,COALESCE((SELECT run_object_id FROM provider_run_links WHERE formation_action_id=provider_actions.id OR formation_action_id=provider_actions.origin_action_id),''),binding_dependencies`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Receipt, error) {
	var r Receipt
	var dependencies []byte
	err := row.Scan(&r.ID, &r.ObjectID, &r.ObjectRevision, &r.Provider, &r.Target, &r.Verb, &r.Payload, &r.Digest, &r.State, &r.Attempts, &r.RetryAfter, &r.Result, &r.Problem, &r.Actor, &r.CreatedAt, &r.UpdatedAt, &r.identity, &r.inputDigest, &r.OriginID, &r.ProjectionProblem, &r.ExecutionRunID, &dependencies)
	if errors.Is(err, sql.ErrNoRows) {
		return r, svcerr.New(svcerr.CodeNotFound, "Provider action not found.")
	}
	if err == nil {
		err = json.Unmarshal(dependencies, &r.BindingDependencies)
	}
	if err == nil && r.Verb == "catalog.plan" {
		r.DefinitionArtifact = publicationArtifact(r.Payload)
	}
	return r, err
}
func (s *Service) Get(ctx context.Context, id string) (Receipt, error) {
	if !validID(id) {
		return Receipt{}, invalid("A canonical action UUID is required.")
	}
	return scan(s.Store.Reader.QueryRowContext(ctx, "SELECT "+columns+" FROM provider_actions WHERE id=?", id))
}
func (s *Service) List(ctx context.Context, in ListRequest) (Page, error) {
	out := Page{Items: []Summary{}}
	if !validID(in.ObjectID) || (in.BeforeID != "" && !validID(in.BeforeID)) {
		return out, invalid("Valid object and cursor UUIDs are required.")
	}
	query := "SELECT id,object_revision,state,created_at,verb FROM provider_actions WHERE object_id=?"
	args := []any{in.ObjectID}
	if in.Verb != "" {
		query += " AND verb=?"
		args = append(args, in.Verb)
	}
	if in.BeforeID != "" {
		var created string
		if err := s.Store.Reader.QueryRowContext(ctx, "SELECT created_at FROM provider_actions WHERE id=? AND object_id=?", in.BeforeID, in.ObjectID).Scan(&created); err != nil {
			return out, invalid("Cursor does not belong to this object's history.")
		}
		query += " AND (created_at,id)<(?,?)"
		args = append(args, created, in.BeforeID)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT 25"
	rows, err := s.Store.Reader.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Summary
		e := rows.Scan(&r.ID, &r.ObjectRevision, &r.State, &r.CreatedAt, &r.Verb)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, r)
	}
	if len(out.Items) == 25 {
		out.NextBeforeID = out.Items[24].ID
	}
	return out, rows.Err()
}
func (s *Service) Prepare(ctx context.Context, in PrepareRequest, actor string) (Receipt, error) {
	if err := actorRequired(actor); err != nil {
		return Receipt{}, err
	}
	if !validID(in.ID) || !validID(in.ObjectID) || in.ExpectedRevision < 1 {
		return Receipt{}, invalid("Action UUID, team UUID and reviewed revision are required.")
	}
	encoded, _ := json.Marshal(in)
	inputDigest := digest(encoded)
	if old, err := s.Get(ctx, in.ID); err == nil {
		if old.inputDigest != inputDigest {
			return Receipt{}, conflict("This action UUID belongs to another preview.")
		}
		return old, nil
	}
	row, err := objects.New(s.Store).Get(ctx, in.ObjectID)
	if err != nil {
		return Receipt{}, invalid("Saved team is unavailable.")
	}
	if row.Revision != in.ExpectedRevision {
		return Receipt{}, conflict("Team changed. Reload the saved team before previewing formation.")
	}
	bound, err := s.Bind()
	if err != nil {
		return Receipt{}, unavailable("Tether configuration or credential is unavailable.")
	}
	bindings, dependencies, err := s.teamBindings(ctx, row, bound)
	if err != nil {
		return Receipt{}, err
	}
	payload, err := formation(row, in.Mission, in.ID, bindings)
	if err != nil {
		return Receipt{}, err
	}
	return s.retain(ctx, in.ID, row, "teams.form", payload, inputDigest, "", bound, actor, dependencies...)
}
func (s *Service) retain(ctx context.Context, id string, row objects.Record, verb string, payload json.RawMessage, inputDigest, originID string, bound Bound, actor string, dependencies ...BindingDependency) (Receipt, error) {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if dependencies == nil {
		dependencies = []BindingDependency{}
	}
	dependencyJSON, _ := json.Marshal(dependencies)
	r := Receipt{BindingDependencies: dependencies, ID: id, ObjectID: row.ID, ObjectRevision: row.Revision, Provider: "tether", Target: bound.Target(), Verb: verb, Payload: payload, Digest: digest(payload), State: "prepared", Result: json.RawMessage(`{}`), Actor: actor, CreatedAt: stamp, UpdatedAt: stamp, identity: bound.Identity(), inputDigest: inputDigest, OriginID: originID}
	err := s.Store.Writer.Submit(ctx, "provider_action_prepare", func(ctx context.Context, tx *sql.Tx) error {
		var revision int64
		var ownership string
		var deleted sql.NullString
		if e := tx.QueryRowContext(ctx, "SELECT revision,ownership,deleted_at FROM objects WHERE id=?", row.ID).Scan(&revision, &ownership, &deleted); e != nil {
			return e
		}
		if revision != row.Revision || ownership != "tachyon" || deleted.Valid && !cleanupVerb(verb) {
			return conflict("Saved object changed while preparing the action.")
		}
		if e := validateBindingDependenciesTx(ctx, tx, dependencies, bound.Target(), bound.Identity()); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO provider_actions(id,object_id,object_revision,provider,target,verb,payload,digest,state,result,actor,created_at,updated_at,credential_identity,input_digest,origin_action_id,binding_dependencies) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, r.ID, r.ObjectID, r.ObjectRevision, r.Provider, r.Target, r.Verb, r.Payload, r.Digest, r.State, r.Result, r.Actor, r.CreatedAt, r.UpdatedAt, r.identity, r.inputDigest, r.OriginID, dependencyJSON)
		return e
	})
	if err != nil {
		return Receipt{}, err
	}
	old, err := s.Get(ctx, id)
	if err == nil && old.inputDigest != inputDigest {
		err = conflict("This action UUID belongs to another preview.")
	}
	return old, err
}
func cleanupVerb(verb string) bool {
	return verb == "teams.cancel" || verb == "teams.dissolve" || verb == "teams.member_remove"
}

func (s *Service) Apply(ctx context.Context, in ApplyRequest, actor string) (Receipt, error) {
	if err := actorRequired(actor); err != nil {
		return Receipt{}, err
	}
	r, err := s.Get(ctx, in.ID)
	if err != nil {
		return r, err
	}
	if r.Digest != in.Digest {
		return r, conflict("The reviewed action digest does not match.")
	}
	if r.State == "succeeded" {
		return r, nil
	}
	bound, err := s.Bind()
	if err != nil {
		return r, unavailable("Tether configuration or credential is unavailable.")
	}
	if bound.Identity() != r.identity || bound.Target() != r.Target {
		return r, conflict("Tether target or credential changed. Reconcile this action before preparing another; it cannot be replayed under a different caller.")
	}
	var now time.Time
	claimed := false
	err = s.Store.Writer.Submit(ctx, "provider_action_claim", func(ctx context.Context, tx *sql.Tx) error {
		now = time.Now()
		var e error
		r, e = scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM provider_actions WHERE id=?", in.ID))
		if e != nil {
			return e
		}
		if r.State == "succeeded" {
			return nil
		}
		if r.State == "sending" && r.RetryAfter > now.Unix() {
			return conflict("The request is still pending. Refresh its receipt before retrying.")
		}
		if r.State != "prepared" && (r.Verb == "catalog.plan" || r.Verb == "catalog.apply") {
			return conflict("This provider operation does not support safe replay. Reconcile the retained plan outcome instead.")
		}
		if r.State != "prepared" && !in.Retry {
			return conflict("Outcome is unconfirmed. An explicit same-request retry is required.")
		}
		if r.State == "prepared" {
			if e := validateBindingDependenciesTx(ctx, tx, r.BindingDependencies, r.Target, r.identity); e != nil {
				return e
			}
			var revision int64
			var ownership string
			var deleted sql.NullString
			if e = tx.QueryRowContext(ctx, "SELECT revision,ownership,deleted_at FROM objects WHERE id=?", r.ObjectID).Scan(&revision, &ownership, &deleted); e != nil {
				return e
			}
			if revision != r.ObjectRevision || ownership != "tachyon" || deleted.Valid && !cleanupVerb(r.Verb) {
				return conflict("Saved object changed or was retired. Prepare and review a new action.")
			}
			if t, e := time.Parse(time.RFC3339Nano, r.CreatedAt); e != nil || now.Sub(t) > 30*time.Minute {
				return conflict("Preview expired. Prepare and review a new action.")
			}
		}
		// Lease outlives our bounded HTTP call. After a crash, explicit same-key
		// replay delegates recovery to Tether's durable actor/verb/key journal.
		r.Attempts++
		r.State = "sending"
		r.RetryAfter = now.Add(2 * time.Minute).Unix()
		r.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		_, e = tx.ExecContext(ctx, "UPDATE provider_actions SET state=?,attempts=?,lease_until=?,updated_at=?,problem='' WHERE id=?", r.State, r.Attempts, r.RetryAfter, r.UpdatedAt, r.ID)
		claimed = e == nil
		return e
	})
	if err != nil || !claimed {
		return r, err
	}
	// A disconnected browser must not erase a request already accepted locally.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	result, sendErr := bound.Execute(sendCtx, r.Verb, "tachyon-"+r.ID, r.Payload)
	cancel()
	r.State = "unknown"
	r.Result = json.RawMessage(`{}`)
	r.Problem = "Tether did not confirm the outcome. Inspect the receipt and retry this same request to recover it."
	if r.Verb == "catalog.plan" || r.Verb == "catalog.apply" {
		r.Problem = "Tether did not confirm the catalog outcome. Reconcile the retained plan before proceeding; this operation cannot be replayed safely."
	}
	if sendErr == nil {
		r.State = "succeeded"
		r.Result = result
		r.Problem = ""
	}
	// Provider errors are deliberately conservative: even a failure can follow
	// partial provisioning. Never manufacture a new idempotency key on retry.
	if failure, ok := sendErr.(*Failure); ok {
		r.Problem = failure.Message
		if failure.NoEffect {
			r.State = "prepared"
		}
	}
	r.RetryAfter = 0
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	saveCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	err = s.Store.Writer.Submit(saveCtx, "provider_action_receipt", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE provider_actions SET state=?,result=?,problem=?,lease_until=0,updated_at=? WHERE id=? AND attempts=?", r.State, r.Result, r.Problem, r.UpdatedAt, r.ID, r.Attempts)
		if e != nil {
			return e
		}
		return s.projectReceiptTx(ctx, tx, &r)
	})
	if err != nil {
		return Receipt{}, unavailable("Could not retain the provider response. Reload this action; do not create a replacement request.")
	}
	return r, nil
}

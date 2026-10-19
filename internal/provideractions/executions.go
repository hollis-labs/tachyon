package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func reportedStatus(status string) string {
	switch status {
	case "submitted":
		return "queued"
	case "working":
		return "running"
	case "input_required", "auth_required":
		return "blocked"
	case "paused", "completed", "failed":
		return status
	case "canceled":
		return "cancelled"
	default:
		return "unknown"
	}
}

// Run identity is provider-qualified, never a name, member actor or credential.
// Credentials govern actions but rotation does not create another run object.
func runKey(origin Receipt, externalID string) objects.SourceKey {
	instance := origin.Target
	if rest, ok := strings.CutPrefix(instance, "tcp:"); ok {
		instance = "http://" + rest
	}
	return objects.SourceKey{SourceScope: objects.SourceScope{Provider: origin.Provider, Instance: instance, Collection: "teams/runs", Kind: objects.RunKind}, ExternalID: externalID}
}
func (s *Service) observeRunTx(ctx context.Context, tx *sql.Tx, origin Receipt, run teams.TeamRun, at time.Time, version uint64, evidence string) (objects.Record, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT record_json FROM object_revisions WHERE object_id=? AND revision=?", origin.ObjectID, origin.ObjectRevision).Scan(&raw); err != nil {
		return objects.Record{}, err
	}
	var saved objects.Record
	var team objects.Team
	if json.Unmarshal(raw, &saved) != nil || saved.Kind != objects.TeamKind || json.Unmarshal(saved.Definition, &team) != nil {
		return objects.Record{}, invalid("The reviewed team revision cannot be read.")
	}
	definition := objects.Run{Common: objects.Common{Name: team.Name + " · " + run.ID, Properties: map[string]any{"tether": map[string]any{
		"formation_action_id": origin.ID, "team_revision": origin.ObjectRevision, "team_definition_id": run.TeamID, "team_definition_version": run.TeamVersion, "channel": run.Channel, "reported_status": run.Status, "evidence": evidence, "roster_version": version,
	}}}, ExecutionLinks: objects.ExecutionLinks{TeamID: origin.ObjectID, ProjectID: team.ProjectID}, Status: reportedStatus(string(run.Status))}
	// Receipt/observation time is not evidence of actual process start or end.
	body, err := json.Marshal(definition)
	if err != nil {
		return objects.Record{}, err
	}
	return objects.ObserveRunTx(ctx, tx, objects.Observation{Key: runKey(origin, run.ID), ObservedAt: at, Definition: body}, "provider-action:"+origin.ID)
}
func (s *Service) projectTx(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	r, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM provider_actions WHERE id=?", id))
	if err != nil {
		return "", err
	}
	origin := r
	if r.Verb != "teams.form" {
		if !strings.HasPrefix(r.Verb, "teams.") || r.OriginID == "" {
			return "", invalid("Only team runtime receipts have execution projections.")
		}
		origin, err = scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM provider_actions WHERE id=?", r.OriginID))
		if err != nil {
			return "", err
		}
		if origin.Target != r.Target || origin.identity != r.identity {
			return "", conflict("Formation identity does not match the action.")
		}
	}
	if origin.Verb != "teams.form" || origin.State != "succeeded" {
		return "", invalid("A confirmed formation is required; an unknown outcome does not establish a run.")
	}
	var response struct {
		Run *teams.TeamRun `json:"run"`
	}
	if json.Unmarshal(origin.Result, &response) != nil || response.Run == nil || response.Run.ID == "" {
		return "", invalid("Formation has no confirmed provider run.")
	}
	at, err := time.Parse(time.RFC3339Nano, origin.UpdatedAt)
	if err != nil {
		return "", err
	}
	// Reject an attempt to attach the same provider run to two independently
	// reviewed formations. Do not silently reassign its saved-team provenance.
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT formation_action_id FROM provider_run_links WHERE provider=? AND target=? AND external_run_id=?", origin.Provider, origin.Target, response.Run.ID).Scan(&existing)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if existing != "" && existing != origin.ID {
		return "", conflict("Provider run is already associated with another formation.")
	}
	record, err := s.observeRunTx(ctx, tx, origin, *response.Run, at, 0, "formation_receipt")
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_run_links(formation_action_id,run_object_id,provider,target,external_run_id,team_id,team_revision,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(formation_action_id) DO NOTHING`, origin.ID, record.ID, origin.Provider, origin.Target, response.Run.ID, origin.ObjectID, origin.ObjectRevision, origin.UpdatedAt)
	if err != nil {
		return "", err
	}
	// Include the latest retained roster when repairing an older receipt. Reads
	// never fetch a provider here, and older snapshots cannot regress the object.
	var snapshot []byte
	var observed string
	err = tx.QueryRowContext(ctx, "SELECT body,observed_at FROM provider_roster_cache WHERE identity=? AND selection=?", origin.identity, origin.ID).Scan(&snapshot, &observed)
	if err == nil {
		var runs []RosterView
		observedAt, e := time.Parse(time.RFC3339Nano, observed)
		if e != nil || json.Unmarshal(snapshot, &runs) != nil || len(runs) != 1 || runs[0].Run.ID != response.Run.ID || runs[0].Run.TeamID != response.Run.TeamID || runs[0].Run.TeamVersion != response.Run.TeamVersion {
			return "", invalid("Saved roster does not match the confirmed run.")
		}
		if _, err = s.observeRunTx(ctx, tx, origin, runs[0].Run, observedAt, runs[0].Version, "roster_snapshot"); err != nil {
			return "", err
		}
	} else if err != sql.ErrNoRows {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE provider_actions SET projection_problem='' WHERE id IN (?,?)", origin.ID, r.ID); err != nil {
		return "", err
	}
	return record.ID, nil
}

// Preserve a confirmed provider outcome even if local projection fails. A
// savepoint rolls back any partial object/history writes; the same transaction
// retains a repair marker alongside the provider receipt.
func (s *Service) projectReceiptTx(ctx context.Context, tx *sql.Tx, r *Receipt) error {
	if !strings.HasPrefix(r.Verb, "teams.") || r.State != "succeeded" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT execution_projection"); err != nil {
		return err
	}
	id, err := s.projectTx(ctx, tx, r.ID)
	if err != nil {
		if _, rollback := tx.ExecContext(ctx, "ROLLBACK TO execution_projection"); rollback != nil {
			return rollback
		}
		r.ProjectionProblem = "Provider outcome is retained. Local execution tracking needs repair; use executions.project with this action ID."
	} else {
		r.ExecutionRunID = id
		r.ProjectionProblem = ""
	}
	if _, release := tx.ExecContext(ctx, "RELEASE execution_projection"); release != nil {
		return release
	}
	_, err = tx.ExecContext(ctx, "UPDATE provider_actions SET projection_problem=? WHERE id=?", r.ProjectionProblem, r.ID)
	return err
}

// Project repairs/backfills local state only, including after credential
// rotation or an upstream outage. It never binds or calls a provider.
func (s *Service) Project(ctx context.Context, id, actor string) (Receipt, error) {
	if err := actorRequired(actor); err != nil {
		return Receipt{}, err
	}
	if !validID(id) {
		return Receipt{}, invalid("A canonical action UUID is required.")
	}
	err := s.Store.Writer.Submit(ctx, "provider_execution_project", func(ctx context.Context, tx *sql.Tx) error { _, err := s.projectTx(ctx, tx, id); return err })
	if err != nil {
		return Receipt{}, err
	}
	return s.Get(ctx, id)
}

type RunFilter struct {
	TeamID         string `json:"team_id,omitempty"`
	AssignmentID   string `json:"assignment_id,omitempty"`
	BeforeID       string `json:"before_id,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	IncludeDeleted bool   `json:"include_deleted,omitempty"`
}
type RunSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	FormationID    string `json:"formation_id"`
	TeamID         string `json:"team_id"`
	TeamRevision   int64  `json:"team_revision"`
	Provider       string `json:"provider"`
	Target         string `json:"target"`
	ProviderRunID  string `json:"provider_run_id"`
	Status         string `json:"status"`
	ReportedStatus string `json:"reported_status"`
	ObservedAt     string `json:"observed_at"`
	CreatedAt      string `json:"created_at"`
	Deleted        bool   `json:"deleted"`
}
type RunPage struct {
	Items        []RunSummary `json:"items"`
	NextBeforeID string       `json:"next_before_id,omitempty"`
}

func executionLimit(limit int) (int, error) {
	if limit == 0 {
		return 25, nil
	}
	if limit < 1 || limit > 100 {
		return 0, invalid("limit must be between 1 and 100.")
	}
	return limit, nil
}
func (s *Service) Runs(ctx context.Context, in RunFilter) (RunPage, error) {
	out := RunPage{Items: []RunSummary{}}
	limit, err := executionLimit(in.Limit)
	if err != nil {
		return out, err
	}
	for _, id := range []string{in.TeamID, in.AssignmentID, in.BeforeID} {
		if id != "" && !validID(id) {
			return out, invalid("Use canonical local UUIDs for run filters and cursors.")
		}
	}
	query := `SELECT l.run_object_id,o.name,l.formation_action_id,l.team_id,l.team_revision,l.provider,l.target,l.external_run_id,e.status,COALESCE(json_extract(s.snapshot_json,'$.properties.tether.reported_status'),''),s.observed_at,l.created_at,(o.deleted_at IS NOT NULL OR (o.ownership='source' AND s.deleted_at IS NOT NULL)) FROM provider_run_links l JOIN objects o ON o.id=l.run_object_id JOIN execution_items e ON e.object_id=o.id JOIN object_sources s ON s.object_id=o.id WHERE 1=1`
	args := []any{}
	if !in.IncludeDeleted {
		query += " AND o.deleted_at IS NULL AND (o.ownership='tachyon' OR s.deleted_at IS NULL)"
	}
	if in.TeamID != "" {
		query += " AND l.team_id=?"
		args = append(args, in.TeamID)
	}
	if in.AssignmentID != "" {
		query += " AND EXISTS(SELECT 1 FROM provider_actions a WHERE a.origin_action_id=l.formation_action_id AND a.object_id=? AND a.verb='teams.assign' AND a.attempts>0)"
		args = append(args, in.AssignmentID)
	}
	if in.BeforeID != "" {
		var at string
		if err = s.Store.Reader.QueryRowContext(ctx, "SELECT created_at FROM provider_run_links WHERE formation_action_id=?", in.BeforeID).Scan(&at); err != nil {
			return out, invalid("Unknown run cursor.")
		}
		query += " AND (l.created_at,l.formation_action_id)<(?,?)"
		args = append(args, at, in.BeforeID)
	}
	query += " ORDER BY l.created_at DESC,l.formation_action_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.Store.Reader.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var row RunSummary
		if err = rows.Scan(&row.ID, &row.Name, &row.FormationID, &row.TeamID, &row.TeamRevision, &row.Provider, &row.Target, &row.ProviderRunID, &row.Status, &row.ReportedStatus, &row.ObservedAt, &row.CreatedAt, &row.Deleted); err != nil {
			return out, err
		}
		out.Items = append(out.Items, row)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextBeforeID = out.Items[limit-1].FormationID
	}
	return out, nil
}

type ActivityFilter struct {
	RunID        string `json:"run_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
	BeforeID     string `json:"before_id,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}
type ActivitySummary struct {
	Summary
	ObjectID          string `json:"object_id"`
	Attempts          int    `json:"attempts"`
	UpdatedAt         string `json:"updated_at"`
	ProjectionProblem string `json:"projection_problem,omitempty"`
}
type ActivityPage struct {
	Items        []ActivitySummary `json:"items"`
	NextBeforeID string            `json:"next_before_id,omitempty"`
}

func (s *Service) Activity(ctx context.Context, in ActivityFilter) (ActivityPage, error) {
	out := ActivityPage{Items: []ActivitySummary{}}
	limit, err := executionLimit(in.Limit)
	if err != nil {
		return out, err
	}
	if !validID(in.RunID) || in.AssignmentID != "" && !validID(in.AssignmentID) || in.BeforeID != "" && !validID(in.BeforeID) {
		return out, invalid("Use canonical local UUIDs for activity filters.")
	}
	var formationID string
	if err = s.Store.Reader.QueryRowContext(ctx, "SELECT formation_action_id FROM provider_run_links WHERE run_object_id=?", in.RunID).Scan(&formationID); err != nil {
		return out, invalid("Unknown local provider run.")
	}
	query := `SELECT id,object_revision,state,verb,created_at,object_id,attempts,updated_at,projection_problem FROM provider_actions WHERE (id=? OR origin_action_id=?)`
	args := []any{formationID, formationID}
	if in.AssignmentID != "" {
		query += " AND object_id=? AND verb='teams.assign'"
		args = append(args, in.AssignmentID)
	}
	if in.BeforeID != "" {
		var at string
		if err = s.Store.Reader.QueryRowContext(ctx, "SELECT created_at FROM provider_actions WHERE id=? AND (id=? OR origin_action_id=?)", in.BeforeID, formationID, formationID).Scan(&at); err != nil {
			return out, invalid("Cursor does not belong to this run.")
		}
		query += " AND (created_at,id)<(?,?)"
		args = append(args, at, in.BeforeID)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.Store.Reader.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var row ActivitySummary
		if err = rows.Scan(&row.ID, &row.ObjectRevision, &row.State, &row.Verb, &row.CreatedAt, &row.ObjectID, &row.Attempts, &row.UpdatedAt, &row.ProjectionProblem); err != nil {
			return out, err
		}
		out.Items = append(out.Items, row)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextBeforeID = out.Items[limit-1].ID
	}
	return out, nil
}

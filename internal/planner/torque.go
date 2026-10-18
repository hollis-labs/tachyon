// Package planner contains import boundaries for Tachyon-owned planning data.
// CRUD, queries, relationships and history live in the shared objects service.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

// TorqueRecord is the safe subset of Torque's typed task representation.
// Plans are also task rows (kind=plan) there. Execution settings, environment,
// credentials, open metadata and runtime control fields are deliberately absent.
type TorqueRecord struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	Priority     int      `json:"priority"`
	Tags         []string `json:"tags"`
	UpdatedAt    string   `json:"updated_at"`
	ProjectID    string   `json:"project_id"`
	ParentID     string   `json:"parent_id"`
	DependsOn    []string `json:"depends_on"`
	EpicID       string   `json:"epic_id"`
	SprintID     string   `json:"sprint_id"`
	PhaseID      string   `json:"phase_id"`
	CollectionID string   `json:"collection_id"`
}

// ImportTorque is an explicit import of a selected record, not an inventory
// scan. It never contacts Torque. Callers must fetch a typed representation and
// supply the read's start time. Subsequent observations update provenance only.
// External relationship IDs remain qualified facts until explicitly linked with
// local UUIDs using objects.Service.Patch. Importing an existing master requires
// a Correlation with its expected revision; it never guesses from a title.
func ImportTorque(ctx context.Context, service *objects.Service, instance string, at time.Time, item TorqueRecord, target *objects.Correlation, actor string) (objects.Record, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(instance), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return objects.Record{}, fmt.Errorf("%w: Torque instance must be a credential-free HTTP base URL", objects.ErrInvalid)
	}
	if strings.TrimSpace(item.ID) == "" || at.IsZero() {
		return objects.Record{}, objects.ErrInvalid
	}
	facts := map[string]any{"id": item.ID, "kind": item.Kind,
		"project_ref": item.ProjectID, "parent_ref": item.ParentID, "dependency_refs": item.DependsOn,
		"epic_ref": item.EpicID, "sprint_ref": item.SprintID, "phase_ref": item.PhaseID, "collection_ref": item.CollectionID}
	common := objects.Common{Name: item.Title, Description: item.Description, Tags: item.Tags, Properties: map[string]any{"torque": facts}}
	var definition objects.Definition
	switch item.Kind {
	case "plan":
		definition = objects.Plan{Common: common, Status: item.Status, Priority: item.Priority}
	case "task", "agent", "issue":
		definition = objects.Task{Common: common, Status: item.Status, Priority: item.Priority}
	default:
		return objects.Record{}, fmt.Errorf("%w: unsupported Torque task kind", objects.ErrInvalid)
	}
	body, err := json.Marshal(definition)
	if err != nil {
		return objects.Record{}, err
	}
	return service.ImportOwned(ctx, objects.Observation{
		Key:        objects.SourceKey{SourceScope: objects.SourceScope{Provider: "torque", Instance: u.String(), Collection: "tasks", Kind: definition.ObjectKind()}, ExternalID: item.ID},
		ObservedAt: at, Version: item.UpdatedAt, Definition: body,
	}, target, actor)
}

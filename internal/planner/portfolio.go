package planner

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

var portfolioDatabases = []string{"priorities", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"}

// PortfolioObservation retains the entire upstream item and manual list position
// in properties.torque_planner. External relationships remain qualified source
// facts; portfolio workstreams are NOT implicitly merged with Tether workstreams.
func PortfolioObservation(instance, db string, item json.RawMessage, position int, at time.Time) (objects.Observation, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(item, &row); err != nil {
		return objects.Observation{}, err
	}
	str := func(key string) string { var s string; _ = json.Unmarshal(row[key], &s); return s }
	id, title, sourceStatus := str("id"), str("title"), str("status")
	if id == "" || title == "" {
		return objects.Observation{}, fmt.Errorf("%w: %s item requires id/title", objects.ErrInvalid, db)
	}
	var rev int64
	if err := json.Unmarshal(row["rev"], &rev); err != nil || rev < 1 {
		return objects.Observation{}, fmt.Errorf("%w: %s/%s requires positive rev", objects.ErrInvalid, db, id)
	}
	statuses := map[string]map[string]string{
		"priorities":  {"exploring": "draft", "active": "active", "queued": "draft", "done": "achieved"},
		"workstreams": {"active": "active", "parked": "paused", "unreviewed": "draft", "planned": "draft"},
		"roadmap":     {"landed": "completed", "done": "completed", "planned": "planned", "adopting": "active", "idea": "draft", "in-progress": "active", "dropped": "cancelled"},
		"ideas":       {"new": "draft", "decided": "archived", "discussing": "exploring"},
		"decisions":   {"decided": "accepted", "deferred": "proposed"},
		"risks":       {"closed": "resolved", "accepted": "accepted", "open": "open", "mitigating": "mitigating"},
		"inbox":       {"new": "new", "dismissed": "archived"},
	}
	status, ok := statuses[db][sourceStatus]
	if !ok {
		return objects.Observation{}, fmt.Errorf("%w: unmapped %s status %q for %s", objects.ErrInvalid, db, sourceStatus, id)
	}
	description := str("tldr")
	if description == "" {
		for _, k := range []string{"description", "summary", "body", "goal", "context"} {
			if description = str(k); description != "" {
				break
			}
		}
	}
	var tags []string
	if raw, ok := row["tags"]; ok {
		if err := json.Unmarshal(raw, &tags); err != nil {
			return objects.Observation{}, err
		}
	}
	common := objects.Common{Name: title, Description: description, Tags: tags, Properties: map[string]any{
		"torque_planner": map[string]any{"database": db, "position": position, "item": item},
	}}
	var d objects.Definition
	switch db {
	case "priorities":
		d = objects.StrategicPriority{Common: common, Status: status, Outcome: str("summary")}
	case "workstreams":
		d = objects.Workstream{Common: common, Status: status}
	case "roadmap":
		d = objects.RoadmapItem{Common: common, Status: status, Outcome: str("outcome")}
	case "ideas":
		d = objects.Idea{CaptureFields: objects.CaptureFields{Common: common, Status: status}, Hypothesis: str("summary")}
	case "decisions":
		d = objects.Decision{Common: common, Status: status, Context: str("context"), Outcome: str("decision"), Rationale: str("rationale")}
	case "risks":
		d = objects.Risk{Common: common, Status: status, Mitigation: str("mitigation")}
	case "inbox":
		d = objects.InboxItem{CaptureFields: objects.CaptureFields{Common: common, Status: status}, Origin: str("source")}
	}
	body, err := json.Marshal(d)
	if err != nil {
		return objects.Observation{}, err
	}
	return objects.Observation{Key: objects.SourceKey{SourceScope: objects.SourceScope{Provider: "torque", Instance: instance, Collection: "planner/" + db, Kind: d.ObjectKind()}, ExternalID: id}, ObservedAt: at, Version: strconv.FormatInt(rev, 10), Definition: body}, nil
}

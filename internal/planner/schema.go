package planner

import (
	"embed"
	"encoding/json"

	"github.com/hollis-labs/tachyon/internal/objects"
)

// Field descriptions originated in the owned portfolio schemas. They are
// embedded locally; discovery never fetches a schema from Torque.
//
//go:embed schema/*.json
var schemas embed.FS

func Schema(db string) (Item, error) {
	db = CanonicalDB(db)
	if _, ok := databaseKinds[db]; !ok {
		return nil, objects.ErrNotFound
	}
	b, err := schemas.ReadFile("schema/" + db + ".json")
	if err != nil {
		return nil, err
	}
	var desc Item
	if err = json.Unmarshal(b, &desc); err != nil {
		return nil, err
	}
	fields := object(desc["fields"])
	fields["id"] = Item{"type": "string", "description": "Tachyon UUID; caller retained on create"}
	fields["rev"] = Item{"type": "integer", "minimum": 1, "description": "Current Tachyon revision; expected_revision is required on every write"}
	fields["legacy_id"] = Item{"type": "string", "description": "Read-only original imported ID"}
	statuses := nativeStatuses[db]
	if db == "priorities" {
		statuses = themeStatuses
	}
	fields["status"] = Item{"type": "string", "enum": statuses}
	if db == "risks" {
		fields["impact"] = Item{"type": "integer", "minimum": 0, "maximum": 5}
		fields["likelihood"] = Item{"type": "integer", "minimum": 0, "maximum": 5}
	}
	for _, key := range typedFields(db) {
		if _, ok := fields[key]; !ok {
			fields[key] = Item{"type": "string"}
		}
	}
	if db == "priorities" {
		fields["success_criteria"] = Item{"type": "array", "items": "string"}
	}
	if db == "decisions" || db == "risks" {
		fields["target"] = Item{"type": "object"}
	}
	if db != "workstreams" && db != "decisions" {
		fields["priority"] = Item{"type": "integer", "minimum": 0, "maximum": 5}
	}
	enums := Item{}
	for k, v := range fields {
		if values, ok := object(v)["enum"]; ok {
			enums[k] = values
		}
	}
	return Item{"db": PublicDB(db), "id": "tachyon/planner/" + PublicDB(db) + "@1", "description": desc["description"], "required": []string{"id", "title", "status"}, "fields": fields, "enums": enums, "link_kinds": []string{"torque", "tesseract", "tether", "pr", "adr", "url", "file", "commit", "workstream"}}, nil
}

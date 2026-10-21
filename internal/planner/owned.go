package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/objects"
)

type Item map[string]any
type Owned struct{ Objects *objects.Service }

type PortfolioRef struct {
	DB string `json:"db"`
	ID string `json:"id"`
}
type RelationRequest struct {
	From             PortfolioRef `json:"from"`
	To               PortfolioRef `json:"to"`
	ExpectedRevision int64        `json:"expected_revision"`
	Field            string       `json:"field,omitempty"`
	Add              bool         `json:"add"`
}

func (s *Owned) ResolveReference(ctx context.Context, id string) (Item, error) {
	var found Item
	for _, db := range portfolioDatabases {
		row, err := s.Get(ctx, db, id)
		if errors.Is(err, objects.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if found != nil {
			return nil, objects.ErrConflict
		}
		found = Item{"db": db, "id": row["id"], "item": row}
	}
	if found == nil {
		return nil, objects.ErrNotFound
	}
	return found, nil
}
func (s *Owned) Relate(ctx context.Context, q RelationRequest, actor string) (Item, error) {
	q.From.DB, q.To.DB = CanonicalDB(q.From.DB), CanonicalDB(q.To.DB)
	if q.Field == "" {
		q.Field = "related_ids"
	}
	allowed := map[string]bool{"related_ids": true, "depends_on": true, "priority_ids": true, "workstream_ids": true, "roadmap_ids": true, "idea_ids": true, "decision_ids": true, "risk_ids": true, "inbox_ids": true}
	if !allowed[q.Field] {
		return nil, objects.ErrInvalid
	}
	target, err := s.Resolve(ctx, q.To.DB, q.To.ID)
	if err != nil {
		return nil, err
	}
	return s.change(ctx, q.From.DB, q.From.ID, q.ExpectedRevision, actor, func(row Item) error {
		values := []any{}
		found := false
		for _, v := range array(row[q.Field]) {
			same := v == target.ID || v == q.To.ID
			if target.Source != nil {
				same = same || v == target.Source.Key.ExternalID
			}
			if same {
				if q.Add && !found {
					values = append(values, target.ID)
				}
				found = true
			} else {
				values = append(values, v)
			}
		}
		if q.Add && !found {
			values = append(values, target.ID)
		}
		row[q.Field] = values
		return nil
	})
}

var databaseKinds = map[string]objects.Kind{"priorities": objects.StrategicPriorityKind, "workstreams": objects.WorkstreamKind, "roadmap": objects.RoadmapItemKind, "ideas": objects.IdeaKind, "decisions": objects.DecisionKind, "risks": objects.RiskKind, "inbox": objects.InboxItemKind}

// DatabaseKind maps the public portfolio database name to its immutable object kind.
// CanonicalDB maps a public database name to its storage key. "themes" is the
// public name of the database stored as "priorities": that key, the
// strategic_priority object kind (a SQL CHECK constraint) and the legacy
// source collection "planner/priorities" are persisted identifiers and stay.
func CanonicalDB(db string) string {
	if db == "themes" {
		return "priorities"
	}
	return db
}

// PublicDB is the name callers see for a storage key.
func PublicDB(db string) string {
	if db == "priorities" {
		return "themes"
	}
	return db
}

func DatabaseKind(db string) (objects.Kind, bool) {
	kind, ok := databaseKinds[CanonicalDB(db)]
	return kind, ok
}

// approvalFields are stamped by the service when a theme is approved. A plain
// create or patch may not write them, so an agent cannot forge an approval.
func approvalFields(db string) []string {
	if db == "priorities" {
		return []string{"approved_by", "approved_at"}
	}
	return nil
}

// themeStatuses is the lifecycle callers should use. Flight-board words (in
// flight, cleared, queued, parked, landed) are derived for display and are
// never stored.
var themeStatuses = []string{"proposed", "ready", "approved", "paused", "done", "archived"}

var nativeStatuses = map[string][]string{
	// Themes. The last three are the earlier priority statuses; existing records
	// keep them until Chrispian migrates and re-approves, and the schema
	// advertises only themeStatuses.
	"priorities":  {"proposed", "ready", "approved", "paused", "done", "archived", "draft", "active", "achieved"},
	"workstreams": {"draft", "active", "paused", "closed", "archived"},
	"roadmap":     {"draft", "planned", "active", "completed", "deferred", "cancelled", "archived"},
	"ideas":       {"draft", "exploring", "accepted", "rejected", "archived"},
	"decisions":   {"proposed", "accepted", "rejected", "superseded", "archived"},
	"risks":       {"open", "mitigating", "accepted", "resolved", "archived"},
	"inbox":       {"new", "triaged", "archived"},
}

func decodeMap(raw []byte) (Item, error) {
	var m Item
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, objects.ErrInvalid
	}
	return m, nil
}
func object(v any) Item {
	switch m := v.(type) {
	case map[string]any:
		return Item(m)
	case Item:
		return m
	}
	return Item{}
}
func textField(m Item, k string) string { s, _ := m[k].(string); return s }
func array(v any) []any                 { a, _ := v.([]any); return a }
func number(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
func encode(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func dbFor(k objects.Kind) string {
	for db, kind := range databaseKinds {
		if k == kind {
			return db
		}
	}
	return ""
}

// View projects authored fields first and retained import fields second. Only
// active Tachyon-owned records participate; source inventory is a separate view.
func View(r objects.Record) Item {
	d, _ := decodeMap(r.Definition)
	props := object(d["properties"])
	row := object(props["portfolio"])
	authored := false
	if _, ok := props["portfolio"]; ok {
		authored = true
	}
	if _, ok := props["portfolio"]; !ok {
		row = object(object(props["torque_planner"])["item"])
	}
	row["id"], row["rev"], row["title"], row["status"] = r.ID, r.Revision, r.Name, d["status"]
	if textField(row, "created") == "" {
		created := textField(object(object(props["torque_planner"])["item"]), "created")
		if r.Source != nil && strings.HasPrefix(r.Source.Key.Collection, "planner/") && created != "" {
			row["created"] = created
		} else {
			row["created"] = r.CreatedAt.Format(time.RFC3339Nano)
		}
	}
	if authored || r.Source == nil || !bytes.Equal(canonicalJSON(r.Local), canonicalJSON(r.Source.Definition)) || textField(row, "updated") == "" {
		row["updated"] = r.UpdatedAt.Format(time.RFC3339Nano)
	}
	if r.Source != nil && strings.HasPrefix(r.Source.Key.Collection, "planner/") {
		row["legacy_id"] = r.Source.Key.ExternalID
	}
	row["tldr"] = d["description"]
	if tags, ok := d["tags"]; ok {
		row["tags"] = tags
	} else {
		delete(row, "tags")
	}
	for _, key := range typedFields(dbFor(r.Kind)) {
		if value, ok := d[key]; ok {
			if !authored && key == "priority" && number(value) == 0 && number(row[key]) > 0 {
				continue
			}
			if key == "impact" {
				if v, ok := row[key].(string); ok {
					row["impact_description"] = v
				}
			}
			row[key] = value
		} else {
			delete(row, key)
		}
	}
	if r.Kind == objects.DecisionKind {
		row["decision"] = d["outcome"]
	}
	if _, ok := row["order"]; !ok {
		if pos, ok := object(props["torque_planner"])["position"]; ok {
			row["order"] = number(pos) * 10
		}
	}
	return row
}

func typedFields(db string) []string {
	base := []string{"project_id"}
	if db != "workstreams" {
		base = append(base, "workstream_id")
	}
	if db != "workstreams" && db != "decisions" {
		base = append(base, "priority")
	}
	switch db {
	case "workstreams":
		base = append(base, "workflow_id")
	case "roadmap":
		base = append(base, "outcome", "strategic_priority_id", "plan_id", "epic_id", "starts_at", "ends_at")
	case "priorities":
		base = append(base, "outcome", "success_criteria", "starts_at", "ends_at")
	case "ideas":
		base = append(base, "hypothesis", "expected_outcome")
	case "decisions":
		base = append(base, "context", "outcome", "rationale", "decided_at", "target")
	case "risks":
		base = append(base, "likelihood", "impact", "mitigation", "contingency", "due_at", "target")
	case "inbox":
		base = append(base, "origin", "captured_at")
	}
	return base
}

func (s *Owned) records(ctx context.Context, db string) ([]objects.Record, error) {
	db = CanonicalDB(db)
	kind, ok := databaseKinds[db]
	if !ok {
		return nil, objects.ErrNotFound
	}
	out := []objects.Record{}
	after := ""
	for {
		rows, err := s.Objects.List(ctx, objects.Filter{Kind: kind, Limit: 500, AfterID: after})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Ownership == "tachyon" {
				out = append(out, r)
			}
		}
		if len(rows) < 500 {
			break
		}
		after = rows[len(rows)-1].ID
	}
	return out, nil
}

func (s *Owned) Resolve(ctx context.Context, db, id string) (objects.Record, error) {
	db = CanonicalDB(db)
	kind, ok := databaseKinds[db]
	if !ok {
		return objects.Record{}, objects.ErrNotFound
	}
	if _, err := uuid.Parse(id); err == nil {
		r, err := s.Objects.Get(ctx, id)
		if err != nil {
			return r, err
		}
		if r.Kind != kind || r.Ownership != "tachyon" || r.Deleted() {
			return objects.Record{}, objects.ErrNotFound
		}
		return r, nil
	}
	// Legacy links are accepted only when exactly one qualified source matches.
	rows, err := s.records(ctx, db)
	if err != nil {
		return objects.Record{}, err
	}
	var found objects.Record
	for _, r := range rows {
		if r.Source != nil && r.Source.Key.Provider == "torque" && r.Source.Key.Collection == "planner/"+db && r.Source.Key.ExternalID == id {
			if found.ID != "" {
				return objects.Record{}, objects.ErrConflict
			}
			found = r
		}
	}
	if found.ID == "" {
		return found, objects.ErrNotFound
	}
	return found, nil
}
func (s *Owned) decorate(ctx context.Context, r objects.Record) Item {
	row := View(r)
	if r.Kind == objects.InboxItemKind {
		if receipt, err := s.Objects.GetPromotion(ctx, r.ID); err == nil {
			if target, err := s.Objects.Get(ctx, receipt.TargetID); err == nil {
				row["promoted_to"] = Item{"id": target.ID, "db": dbFor(target.Kind)}
			}
		}
	}
	return row
}
func (s *Owned) Get(ctx context.Context, db, id string) (Item, error) {
	r, err := s.Resolve(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return s.decorate(ctx, r), nil
}
func (s *Owned) List(ctx context.Context, db string, filters map[string]string, sortBy string) ([]Item, error) {
	rows, err := s.records(ctx, db)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, r := range rows {
		v := s.decorate(ctx, r)
		match := true
		for k, want := range filters {
			hit := fmt.Sprint(v[k]) == want
			for _, x := range array(v[k]) {
				if fmt.Sprint(x) == want {
					hit = true
				}
			}
			if !hit {
				match = false
				break
			}
		}
		if match {
			out = append(out, v)
		}
	}
	desc := strings.HasPrefix(sortBy, "-")
	key := strings.TrimPrefix(sortBy, "-")
	if key == "" {
		key = "order"
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i][key], out[j][key]
		if a == nil && b != nil {
			return false
		}
		if b == nil && a != nil {
			return true
		}
		var less bool
		if key == "order" || key == "priority" || key == "rank" {
			if number(a) == number(b) {
				return textField(out[i], "id") < textField(out[j], "id")
			}
			less = number(a) < number(b)
		} else {
			if fmt.Sprint(a) == fmt.Sprint(b) {
				return textField(out[i], "id") < textField(out[j], "id")
			}
			less = fmt.Sprint(a) < fmt.Sprint(b)
		}
		if desc {
			return !less
		}
		return less
	})
	return out, nil
}

func definition(db string, row Item, base json.RawMessage) (json.RawMessage, error) {
	if strings.TrimSpace(textField(row, "title")) == "" {
		return nil, fmt.Errorf("%w: title required", objects.ErrInvalid)
	}
	status := textField(row, "status")
	valid := false
	for _, v := range nativeStatuses[db] {
		if status == v {
			valid = true
		}
	}
	if !valid {
		return nil, fmt.Errorf("%w: native status required for %s", objects.ErrInvalid, db)
	}
	if err := validateDetails(row); err != nil {
		return nil, err
	}
	d := Item{}
	if len(base) > 0 {
		var err error
		d, err = decodeMap(base)
		if err != nil {
			return nil, err
		}
	}
	d["name"], d["status"] = row["title"], status
	for from, to := range map[string]string{"tldr": "description", "tags": "tags"} {
		if v, ok := row[from]; ok && v != nil {
			d[to] = v
		} else {
			delete(d, to)
		}
	}
	for _, k := range typedFields(db) {
		if v, ok := row[k]; ok && v != nil {
			d[k] = v
		} else {
			delete(d, k)
		}
	}
	props := object(d["properties"])
	stored := Item{}
	for k, v := range row {
		switch k {
		case "id", "rev", "legacy_id", "created", "updated", "promoted_to":
			continue
		}
		stored[k] = v
	}
	props["portfolio"] = stored
	d["properties"] = props
	return encode(d), nil
}
func validateDetails(row Item) error {
	for _, field := range []string{"options", "links", "comments"} {
		if v, ok := row[field]; ok && v != nil {
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("%w: %s must be array", objects.ErrInvalid, field)
			}
		}
	}
	seen := map[string]bool{}
	for _, v := range array(row["options"]) {
		o := object(v)
		id := textField(o, "id")
		if id == "" || textField(o, "label") == "" || seen[id] {
			return fmt.Errorf("%w: unique option id and label required", objects.ErrInvalid)
		}
		seen[id] = true
	}
	for _, v := range array(row["links"]) {
		o := object(v)
		if textField(o, "kind") == "" || textField(o, "ref") == "" {
			return fmt.Errorf("%w: link kind/ref required", objects.ErrInvalid)
		}
	}
	return nil
}

type rawDefinition struct {
	kind objects.Kind
	body json.RawMessage
}

func (d rawDefinition) ObjectKind() objects.Kind     { return d.kind }
func (d rawDefinition) MarshalJSON() ([]byte, error) { return d.body, nil }

func (s *Owned) Create(ctx context.Context, db, id string, fields Item, actor string) (Item, error) {
	db = CanonicalDB(db)
	kind, ok := databaseKinds[db]
	if !ok {
		return nil, objects.ErrNotFound
	}
	if id == "" {
		return nil, fmt.Errorf("%w: caller-retained UUID required", objects.ErrInvalid)
	}
	for _, k := range append([]string{"rev", "legacy_id", "created", "updated", "comments", "author"}, approvalFields(db)...) {
		if _, ok := fields[k]; ok {
			return nil, fmt.Errorf("%w: %s is server managed", objects.ErrInvalid, k)
		}
	}
	if textField(fields, "status") == "" {
		fields["status"] = nativeStatuses[db][0]
	}
	fields["author"] = actor
	if db == "priorities" {
		if err := guardTheme(nil, fields, actor); err != nil {
			return nil, err
		}
	}
	b, err := definition(db, fields, nil)
	if err != nil {
		return nil, err
	}
	r, err := s.Objects.CreateWithID(ctx, id, rawDefinition{kind, b}, nil, actor)
	if err != nil {
		return nil, err
	}
	return View(r), nil
}
func (s *Owned) Patch(ctx context.Context, db, id string, rev int64, patch Item, actor string) (Item, error) {
	db = CanonicalDB(db)
	for _, k := range append([]string{"id", "rev", "legacy_id", "created", "updated", "author", "comments", "promoted_to"}, approvalFields(db)...) {
		if _, ok := patch[k]; ok {
			return nil, fmt.Errorf("%w: %s is server managed", objects.ErrInvalid, k)
		}
	}
	return s.change(ctx, db, id, rev, actor, func(row Item) error {
		for k, v := range patch {
			if v == nil {
				delete(row, k)
			} else {
				row[k] = v
			}
		}
		if db == "decisions" {
			if v, ok := patch["decision"]; ok {
				if _, explicit := patch["outcome"]; !explicit {
					if v == nil {
						delete(row, "outcome")
					} else {
						row["outcome"] = v
					}
				}
			}
		}
		return nil
	})
}
func (s *Owned) change(ctx context.Context, db, id string, rev int64, actor string, fn func(Item) error) (Item, error) {
	db = CanonicalDB(db)
	r, err := s.Resolve(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if rev < 1 || r.Revision != rev {
		return nil, objects.ErrConflict
	}
	row := View(r)
	before := Item{}
	for k, v := range row {
		before[k] = v
	}
	if err = fn(row); err != nil {
		return nil, err
	}
	if db == "priorities" {
		if err = guardTheme(before, row, actor); err != nil {
			return nil, err
		}
	}
	b, err := definition(db, row, r.Definition)
	if err != nil {
		return nil, err
	}
	rows, err := s.Objects.ReplaceDefinitions(ctx, []objects.Replacement{{ID: r.ID, ExpectedRevision: rev, Definition: b}}, actor)
	if err != nil {
		return nil, err
	}
	return s.decorate(ctx, rows[0]), nil
}
func appendComment(row Item, text, kind, actor string) error {
	if strings.TrimSpace(text) == "" {
		return objects.ErrInvalid
	}
	if kind == "" {
		kind = "comment"
	}
	if kind != "comment" && kind != "decision" {
		return objects.ErrInvalid
	}
	row["comments"] = append(array(row["comments"]), Item{"id": uuid.NewString(), "author": actor, "text": text, "kind": kind, "created": time.Now().UTC().Format(time.RFC3339Nano)})
	return nil
}
func (s *Owned) Action(ctx context.Context, db, id string, rev int64, action string, args Item, actor string) (Item, error) {
	db = CanonicalDB(db)
	if IsThemeAction(action) {
		if db != "priorities" {
			return nil, objects.ErrInvalid
		}
		if action == "resequence" {
			rows, err := s.Resequence(ctx, id, rev, int(number(args["position"])), actor)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				if r["id"] == id || r["legacy_id"] == id {
					return r, nil
				}
			}
			return s.Get(ctx, db, id)
		}
		return s.change(ctx, db, id, rev, actor, func(row Item) error { return s.themeAction(ctx, row, action, args, actor) })
	}
	return s.change(ctx, db, id, rev, actor, func(row Item) error {
		switch action {
		case "comments":
			return appendComment(row, textField(args, "text"), textField(args, "kind"), actor)
		case "link-add":
			l := object(args["link"])
			if textField(l, "kind") == "" || textField(l, "ref") == "" {
				return objects.ErrInvalid
			}
			for _, v := range array(row["links"]) {
				o := object(v)
				if o["kind"] == l["kind"] && o["ref"] == l["ref"] {
					return nil
				}
			}
			row["links"] = append(array(row["links"]), l)
		case "link-remove":
			if textField(args, "kind") == "" || textField(args, "ref") == "" {
				return objects.ErrInvalid
			}
			links := []any{}
			for _, v := range array(row["links"]) {
				o := object(v)
				if o["kind"] != args["kind"] || o["ref"] != args["ref"] {
					links = append(links, v)
				}
			}
			row["links"] = links
		case "decide":
			if db != "decisions" {
				return objects.ErrInvalid
			}
			found := false
			for _, v := range array(row["options"]) {
				o := object(v)
				if textField(o, "id") == textField(args, "option") {
					row["selected_option"], row["outcome"], found = o["id"], o["label"], true
				}
			}
			if !found {
				return objects.ErrInvalid
			}
			row["status"], row["decided_at"], row["decided_by"] = "accepted", time.Now().UTC().Format(time.RFC3339Nano), actor
			delete(row, "deferred")
			if c := textField(args, "comment"); c != "" {
				return appendComment(row, c, "decision", actor)
			}
		case "defer", "reopen":
			if db != "decisions" {
				return objects.ErrInvalid
			}
			row["status"] = "proposed"
			delete(row, "selected_option")
			delete(row, "decided_at")
			delete(row, "decided_by")
			delete(row, "outcome")
			row["deferred"] = action == "defer"
			row["defer_note"] = textField(args, "note")
		case "dismiss":
			if db != "inbox" {
				return objects.ErrInvalid
			}
			row["status"] = "archived"
			if note := textField(args, "note"); note != "" {
				return appendComment(row, note, "comment", actor)
			}
		default:
			return objects.ErrNotFound
		}
		return nil
	})
}

type OrderChange struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (s *Owned) Reorder(ctx context.Context, db string, items []OrderChange, actor string) ([]Item, error) {
	db = CanonicalDB(db)
	if db == "priorities" && !isOperator(actor) {
		return nil, fmt.Errorf("%w: sequencing themes is operator-only", ErrOperatorRequired)
	}
	changes := []objects.Replacement{}
	for i, item := range items {
		r, err := s.Resolve(ctx, db, item.ID)
		if err != nil {
			return nil, err
		}
		row := View(r)
		row["order"] = (i + 1) * 10
		b, err := definition(db, row, r.Definition)
		if err != nil {
			return nil, err
		}
		changes = append(changes, objects.Replacement{ID: r.ID, ExpectedRevision: item.ExpectedRevision, Definition: b})
	}
	rows, err := s.Objects.ReplaceDefinitions(ctx, changes, actor)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, r := range rows {
		out = append(out, View(r))
	}
	return out, nil
}
func (s *Owned) Promote(ctx context.Context, id string, rev int64, toDB, targetID string, fields Item, actor string) (Item, error) {
	toDB = CanonicalDB(toDB)
	if toDB == "inbox" {
		return nil, objects.ErrInvalid
	}
	kind, ok := databaseKinds[toDB]
	if !ok {
		return nil, objects.ErrInvalid
	}
	source, err := s.Resolve(ctx, "inbox", id)
	if err != nil {
		return nil, err
	}
	// Rebuild a replay from the exact source revision; the object service checks
	// the entire canonical request, including fields, against its immutable receipt.
	seed := source
	if receipt, e := s.Objects.GetPromotion(ctx, source.ID); e == nil {
		if receipt.TargetID != targetID || receipt.SourceRevision != rev {
			return nil, objects.ErrConflict
		}
		history, e := s.Objects.History(ctx, source.ID, rev-1, 1)
		if e != nil {
			return nil, e
		}
		if len(history) != 1 || history[0].Record.Revision != rev {
			return nil, objects.ErrConflict
		}
		seed = history[0].Record
	} else if !errors.Is(e, objects.ErrNotFound) {
		return nil, e
	}
	row := Item{"title": seed.Name, "status": nativeStatuses[toDB][0]}
	for k, v := range fields {
		row[k] = v
	}
	b, err := definition(toDB, row, nil)
	if err != nil {
		return nil, err
	}
	result, err := s.Objects.Promote(ctx, objects.PromotionRequest{SourceID: source.ID, ExpectedRevision: rev, TargetID: targetID, TargetKind: kind, Overrides: b, TriageSource: true}, actor)
	if err != nil {
		return nil, err
	}
	return Item{"inbox": s.decorate(ctx, result.Source), "item": View(result.Target)}, nil
}

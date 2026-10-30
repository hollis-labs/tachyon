package planner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

// ErrCycle is returned when a needs relation would close a loop; HTTP answers 422 "cycle".
var ErrCycle = errors.New("that link would make a cycle")

// ErrRule is returned when a relation breaks a kind rule (self link, delivers only theme->roadmap,
// mitigates only theme->risk, needs only within one database); HTTP answers 422 "invalid".
var ErrRule = errors.New("relation not allowed")

// Relation is one typed edge stored on its source item (properties.portfolio.relations[]).
// to.id is the target's canonical object id; direction is stored on the source and the inbound
// side is read from planner_relation_index.
type Relation struct {
	Kind      string
	To        PortfolioRef
	CreatedAt string
	By        string
}

func (r Relation) item() Item {
	return Item{"kind": r.Kind, "to": Item{"db": PublicDB(r.To.DB), "id": r.To.ID}, "created_at": r.CreatedAt, "by": r.By}
}

func relationsOf(row Item) []Relation {
	out := []Relation{}
	for _, v := range array(row["relations"]) {
		o := object(v)
		to := object(o["to"])
		out = append(out, Relation{Kind: textField(o, "kind"), To: PortfolioRef{DB: CanonicalDB(textField(to, "db")), ID: textField(to, "id")}, CreatedAt: textField(o, "created_at"), By: textField(o, "by")})
	}
	return out
}

func validateRelations(row Item) error {
	rels := array(row["relations"])
	if len(rels) > plannerapi.MaxRelationsItem {
		return fmt.Errorf("%w: at most %d relations per item", objects.ErrInvalid, plannerapi.MaxRelationsItem)
	}
	seen := map[string]bool{}
	for _, v := range rels {
		o := object(v)
		to := object(o["to"])
		key := textField(o, "kind") + "|" + textField(to, "id")
		if !slices.Contains(plannerapi.RelationKinds, textField(o, "kind")) || textField(to, "id") == "" || seen[key] {
			return fmt.Errorf("%w: relations need a known kind, a target and no duplicates", objects.ErrInvalid)
		}
		seen[key] = true
	}
	return nil
}

// validatePortfolioFields checks the typed fields PL1 adds. Themes carry area, horizon and
// done_summary; every database carries pinned/pinned_at. pinned_at is set by Pin, never by a client.
func validatePortfolioFields(db string, row Item) error {
	if v, ok := row["pinned"]; ok && v != nil {
		if _, isBool := v.(bool); !isBool {
			return fmt.Errorf("%w: pinned must be a boolean", objects.ErrInvalid)
		}
	}
	if db == "priorities" {
		if v := textField(row, "area"); v != "" && !slices.Contains(plannerapi.Areas, v) {
			return fmt.Errorf("%w: area must be one of %s", objects.ErrInvalid, strings.Join(plannerapi.Areas, ", "))
		}
		if v := textField(row, "horizon"); v != "" && !slices.Contains(plannerapi.Horizons, v) {
			return fmt.Errorf("%w: horizon must be one of %s", objects.ErrInvalid, strings.Join(plannerapi.Horizons, ", "))
		}
		if len([]rune(textField(row, "done_summary"))) > plannerapi.MaxSummaryChars {
			return fmt.Errorf("%w: done_summary is at most %d characters", objects.ErrInvalid, plannerapi.MaxSummaryChars)
		}
	}
	return validateRelations(row)
}

// Pin sets or clears the pin in one revision-checked write. pinned_at is stamped here.
func (s *Owned) Pin(ctx context.Context, db, id string, rev int64, pinned bool, actor string) (Item, error) {
	return s.change(ctx, db, id, rev, actor, func(row Item) error {
		if pinned {
			if row["pinned"] != true {
				row["pinned"], row["pinned_at"] = true, time.Now().UTC().Format(time.RFC3339Nano)
			}
		} else {
			delete(row, "pinned")
			delete(row, "pinned_at")
		}
		return nil
	})
}

// AddRelation stores a typed relation on the source item. Adding an existing (kind, to) is a no-op.
func (s *Owned) AddRelation(ctx context.Context, from PortfolioRef, kind string, to PortfolioRef, rev int64, actor string) (Item, error) {
	return s.editRelation(ctx, from, kind, to, rev, actor, true)
}

// RemoveRelation deletes a typed relation from the source item. Removing an absent one is a no-op.
func (s *Owned) RemoveRelation(ctx context.Context, from PortfolioRef, kind string, to PortfolioRef, rev int64, actor string) (Item, error) {
	return s.editRelation(ctx, from, kind, to, rev, actor, false)
}

func (s *Owned) editRelation(ctx context.Context, from PortfolioRef, kind string, to PortfolioRef, rev int64, actor string, add bool) (Item, error) {
	from.DB, to.DB = CanonicalDB(from.DB), CanonicalDB(to.DB)
	if !slices.Contains(plannerapi.RelationKinds, kind) {
		return nil, fmt.Errorf("%w: unknown relation kind", objects.ErrInvalid)
	}
	target, err := s.Resolve(ctx, to.DB, to.ID)
	if err != nil {
		return nil, err
	}
	source, err := s.Resolve(ctx, from.DB, from.ID)
	if err != nil {
		return nil, err
	}
	to.ID = target.ID
	if add {
		if source.ID == target.ID {
			return nil, fmt.Errorf("%w: an item cannot link to itself", ErrRule)
		}
		fromPub, toPub := PublicDB(from.DB), PublicDB(to.DB)
		switch {
		case kind == plannerapi.RelDelivers && (fromPub != "themes" || toPub != "roadmap"):
			return nil, fmt.Errorf("%w: delivers links a theme to a roadmap item", ErrRule)
		case kind == plannerapi.RelMitigates && (fromPub != "themes" || toPub != "risks"):
			return nil, fmt.Errorf("%w: mitigates links a theme to a risk", ErrRule)
		case kind == plannerapi.RelNeeds && from.DB != to.DB:
			return nil, fmt.Errorf("%w: needs links two items of one database", ErrRule)
		}
		if kind == plannerapi.RelNeeds {
			if reaches, err := s.needsReaches(ctx, target.ID, source.ID); err != nil {
				return nil, err
			} else if reaches {
				return nil, ErrCycle
			}
		}
	}
	return s.change(ctx, from.DB, from.ID, rev, actor, func(row Item) error {
		out := []any{}
		found := false
		for _, r := range relationsOf(row) {
			if r.Kind == kind && r.To.ID == to.ID {
				found = true
				if add {
					out = append(out, r.item())
				}
				continue
			}
			out = append(out, r.item())
		}
		if add && !found {
			out = append(out, Relation{Kind: kind, To: to, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), By: actor}.item())
		}
		if len(out) == 0 {
			delete(row, "relations")
		} else {
			row["relations"] = out
		}
		return nil
	})
}

// needsReaches reports whether following needs edges from start arrives at goal, so adding
// goal -> start would close a loop. Edges come from the derived index, which persist keeps in step.
func (s *Owned) needsReaches(ctx context.Context, start, goal string) (bool, error) {
	seen := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == goal {
			return true, nil
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		rows, err := s.Objects.RelationsFrom(ctx, id)
		if err != nil {
			return false, err
		}
		for _, r := range rows {
			if r.Kind == plannerapi.RelNeeds {
				queue = append(queue, r.ToID)
			}
		}
	}
	return false, nil
}

// legacyArrays map the untyped id arrays onto relation kinds for reads. Nothing is migrated: the
// arrays stay as written. depends_on reads as needs; roadmap_ids on a theme as delivers; the rest
// as relates.
var legacyArrays = []struct{ field, db string }{
	{"depends_on", ""}, {"related_ids", ""}, {"priority_ids", "priorities"}, {"workstream_ids", "workstreams"},
	{"roadmap_ids", "roadmap"}, {"idea_ids", "ideas"}, {"decision_ids", "decisions"}, {"risk_ids", "risks"}, {"inbox_ids", "inbox"},
}

func legacyKind(field, fromDB string) string {
	switch {
	case field == "depends_on":
		return plannerapi.RelNeeds
	case field == "roadmap_ids" && fromDB == "priorities":
		return plannerapi.RelDelivers
	}
	return plannerapi.RelRelates
}

var prRef = regexp.MustCompile(`^(?:https?://github\.com/)?([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(?:#|/pull/)([0-9]+)$`)

func prCite(ref string) Item {
	m := prRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return nil
	}
	n, _ := strconv.Atoi(m[3])
	return Item{"host": "github.com", "owner": m[1], "repo": m[2], "number": n}
}

func (s *Owned) peer(ctx context.Context, db, id string) (Item, bool) {
	row, err := s.Get(ctx, db, id)
	if err != nil {
		return nil, false
	}
	return row, true
}

// Links merges typed relations, the legacy id arrays and external links into one view.
func (s *Owned) Links(ctx context.Context, db, id string) (Item, error) {
	db = CanonicalDB(db)
	rec, err := s.Resolve(ctx, db, id)
	if err != nil {
		return nil, err
	}
	row := View(rec)
	outbound, inbound, external, mapped := []Item{}, []Item{}, []Item{}, []Item{}
	seen := map[string]bool{}
	add := func(kind, toDB, toID string) {
		if seen[kind+"|"+toID] {
			return
		}
		seen[kind+"|"+toID] = true
		if peer, ok := s.peer(ctx, toDB, toID); ok {
			outbound = append(outbound, Item{"kind": kind, "to": Item{"db": PublicDB(toDB), "id": peer["id"]}, "title": peer["title"], "status": peer["status"]})
		}
	}
	for _, r := range relationsOf(row) {
		add(r.Kind, r.To.DB, r.To.ID)
	}
	for _, la := range legacyArrays {
		values := array(row[la.field])
		if len(values) == 0 {
			continue
		}
		kind := legacyKind(la.field, db)
		for _, v := range values {
			ref := fmt.Sprint(v)
			if la.db != "" {
				add(kind, la.db, ref)
			} else if found, err := s.ResolveReference(ctx, ref); err == nil {
				add(kind, textField(found, "db"), ref)
			}
		}
		if kind != plannerapi.RelRelates {
			mapped = append(mapped, Item{"array": la.field, "as": kind})
		}
	}
	in, err := s.Objects.RelationsTo(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range in {
		if peer, ok := s.peer(ctx, r.FromDB, r.FromID); ok {
			inbound = append(inbound, Item{"kind": r.Kind, "from": Item{"db": PublicDB(CanonicalDB(r.FromDB)), "id": r.FromID}, "title": peer["title"], "status": peer["status"]})
		}
	}
	for _, v := range array(row["links"]) {
		o := object(v)
		label := textField(o, "label")
		if label == "" {
			label = textField(o, "ref")
		}
		l := Item{"kind": textField(o, "kind"), "ref": textField(o, "ref"), "label": label}
		if l["kind"] == "pr" {
			if pr := prCite(textField(o, "ref")); pr != nil {
				l["pr"] = pr
			}
		}
		external = append(external, l)
	}
	return Item{"outbound": outbound, "inbound": inbound, "external": external, "legacy_mapped": mapped}, nil
}

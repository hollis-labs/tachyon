package objects

import (
	"context"
	"database/sql"
	"encoding/json"
)

// portfolioDB names the planner database of a portfolio kind (themes are stored as priorities
// but are public "themes"); the empty string marks a kind that has no relations.
func portfolioDB(k Kind) string {
	switch k {
	case StrategicPriorityKind:
		return "themes"
	case WorkstreamKind:
		return "workstreams"
	case RoadmapItemKind:
		return "roadmap"
	case IdeaKind:
		return "ideas"
	case DecisionKind:
		return "decisions"
	case RiskKind:
		return "risks"
	case InboxItemKind:
		return "inbox"
	}
	return ""
}

// projectRelations rewrites the item's rows of planner_relation_index from the relations[] array in
// its portfolio blob, in the persist transaction. A tombstoned or non-portfolio record has no rows.
func projectRelations(ctx context.Context, tx *sql.Tx, r Record) error {
	from := portfolioDB(r.Kind)
	if from == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM planner_relation_index WHERE from_id=?`, r.ID); err != nil {
		return err
	}
	if r.Deleted() || r.Ownership != "tachyon" {
		return nil
	}
	var d struct {
		Properties struct {
			Portfolio struct {
				Relations []struct {
					Kind string `json:"kind"`
					To   struct {
						DB string `json:"db"`
						ID string `json:"id"`
					} `json:"to"`
				} `json:"relations"`
			} `json:"portfolio"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(r.Local, &d); err != nil {
		return nil // an unreadable blob has no relations; validation owns rejecting it
	}
	for _, rel := range d.Properties.Portfolio.Relations {
		if rel.Kind == "" || rel.To.ID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO planner_relation_index(from_id,kind,to_id,from_db,to_db) VALUES(?,?,?,?,?)`, r.ID, rel.Kind, rel.To.ID, from, rel.To.DB); err != nil {
			return err
		}
	}
	return nil
}

// RelationRow is one derived index row.
type RelationRow struct{ FromID, Kind, ToID, FromDB, ToDB string }

// RelationsTo lists the index rows pointing at an item (the inbound side of Links).
func (s *Service) RelationsTo(ctx context.Context, id string) ([]RelationRow, error) {
	return s.relationRows(ctx, `SELECT from_id,kind,to_id,from_db,to_db FROM planner_relation_index WHERE to_id=? ORDER BY kind,from_id`, id)
}

// RelationsFrom lists the index rows an item points at.
func (s *Service) RelationsFrom(ctx context.Context, id string) ([]RelationRow, error) {
	return s.relationRows(ctx, `SELECT from_id,kind,to_id,from_db,to_db FROM planner_relation_index WHERE from_id=? ORDER BY kind,to_id`, id)
}

func (s *Service) relationRows(ctx context.Context, q, id string) ([]RelationRow, error) {
	rows, err := s.store.Reader.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RelationRow{}
	for rows.Next() {
		var r RelationRow
		if err := rows.Scan(&r.FromID, &r.Kind, &r.ToID, &r.FromDB, &r.ToDB); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

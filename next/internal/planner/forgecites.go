package planner

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// forgeRow is the part of a forge PR index row the planner reads: the PR's reference and title.
type forgeRow struct {
	PRKey string `json:"pr_key"`
	Ref   struct {
		Repo struct {
			Host  string `json:"host"`
			Owner string `json:"owner"`
			Name  string `json:"name"`
		} `json:"repo"`
		Number int `json:"number"`
	} `json:"ref"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

// forgeRows reads the forge PR index (forge_pr_index, written by the forge slice) in the old
// snapshot order: most recently updated first, then by key. A row that does not decode is skipped,
// as before.
func (h *HTTP) forgeRows(ctx context.Context) ([]forgeRow, error) {
	rows, err := h.Store.Reader.QueryContext(ctx, `SELECT row_json FROM forge_pr_index`)
	if err != nil {
		return nil, err
	}
	var out []forgeRow
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var st struct {
			Row forgeRow `json:"row"`
		}
		if json.Unmarshal([]byte(raw), &st) != nil {
			continue
		}
		out = append(out, st.Row)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b forgeRow) int {
		if c := strings.Compare(b.UpdatedAt, a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.PRKey, b.PRKey)
	})
	return out, nil
}

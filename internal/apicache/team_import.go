package apicache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const ImportedTeamsPath = "/api/plugins/console/v1/catalog/imported-teams"
const importedTeamsKey = "manual-import:tether:team-definitions:v1"

type TeamSnapshot struct {
	Source     string            `json:"source"`
	ImportedAt string            `json:"imported_at"`
	Teams      []json.RawMessage `json:"teams"`
}

// ImportTeams is an operator-only, one-shot copy. No HTTP write surface or
// permanent access to the source database is installed in the running app.
func (c *Cache) ImportTeams(ctx context.Context, source string, body []byte) (int, error) {
	if strings.TrimSpace(source) == "" || len(source) > 200 {
		return 0, errors.New("source label is required (max 200 characters)")
	}
	if int64(len(body)) > c.settings.MaxBytes || len(body) > 8<<20 {
		return 0, errors.New("team snapshot exceeds cache size limit")
	}
	var teams []json.RawMessage
	if err := json.Unmarshal(body, &teams); err != nil || len(teams) == 0 {
		return 0, errors.New("expected a non-empty array of team definitions")
	}
	seen := map[string]bool{}
	for _, raw := range teams {
		var team struct {
			ID      string            `json:"id"`
			Name    string            `json:"name"`
			Version uint64            `json:"version"`
			Slots   []json.RawMessage `json:"slots"`
		}
		if err := json.Unmarshal(raw, &team); err != nil || team.ID == "" || team.Name == "" || team.Version == 0 || len(team.Slots) == 0 {
			return 0, errors.New("invalid saved team definition")
		}
		key := fmt.Sprintf("%s@%d", team.ID, team.Version)
		if seen[key] {
			return 0, fmt.Errorf("duplicate team revision: %s", key)
		}
		seen[key] = true
	}
	now := c.now()
	snapshot := TeamSnapshot{Source: source, ImportedAt: now.UTC().Format(time.RFC3339Nano), Teams: teams}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	if int64(len(encoded)) > c.settings.MaxBytes {
		return 0, errors.New("team snapshot exceeds cache size limit")
	}
	err = c.save(ctx, importedTeamsKey, "catalog-import", "operator-import", entry{body: encoded, contentType: "application/json", updated: now.UnixMilli()})
	return len(teams), err
}

// ImportedTeamsHandler never attempts the denied/unavailable upstream read.
// Imported data is distinct from credential-bound live cache entries.
func (c *Cache) ImportedTeamsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	e, err := c.get(r.Context(), importedTeamsKey)
	if errors.Is(err, sql.ErrNoRows) || err == nil && c.now().Sub(time.UnixMilli(e.updated)) > time.Duration(c.settings.RetentionSeconds)*time.Second {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Tachyon-Source", "catalog")
		w.Header().Set("X-Tachyon-Cache", "cached")
		w.Header().Set("X-Tachyon-Cache-Enabled", "false")
		_, _ = w.Write([]byte(`{"source":"","imported_at":"","teams":[]}`))
		return
	}
	if err != nil {
		failure(w, "catalog", "storage", "Could not read the imported team snapshot.")
		return
	}
	serveEntry(w, "catalog", "imported", e, c.settings.Policies["catalog"], "")
}

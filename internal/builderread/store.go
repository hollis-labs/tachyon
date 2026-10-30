// Package builderread is the Builder's read model (B2): agents, launches, used-by, checks and history served from
// the catalog index (internal/catalogindex). Tether stays the system of record; everything here is derived from
// the masked snapshots the index keeps, so no environment value can reach a response. Design:
// design_tachyon_builder_registry_backend_20261030, section 5.
package builderread

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// file is one indexed catalog file with the masked content of the snapshot that matches its current sha.
type file struct {
	catalogindex.Entity
	Content    string
	ObservedAt time.Time
	SnapSeq    int // 1-based position among the path's snapshots
}

// loadAll reads every entity of a kind with its current masked content. An entity whose snapshot was pruned
// or never written (a presence-only file) has empty Content.
func loadAll(ctx context.Context, db *catalogindex.Store, kind string) ([]file, error) {
	rows, err := db.DB.Reader.QueryContext(ctx, `SELECT e.path,e.kind,e.id,e.layer,e.sha,e.name,e.state,e.provider,e.model,e.fields_json,e.updated_ns,
 COALESCE((SELECT s.content FROM catalog_snapshots s WHERE s.path=e.path AND s.sha=e.sha ORDER BY s.id DESC LIMIT 1),''),
 (SELECT COUNT(*) FROM catalog_snapshots s WHERE s.path=e.path AND s.id<=COALESCE((SELECT MAX(s2.id) FROM catalog_snapshots s2 WHERE s2.path=e.path AND s2.sha=e.sha),0))
 FROM catalog_entities e WHERE e.kind=? ORDER BY e.id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []file
	for rows.Next() {
		var f file
		var fields string
		var ns int64
		if err := rows.Scan(&f.Path, &f.Kind, &f.ID, &f.Layer, &f.SHA, &f.Name, &f.State, &f.Provider, &f.Model, &fields, &ns, &f.Content, &f.SnapSeq); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(fields), &f.Fields)
		f.ObservedAt = time.Unix(0, ns).UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

// snapshots lists a path's masked snapshots newest first, with their ordinal (1 = oldest kept).
type snap struct {
	catalogindex.Snapshot
	Seq int
}

func loadSnaps(ctx context.Context, db *catalogindex.Store, path string, limit int) ([]snap, error) {
	var total int
	if err := db.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_snapshots WHERE path=?`, path).Scan(&total); err != nil {
		return nil, err
	}
	list, err := db.Snapshots(ctx, path, limit)
	if err != nil {
		return nil, err
	}
	out := make([]snap, len(list))
	for i, s := range list {
		out[i] = snap{s, total - i}
	}
	return out, nil
}

package catalogindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Retention: the last 50 snapshots per path and 90 days.
const (
	SnapshotsPerPath  = 50
	SnapshotRetention = 90 * 24 * time.Hour
)

type Store struct {
	DB  *localstore.Store
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// State is the single bookkeeping row.
type State struct {
	ObservedAt   time.Time
	SourceStatus string
	LastError    string
	LastPlanID   string
	FullScanAt   time.Time
}

func (s *Store) State(ctx context.Context) (st State, err error) {
	var obs, full int64
	err = s.DB.Reader.QueryRowContext(ctx, `SELECT observed_ns, source_status, last_error, last_plan_id, full_scan_ns FROM catalog_state WHERE id = 1`).
		Scan(&obs, &st.SourceStatus, &st.LastError, &st.LastPlanID, &full)
	if obs > 0 {
		st.ObservedAt = time.Unix(0, obs).UTC()
	}
	if full > 0 {
		st.FullScanAt = time.Unix(0, full).UTC()
	}
	return st, err
}

func (s *Store) SetState(ctx context.Context, st State) error {
	return s.DB.Writer.Submit(ctx, "catalog_state", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_state SET observed_ns=?, source_status=?, last_error=?, last_plan_id=?, full_scan_ns=? WHERE id=1`,
			nsOf(st.ObservedAt), st.SourceStatus, st.LastError, st.LastPlanID, nsOf(st.FullScanAt))
		return err
	})
}

func nsOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// Entities returns every indexed entity keyed by path.
func (s *Store) Entities(ctx context.Context) (map[string]Entity, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT path, kind, id, layer, sha, fingerprint, name, state, provider, model, fields_json FROM catalog_entities`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Entity{}
	for rows.Next() {
		var e Entity
		var f string
		if err := rows.Scan(&e.Path, &e.Kind, &e.ID, &e.Layer, &e.SHA, &e.Fingerprint, &e.Name, &e.State, &e.Provider, &e.Model, &f); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(f), &e.Fields)
		out[e.Path] = e
	}
	return out, rows.Err()
}

// Snapshot is a masked copy of a file at a sha.
type Snapshot struct {
	Path, SHA, Content, Source, PlanID, Actor string
	At                                        time.Time
}

// Apply writes one pass: upserted entities, the paths that vanished, and the snapshots for changed
// content, in one transaction.
func (s *Store) Apply(ctx context.Context, up []Entity, gone []string, snaps []Snapshot) error {
	return s.DB.Writer.Submit(ctx, "catalog_apply", func(ctx context.Context, tx *sql.Tx) error {
		now := s.now().UnixNano()
		for _, e := range up {
			f, _ := json.Marshal(e.Fields)
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_entities(path,kind,id,layer,sha,fingerprint,name,state,provider,model,fields_json,updated_ns)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET kind=excluded.kind, id=excluded.id, layer=excluded.layer, sha=excluded.sha,
 fingerprint=excluded.fingerprint, name=excluded.name, state=excluded.state, provider=excluded.provider, model=excluded.model, fields_json=excluded.fields_json, updated_ns=excluded.updated_ns`,
				e.Path, e.Kind, e.ID, e.Layer, e.SHA, e.Fingerprint, e.Name, e.State, e.Provider, e.Model, string(f), now); err != nil {
				return err
			}
		}
		for _, p := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_entities WHERE path = ?`, p); err != nil {
				return err
			}
		}
		for _, sn := range snaps {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_snapshots(path,sha,content,at_ns,source,plan_id,actor) VALUES (?,?,?,?,?,?,?)`,
				sn.Path, sn.SHA, sn.Content, sn.At.UnixNano(), sn.Source, sn.PlanID, sn.Actor); err != nil {
				return err
			}
		}
		return nil
	})
}

// Prune applies the retention policy: older than 90 days, or beyond the newest 50 for a path.
func (s *Store) Prune(ctx context.Context) error {
	return s.DB.Writer.Submit(ctx, "catalog_prune", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_snapshots WHERE at_ns < ?`, s.now().Add(-SnapshotRetention).UnixNano()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_snapshots WHERE id IN (
 SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY path ORDER BY id DESC) AS n FROM catalog_snapshots) WHERE n > ?)`, SnapshotsPerPath)
		return err
	})
}

// Snapshots lists a path's snapshots newest first (content included).
func (s *Store) Snapshots(ctx context.Context, p string, limit int) ([]Snapshot, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT path, sha, content, at_ns, source, plan_id, actor FROM catalog_snapshots WHERE path = ? ORDER BY id DESC LIMIT ?`, p, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		var ns int64
		if err := rows.Scan(&sn.Path, &sn.SHA, &sn.Content, &ns, &sn.Source, &sn.PlanID, &sn.Actor); err != nil {
			return nil, err
		}
		sn.At = time.Unix(0, ns).UTC()
		out = append(out, sn)
	}
	return out, rows.Err()
}

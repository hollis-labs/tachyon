package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func prepare(obs Observation) (Observation, error) {
	if err := validateScope(obs.Key.SourceScope); err != nil {
		return obs, err
	}
	if strings.TrimSpace(obs.Key.ExternalID) == "" || obs.ObservedAt.IsZero() {
		return obs, fmt.Errorf("%w: external ID and observation time required", ErrInvalid)
	}
	obs.ObservedAt = obs.ObservedAt.UTC()
	if len(obs.Definition) > 0 || !obs.Deleted {
		d, err := canonical(obs.Definition)
		if err != nil {
			return obs, err
		}
		if _, _, err = validateDefinition(obs.Key.Kind, d); err != nil {
			return obs, err
		}
		obs.Definition = d
	}
	return obs, nil
}
func watermark(ctx context.Context, tx *sql.Tx, scope SourceScope) (time.Time, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT observed_at FROM object_sync_watermarks WHERE provider=? AND instance=? AND collection=? AND kind=?`, scope.Provider, scope.Instance, scope.Collection, scope.Kind).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(stamp, value)
}
func upsert(ctx context.Context, tx *sql.Tx, obs Observation, actor string) (Record, error) {
	if actor == "" {
		return Record{}, ErrInvalid
	}
	mark, err := watermark(ctx, tx, obs.Key.SourceScope)
	if err != nil {
		return Record{}, err
	}
	if obs.ObservedAt.Before(mark) {
		return Record{}, ErrStaleObservation
	}
	r, err := lookup(ctx, tx, obs.Key)
	if errors.Is(err, ErrNotFound) {
		if !mark.IsZero() && obs.ObservedAt.Equal(mark) {
			return Record{}, ErrStaleObservation
		}
		if len(obs.Definition) == 0 {
			return Record{}, fmt.Errorf("%w: first observation needs a definition", ErrInvalid)
		}
		id, err := uuid.NewV7()
		if err != nil {
			return Record{}, err
		}
		r = Record{ID: id.String(), Kind: obs.Key.Kind, Ownership: "source", Local: []byte(`{}`), Metadata: []byte(`{}`), Source: &Source{Key: obs.Key}}
	} else if err != nil {
		return Record{}, err
	}
	src := r.Source
	if obs.ObservedAt.Before(src.ObservedAt) {
		return Record{}, ErrStaleObservation
	}
	definition := obs.Definition
	if obs.Deleted && obs.Version == "" {
		obs.Version = src.Version
	}
	if len(definition) == 0 {
		definition = src.Definition
	} // explicit tombstone preserves last observed content
	changed := string(definition) != string(src.Definition) || obs.Version != src.Version || obs.Deleted != (src.DeletedAt != nil)
	if !src.ObservedAt.IsZero() && obs.ObservedAt.Equal(src.ObservedAt) && changed {
		return Record{}, fmt.Errorf("%w: different data at the same observation time", ErrConflict)
	}
	if !changed {
		// A successful poll advances liveness/order without inventing a revision.
		_, err := tx.ExecContext(ctx, "UPDATE object_sources SET observed_at=? WHERE object_id=?", timestamp(obs.ObservedAt), r.ID)
		src.ObservedAt = obs.ObservedAt
		return r, err
	}
	src.Definition, src.Version, src.ObservedAt = definition, obs.Version, obs.ObservedAt
	if obs.Deleted {
		if src.DeletedAt == nil {
			t := obs.ObservedAt
			src.DeletedAt = &t
		}
	} else {
		src.DeletedAt = nil
	}
	operation := "observe"
	if obs.Deleted {
		operation = "source_delete"
	}
	if err := persist(ctx, tx, &r, operation, actor); err != nil {
		return Record{}, err
	}
	return r, nil
}

// UpdateOrCreate imports normalized data using its immutable source identity.
// Neither names nor repository URLs are identity keys. Local edits are retained.
func (s *Service) UpdateOrCreate(ctx context.Context, observation Observation, actor string) (Record, error) {
	obs, err := prepare(observation)
	if err != nil {
		return Record{}, err
	}
	var out Record
	err = s.store.Writer.Submit(ctx, "object_observe", func(ctx context.Context, tx *sql.Tx) error {
		r, e := upsert(ctx, tx, obs, actor)
		if e == nil {
			out = r
		}
		return e
	})
	if err != nil {
		return Record{}, err
	}
	return out, nil
}

// Reconcile applies one collection observation atomically. complete must only be
// true after all unfiltered pages succeeded. Partial observations never infer
// removals. observedAt is the scan START time, preventing slow polls from winning
// over newer scans; external version tokens remain opaque provenance.
func (s *Service) Reconcile(ctx context.Context, scope SourceScope, observedAt time.Time, observations []Observation, complete bool, actor string) ([]Record, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if observedAt.IsZero() || actor == "" {
		return nil, ErrInvalid
	}
	if len(observations) > 10000 {
		return nil, fmt.Errorf("%w: collection exceeds 10000 records", ErrInvalid)
	}
	observedAt = observedAt.UTC()
	prepared := make([]Observation, len(observations))
	seen := map[string]bool{}
	// Bound the single queued operation. Adapters can stage a large scan later;
	// they must never silently truncate one and call it complete.
	totalBytes := 0
	for i, ob := range observations {
		if ob.Key.SourceScope != scope || !ob.ObservedAt.Equal(observedAt) || seen[ob.Key.ExternalID] {
			return nil, fmt.Errorf("%w: mixed scope/time or duplicate source ID", ErrInvalid)
		}
		p, err := prepare(ob)
		if err != nil {
			return nil, err
		}
		prepared[i] = p
		seen[p.Key.ExternalID] = true
		totalBytes += len(p.Definition)
		if totalBytes > 16<<20 {
			return nil, fmt.Errorf("%w: collection exceeds 16 MiB", ErrInvalid)
		}
	}
	var out []Record
	err := s.store.Writer.Submit(ctx, "object_reconcile", func(ctx context.Context, tx *sql.Tx) error {
		out = nil
		mark, err := watermark(ctx, tx, scope)
		if err != nil {
			return err
		}
		if observedAt.Before(mark) {
			return ErrStaleObservation
		}
		for _, ob := range prepared {
			r, err := upsert(ctx, tx, ob, actor)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		if !complete {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT external_id FROM object_sources WHERE provider=? AND instance=? AND collection=? AND kind=? AND deleted_at IS NULL`, scope.Provider, scope.Instance, scope.Collection, scope.Kind)
		if err != nil {
			return err
		}
		var missing []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !seen[id] {
				missing = append(missing, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range missing {
			r, err := upsert(ctx, tx, Observation{Key: SourceKey{SourceScope: scope, ExternalID: id}, ObservedAt: observedAt, Deleted: true}, actor)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO object_sync_watermarks(provider,instance,collection,kind,observed_at) VALUES(?,?,?,?,?) ON CONFLICT(provider,instance,collection,kind) DO UPDATE SET observed_at=excluded.observed_at`, scope.Provider, scope.Instance, scope.Collection, scope.Kind, timestamp(observedAt))
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

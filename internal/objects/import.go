package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Correlation explicitly attaches a source identity to an existing Tachyon
// master. Names never correlate records; the caller supplies ID and revision.
type Correlation struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// ImportOwned seeds a Tachyon master once from a normalized observation. Later
// imports update only its source snapshot. They cannot replace authored fields,
// remove the master or resurrect a locally deleted record. No upstream write is
// implied. A source-owned record requires an explicit TakeOwnership first.
func (s *Service) ImportOwned(ctx context.Context, observation Observation, target *Correlation, actor string) (Record, error) {
	return s.importOwned(ctx, observation, target, actor, false)
}

// RefreshImported is an explicit migration refresh, never a recurring sync.
// An unchanged upstream preserves authored edits. A changed upstream replaces
// only an untouched master; divergent local edits or tombstones conflict. The
// comparison and both source/master updates share the writer transaction.
func (s *Service) RefreshImported(ctx context.Context, observation Observation, actor string) (Record, error) {
	if observation.Deleted {
		return Record{}, ErrInvalid
	}
	return s.importOwned(ctx, observation, nil, actor, true)
}

func (s *Service) importOwned(ctx context.Context, observation Observation, target *Correlation, actor string, refresh bool) (Record, error) {
	obs, err := prepare(observation)
	if err != nil {
		return Record{}, err
	}
	var out Record
	err = s.store.Writer.Submit(ctx, "object_import_owned", func(ctx context.Context, tx *sql.Tx) error {
		if actor == "" {
			return ErrInvalid
		}
		existing, err := lookup(ctx, tx, obs.Key)
		if err == nil {
			if existing.Ownership != "tachyon" || target != nil && (target.ID != existing.ID || target.ExpectedRevision != existing.Revision) {
				return ErrConflict
			}
			changed := refresh && string(obs.Definition) != string(existing.Source.Definition)
			if changed && (existing.Deleted() || string(existing.Local) != string(existing.Source.Definition)) {
				return fmt.Errorf("%w: imported master has local edits; reconcile explicitly", ErrConflict)
			}
			out, err = upsert(ctx, tx, obs, actor)
			if err == nil && changed {
				out.Local = obs.Definition
				err = persist(ctx, tx, &out, "refresh_import", actor)
			}
			return err
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if obs.Deleted {
			return fmt.Errorf("%w: cannot seed or correlate from a tombstone", ErrInvalid)
		}
		mark, err := watermark(ctx, tx, obs.Key.SourceScope)
		if err != nil {
			return err
		}
		if !mark.IsZero() && !obs.ObservedAt.After(mark) {
			return ErrStaleObservation
		}
		operation := "import_owned"
		var r Record
		if target != nil {
			r, err = get(ctx, tx, target.ID)
			if err != nil {
				return err
			}
			if r.Kind != obs.Key.Kind || r.Ownership != "tachyon" || r.Source != nil || r.Deleted() || r.Revision != target.ExpectedRevision {
				return ErrConflict
			}
			operation = "correlate"
		} else {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			r = Record{ID: id.String(), Kind: obs.Key.Kind, Ownership: "tachyon", Local: obs.Definition, Metadata: []byte(`{}`)}
		}
		r.Source = &Source{Key: obs.Key, Version: obs.Version, ObservedAt: obs.ObservedAt, Definition: obs.Definition}
		if err := persist(ctx, tx, &r, operation, actor); err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return out, nil
}

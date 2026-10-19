package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type Replacement struct {
	ID               string
	ExpectedRevision int64
	Definition       json.RawMessage
}

// ReplaceDefinitions is a revision-checked atomic batch for owned editing and
// ordering. Metadata, source provenance and tombstones cannot be replaced here.
func (s *Service) ReplaceDefinitions(ctx context.Context, changes []Replacement, actor string) ([]Record, error) {
	if len(changes) == 0 || len(changes) > 500 || actor == "" {
		return nil, ErrInvalid
	}
	prepared := make([]Replacement, len(changes))
	seen := map[string]bool{}
	for i, c := range changes {
		if c.ID == "" || c.ExpectedRevision < 1 || seen[c.ID] {
			return nil, ErrInvalid
		}
		seen[c.ID] = true
		b, err := canonical(c.Definition)
		if err != nil {
			return nil, err
		}
		prepared[i] = Replacement{c.ID, c.ExpectedRevision, b}
	}
	out := make([]Record, 0, len(changes))
	err := s.store.Writer.Submit(ctx, "objects_replace", func(ctx context.Context, tx *sql.Tx) error {
		for _, c := range prepared {
			r, err := get(ctx, tx, c.ID)
			if err != nil {
				return err
			}
			if r.Revision != c.ExpectedRevision {
				return ErrConflict
			}
			if r.Ownership != "tachyon" || r.Deleted() {
				return fmt.Errorf("%w: active owned record required", ErrInvalid)
			}
			if string(r.Local) != string(c.Definition) {
				r.Local = c.Definition
				if err := persist(ctx, tx, &r, "replace_definition", actor); err != nil {
					return err
				}
			}
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

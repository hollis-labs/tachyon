package objects

import (
	"context"
	"database/sql"
	"errors"
)

// ObserveRunTx joins a trusted integration's receipt transaction to the normal
// object merge/history path. Callers must roll back on error and must not nest
// a writer submission. Replaying older evidence returns the newer record.
func ObserveRunTx(ctx context.Context, tx *sql.Tx, observation Observation, actor string) (Record, error) {
	if observation.Key.Kind != RunKind {
		return Record{}, ErrInvalid
	}
	obs, err := prepare(observation)
	if err != nil {
		return Record{}, err
	}
	record, err := upsert(ctx, tx, obs, actor)
	if errors.Is(err, ErrStaleObservation) {
		return lookup(ctx, tx, obs.Key)
	}
	return record, err
}

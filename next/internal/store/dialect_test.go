package store_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	libstore "github.com/hollis-labs/libs/core/store"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// Concurrent read-then-write transactions through WriteTx never collide on a key, on either dialect:
// each reads the next number and inserts it, as the domain code does for the next revision. On
// Postgres this holds only because WriteTx serializes Tachyon's writes (under read committed two
// transactions would read the same max and one would fail on the primary key).
func TestConcurrentReadThenWriteDoesNotCollide(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t).Open(t)
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.WriteTx(ctx, s.Lib(), func(ctx context.Context, tx libstore.Tx) error {
				var next int
				if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(CAST(key AS INTEGER)),0)+1 FROM app_settings`).Scan(&next); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key, value) VALUES (?, ?)`, strconv.Itoa(next), "x")
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	if err := errors.Join(all...); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Reader.QueryRowContext(ctx, `SELECT count(*) FROM app_settings`).Scan(&n); err != nil || n != writers {
		t.Fatalf("%d rows, want %d (%v)", n, writers, err)
	}
}

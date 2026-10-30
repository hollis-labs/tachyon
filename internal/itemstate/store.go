// Package itemstate is the shared console item-state store behind /console/state (PR2). It is the
// one implementation: pull-request triage uses it now and Alerts compat (AL1) sits on it. A row is
// the UI's ItemState {key,status,snoozeUntil,resolution,note,by,updatedAt} plus a version for
// optimistic concurrency. Rows hold no content, only the operator's decision about a keyed item.
package itemstate

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Statuses and ops are the existing UI StateStore vocabulary.
var (
	Statuses = []string{"unread", "read", "dismissed", "snoozed", "settled"}
	Ops      = []string{"read", "dismiss", "snooze", "settle", "reopen"}
)

const (
	MaxKey       = 200
	MaxNote      = 500
	MaxResolve   = 100
	MaxSnooze    = 90 * 24 * time.Hour
	MaxBulk      = 100
	ListLimit    = 1000
	WakeInterval = 30 * time.Second
)

// Errors a caller can map to HTTP statuses.
var (
	ErrInvalid  = errors.New("invalid")
	ErrConflict = errors.New("version conflict")
)

// InvalidError carries the message shown to the client.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string   { return e.Msg }
func (e *InvalidError) Is(t error) bool { return t == ErrInvalid }

// ConflictError carries the version the row has now.
type ConflictError struct{ Current int }

func (e *ConflictError) Error() string   { return "the entry changed" }
func (e *ConflictError) Is(t error) bool { return t == ErrConflict }

// State is one row.
type State struct {
	Key        string
	Status     string
	SnoozeUtil *time.Time
	WakeAt     *time.Time
	Note       string
	Resolution string
	By         string
	Version    int
	UpdatedAt  time.Time
}

// Op is one change request.
type Op struct {
	Type            string
	Note            string
	UntilMS         int64  // epoch ms; 0 when absent
	Preset          string // 1h | 4h | tomorrow
	Resolution      string
	ExpectedVersion *int
}

// Store is the SQLite-backed store. Prefixes, when set, is an allow-list of key prefixes; the console
// client's own keys (alert event ids) are arbitrary, so the default is any key.
type Store struct {
	DB       *localstore.Store
	Prefixes []string
	Now      func() time.Time
	// OnWake, if set, is told which keys just woke from a snooze (the Alerts/PR event hook).
	OnWake func(keys []string)
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// CheckKey validates a key against length, characters and the prefix allow-list.
func (s *Store) CheckKey(key string) error {
	if key == "" || len(key) > MaxKey {
		return &InvalidError{"key must be 1 to 200 characters."}
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return &InvalidError{"key has control characters."}
		}
	}
	if len(s.Prefixes) == 0 {
		return nil
	}
	for _, p := range s.Prefixes {
		if strings.HasPrefix(key, p) && len(key) > len(p) {
			return nil
		}
	}
	return &InvalidError{"key must start with " + strings.Join(s.Prefixes, " or ") + "."}
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func scan(row interface{ Scan(...any) error }) (State, error) {
	var st State
	var snooze, wake sql.NullInt64
	var upd int64
	if err := row.Scan(&st.Key, &st.Status, &snooze, &wake, &st.Note, &st.Resolution, &st.By, &st.Version, &upd); err != nil {
		return st, err
	}
	if snooze.Valid {
		t := time.UnixMilli(snooze.Int64).UTC()
		st.SnoozeUtil = &t
	}
	if wake.Valid {
		t := time.UnixMilli(wake.Int64).UTC()
		st.WakeAt = &t
	}
	st.UpdatedAt = time.UnixMilli(upd).UTC()
	return st, nil
}

const cols = `key, status, snooze_until_ms, wake_at_ms, note, resolution, by, version, updated_ms`

// List returns rows whose key starts with prefix, newest first. An empty prefix lists everything allowed.
func (s *Store) List(ctx context.Context, prefix string) ([]State, error) {
	if len(prefix) > MaxKey {
		return nil, &InvalidError{"prefix is too long."}
	}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT `+cols+` FROM console_item_state WHERE substr(key, 1, ?) = ? ORDER BY updated_ms DESC, key LIMIT ?`,
		len(prefix), prefix, ListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []State{}
	for rows.Next() {
		st, err := scan(rows)
		if err != nil {
			return nil, err
		}
		if s.allowed(st.Key) {
			out = append(out, st)
		}
	}
	return out, rows.Err()
}

func (s *Store) allowed(key string) bool {
	if len(s.Prefixes) == 0 {
		return true
	}
	return slices.ContainsFunc(s.Prefixes, func(p string) bool { return strings.HasPrefix(key, p) })
}

// Get returns one row, or (zero, false).
func (s *Store) Get(ctx context.Context, key string) (State, bool, error) {
	st, err := scan(s.DB.Reader.QueryRowContext(ctx, `SELECT `+cols+` FROM console_item_state WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, false, nil
	}
	return st, err == nil, err
}

func (s *Store) resolveSnooze(o Op, now time.Time) (time.Time, error) {
	switch o.Preset {
	case "1h":
		return now.Add(time.Hour), nil
	case "4h":
		return now.Add(4 * time.Hour), nil
	case "tomorrow":
		return now.Add(24 * time.Hour), nil
	case "":
		t := time.UnixMilli(o.UntilMS).UTC()
		if o.UntilMS <= 0 || !t.After(now) || t.After(now.Add(MaxSnooze)) {
			return time.Time{}, &InvalidError{"snooze needs a preset (1h, 4h, tomorrow) or a future until within 90 days."}
		}
		return t, nil
	}
	return time.Time{}, &InvalidError{"unknown snooze preset."}
}

func (s *Store) validate(key string, o Op) error {
	if !slices.Contains(Ops, o.Type) {
		return &InvalidError{"type must be one of read, dismiss, snooze, settle, reopen."}
	}
	if err := s.CheckKey(key); err != nil {
		return err
	}
	if len(o.Note) > MaxNote || len(o.Resolution) > MaxResolve {
		return &InvalidError{"note or resolution is too long."}
	}
	if o.ExpectedVersion != nil && *o.ExpectedVersion < 0 {
		return &InvalidError{"expected_version must not be negative."}
	}
	return nil
}

// Apply performs one op in a single write transaction and returns the new row. A missing row is
// version 0, so expected_version 0 means "create only if absent".
func (s *Store) Apply(ctx context.Context, key, by string, o Op) (State, error) {
	if err := s.validate(key, o); err != nil {
		return State{}, err
	}
	now := s.now()
	var until time.Time
	if o.Type == "snooze" {
		var err error
		if until, err = s.resolveSnooze(o, now); err != nil {
			return State{}, err
		}
	}
	var out State
	err := s.DB.Writer.Submit(ctx, "item_state", func(ctx context.Context, tx *sql.Tx) error {
		cur, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM console_item_state WHERE key = ?`, key))
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		have := 0
		if exists {
			have = cur.Version
		}
		if o.ExpectedVersion != nil && *o.ExpectedVersion != have {
			return &ConflictError{Current: have}
		}
		status := map[string]string{"read": "read", "dismiss": "dismissed", "snooze": "snoozed", "settle": "settled", "reopen": "unread"}[o.Type]
		var snooze, resolution any
		if o.Type == "snooze" {
			snooze = ms(until)
		}
		res := ""
		if o.Type == "settle" {
			res = o.Resolution
		}
		_ = resolution
		note := o.Note
		if note == "" && exists && o.Type != "reopen" {
			note = cur.Note
		}
		if exists {
			_, err = tx.ExecContext(ctx, `UPDATE console_item_state SET status=?, snooze_until_ms=?, wake_at_ms=NULL, note=?, resolution=?, by=?, version=version+1, updated_ms=? WHERE key=?`,
				status, snooze, note, res, by, ms(now), key)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO console_item_state(key,status,snooze_until_ms,note,resolution,by,version,created_ms,updated_ms) VALUES (?,?,?,?,?,?,1,?,?)`,
				key, status, snooze, note, res, by, ms(now), ms(now))
		}
		if err != nil {
			return err
		}
		out, err = scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM console_item_state WHERE key = ?`, key))
		return err
	})
	return out, err
}

// Result is one bulk outcome.
type Result struct {
	Key   string
	State *State
	Err   error
}

// BulkOp is a keyed op.
type BulkOp struct {
	Key string
	Op
}

// Bulk applies each op independently (one failure does not stop the rest) and reports per key.
func (s *Store) Bulk(ctx context.Context, by string, ops []BulkOp) []Result {
	out := make([]Result, 0, len(ops))
	for _, o := range ops {
		st, err := s.Apply(ctx, o.Key, by, o.Op)
		r := Result{Key: o.Key, Err: err}
		if err == nil {
			r.State = &st
		}
		out = append(out, r)
	}
	return out
}

// Wake turns snoozes that have expired back to unread, stamping wake_at, and reports the keys.
func (s *Store) Wake(ctx context.Context) ([]string, error) {
	var keys []string
	now := s.now()
	err := s.DB.Writer.Submit(ctx, "item_state_wake", func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT key FROM console_item_state WHERE status='snoozed' AND snooze_until_ms <= ?`, ms(now))
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, k)
		}
		rows.Close()
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, `UPDATE console_item_state SET status='unread', snooze_until_ms=NULL, wake_at_ms=?, version=version+1, updated_ms=? WHERE key=?`, ms(now), ms(now), k); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && len(keys) > 0 && s.OnWake != nil {
		s.OnWake(keys)
	}
	return keys, err
}

// Maintain runs Wake every interval until ctx ends.
func (s *Store) Maintain(ctx context.Context, interval time.Duration, log func(msg string, args ...any)) {
	if interval <= 0 {
		interval = WakeInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.Wake(ctx); err != nil && ctx.Err() == nil && log != nil {
			log("item state wake failed", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

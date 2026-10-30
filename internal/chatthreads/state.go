package chatthreads

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// State is the overlay row for one thread (either source).
type State struct {
	Ref        string
	PinnedNS   int64 // 0 = not pinned
	ArchivedNS int64
	Title      string
	ProjectID  string
	ForkedFrom string
	ReadNS     int64
}

// Patch is a partial overlay update. Nil fields are untouched.
type Patch struct {
	Pinned     *bool
	Archived   *bool
	Title      *string // "" clears the override
	SetProject bool    // distinguishes project_id:null (clear) from absent
	ProjectID  string
}

// StateStore reads and writes the overlay through the one SQLite writer.
type StateStore struct{ DB *localstore.Store }

const stateColumns = `thread_ref,COALESCE(pinned_ns,0),COALESCE(archived_ns,0),COALESCE(title_override,''),COALESCE(project_id,''),COALESCE(forked_from,''),read_ns`

func scanState(row interface{ Scan(...any) error }) (State, error) {
	var s State
	err := row.Scan(&s.Ref, &s.PinnedNS, &s.ArchivedNS, &s.Title, &s.ProjectID, &s.ForkedFrom, &s.ReadNS)
	return s, err
}

// All returns every overlay row by thread_ref.
func (s *StateStore) All(ctx context.Context) (map[string]State, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT `+stateColumns+` FROM chat_thread_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]State{}
	for rows.Next() {
		st, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out[st.Ref] = st
	}
	return out, rows.Err()
}

func (s *StateStore) Get(ctx context.Context, ref string) (State, error) {
	st, err := scanState(s.DB.Reader.QueryRowContext(ctx, `SELECT `+stateColumns+` FROM chat_thread_state WHERE thread_ref=?`, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return State{Ref: ref}, nil
	}
	return st, err
}

func nz(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nzs(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// Apply upserts the overlay and returns the result.
func (s *StateStore) Apply(ctx context.Context, ref string, p Patch, now time.Time) (State, error) {
	var out State
	err := s.DB.Writer.Submit(ctx, "chat_thread_state", func(ctx context.Context, tx *sql.Tx) error {
		cur, err := scanState(tx.QueryRowContext(ctx, `SELECT `+stateColumns+` FROM chat_thread_state WHERE thread_ref=?`, ref))
		if errors.Is(err, sql.ErrNoRows) {
			cur, err = State{Ref: ref}, nil
		}
		if err != nil {
			return err
		}
		if p.Pinned != nil {
			cur.PinnedNS = 0
			if *p.Pinned {
				cur.PinnedNS = now.UnixNano()
			}
		}
		if p.Archived != nil {
			cur.ArchivedNS = 0
			if *p.Archived {
				cur.ArchivedNS = now.UnixNano()
			}
		}
		if p.Title != nil {
			cur.Title = *p.Title
		}
		if p.SetProject {
			cur.ProjectID = p.ProjectID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_thread_state(thread_ref,pinned_ns,archived_ns,title_override,project_id,forked_from,read_ns,updated_ns) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(thread_ref) DO UPDATE SET pinned_ns=excluded.pinned_ns,archived_ns=excluded.archived_ns,title_override=excluded.title_override,project_id=excluded.project_id,updated_ns=excluded.updated_ns`,
			ref, nz(cur.PinnedNS), nz(cur.ArchivedNS), nzs(cur.Title), nzs(cur.ProjectID), nzs(cur.ForkedFrom), cur.ReadNS, now.UnixNano())
		out = cur
		return err
	})
	return out, err
}

// MarkRead records that the operator has seen everything up to now.
func (s *StateStore) MarkRead(ctx context.Context, ref string, now time.Time) error {
	return s.DB.Writer.Submit(ctx, "chat_thread_read", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_thread_state(thread_ref,read_ns,updated_ns) VALUES(?,?,?) ON CONFLICT(thread_ref) DO UPDATE SET read_ns=excluded.read_ns,updated_ns=excluded.updated_ns`, ref, now.UnixNano(), now.UnixNano())
		return err
	})
}

// Key returns the stored result of an earlier send with this idempotency key.
func (s *StateStore) Key(ctx context.Context, ref, key string) (messageID, replyID string, seq int64, ok bool, err error) {
	err = s.DB.Reader.QueryRowContext(ctx, `SELECT message_id,reply_id,seq FROM chat_message_keys WHERE thread_ref=? AND idempotency_key=?`, ref, key).Scan(&messageID, &replyID, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, false, nil
	}
	return messageID, replyID, seq, err == nil, err
}

func (s *StateStore) SaveKey(ctx context.Context, ref, key, messageID, replyID string, seq int64, now time.Time) error {
	return s.DB.Writer.Submit(ctx, "chat_message_key", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chat_message_keys(thread_ref,idempotency_key,message_id,reply_id,seq,created_ns) VALUES(?,?,?,?,?,?)`, ref, key, messageID, replyID, seq, now.UnixNano())
		return err
	})
}

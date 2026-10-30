// Package chatworkspace is the per-thread workspace (files, links, refs), the scratchpad and
// the chat settings. Files are bounded, sniffed against an allow-list, stored under the data
// dir and never served by path.
package chatworkspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	MaxFileBytes  = 25 << 20
	MaxItems      = 100
	MaxTotalBytes = 250 << 20
	MaxScratchpad = 64 << 10
)

var (
	ErrNotFound = errors.New("no such workspace item")
	ErrConflict = errors.New("revision conflict")
	ErrLimit    = errors.New("workspace limit reached")
)

type Item struct {
	ID        string  `json:"id"`
	ThreadRef string  `json:"thread_ref"`
	Type      string  `json:"type"`
	Name      string  `json:"name"`
	Mime      *string `json:"mime"`
	SizeBytes *int64  `json:"size_bytes"`
	SHA256    *string `json:"sha256"`
	URL       *string `json:"url"`
	Ref       *string `json:"ref"`
	Shared    bool    `json:"shared"`
	AddedBy   string  `json:"added_by"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type Limits struct {
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxItems      int   `json:"max_items"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	UsedItems     int   `json:"used_items"`
	UsedBytes     int64 `json:"used_bytes"`
}

type Scratchpad struct {
	ThreadRef string `json:"thread_ref"`
	Body      string `json:"body"`
	Revision  int64  `json:"revision"`
	UpdatedAt int64  `json:"updated_at"`
}

// Store persists workspace rows through the one SQLite writer and keeps file bytes on disk.
type Store struct {
	DB  *localstore.Store
	Dir string // $TACHYON_DATA_DIR/workspace
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

const itemColumns = `id,thread_ref,type,name,mime,size_bytes,sha256,url,ref,shared,added_by,created_ns,updated_ns`

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var mime, sum, url, ref sql.NullString
	var size sql.NullInt64
	var shared int
	var created, updated int64
	if err := row.Scan(&it.ID, &it.ThreadRef, &it.Type, &it.Name, &mime, &size, &sum, &url, &ref, &shared, &it.AddedBy, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Item{}, ErrNotFound
		}
		return Item{}, err
	}
	if mime.Valid {
		it.Mime = &mime.String
	}
	if size.Valid {
		it.SizeBytes = &size.Int64
	}
	if sum.Valid {
		it.SHA256 = &sum.String
	}
	if url.Valid {
		it.URL = &url.String
	}
	if ref.Valid {
		it.Ref = &ref.String
	}
	it.Shared = shared == 1
	it.CreatedAt, it.UpdatedAt = created/1e6, updated/1e6
	return it, nil
}

func (s *Store) List(ctx context.Context, thread string) ([]Item, Limits, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT `+itemColumns+` FROM chat_workspace_items WHERE thread_ref=? ORDER BY created_ns,id`, thread)
	if err != nil {
		return nil, Limits{}, err
	}
	defer rows.Close()
	out := []Item{}
	lim := Limits{MaxFileBytes: MaxFileBytes, MaxItems: MaxItems, MaxTotalBytes: MaxTotalBytes}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, lim, err
		}
		out = append(out, it)
		lim.UsedItems++
		if it.SizeBytes != nil {
			lim.UsedBytes += *it.SizeBytes
		}
	}
	return out, lim, rows.Err()
}

// Get returns one item of the thread; an item of another thread is "not found".
func (s *Store) Get(ctx context.Context, thread, id string) (Item, error) {
	return scanItem(s.DB.Reader.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM chat_workspace_items WHERE thread_ref=? AND id=?`, thread, id))
}

// NewFile is a stored upload awaiting its row. Path is under Dir.
type NewFile struct {
	ID, Name, Mime, Type, SHA256, AddedBy string
	Size                                  int64
	Shared                                bool
}

// Path is where a file item's bytes live. It derives from the thread and item id only.
func (s *Store) Path(thread, id string) string {
	h := sha256.Sum256([]byte(thread))
	return filepath.Join(s.Dir, hex.EncodeToString(h[:8]), id)
}

func (s *Store) Add(ctx context.Context, thread string, it Item) (Item, error) {
	it.ID, it.ThreadRef = firstID(it.ID), thread
	now := s.now()
	err := s.DB.Writer.Submit(ctx, "chat_workspace_add", func(ctx context.Context, tx *sql.Tx) error {
		var n int
		var used int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM chat_workspace_items WHERE thread_ref=?`, thread).Scan(&n, &used); err != nil {
			return err
		}
		if n >= MaxItems || (it.SizeBytes != nil && used+*it.SizeBytes > MaxTotalBytes) {
			return ErrLimit
		}
		shared := 0
		if it.Shared {
			shared = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_workspace_items(`+itemColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			it.ID, thread, it.Type, it.Name, it.Mime, it.SizeBytes, it.SHA256, it.URL, it.Ref, shared, it.AddedBy, now.UnixNano(), now.UnixNano())
		return err
	})
	if err != nil {
		return Item{}, err
	}
	return s.Get(ctx, thread, it.ID)
}

func firstID(id string) string {
	if id != "" {
		return id
	}
	return "ws_" + uuid.NewString()
}

// Patch changes name and/or shared. Nil fields are untouched.
func (s *Store) Patch(ctx context.Context, thread, id string, name *string, shared *bool) (Item, error) {
	err := s.DB.Writer.Submit(ctx, "chat_workspace_patch", func(ctx context.Context, tx *sql.Tx) error {
		cur, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM chat_workspace_items WHERE thread_ref=? AND id=?`, thread, id))
		if err != nil {
			return err
		}
		if name != nil {
			cur.Name = *name
		}
		if shared != nil {
			cur.Shared = *shared
		}
		sh := 0
		if cur.Shared {
			sh = 1
		}
		_, err = tx.ExecContext(ctx, `UPDATE chat_workspace_items SET name=?,shared=?,updated_ns=? WHERE id=?`, cur.Name, sh, s.now().UnixNano(), id)
		return err
	})
	if err != nil {
		return Item{}, err
	}
	return s.Get(ctx, thread, id)
}

// Delete removes the row, then its bytes.
func (s *Store) Delete(ctx context.Context, thread, id string) error {
	err := s.DB.Writer.Submit(ctx, "chat_workspace_delete", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM chat_workspace_items WHERE thread_ref=? AND id=?`, thread, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err == nil {
		_ = os.Remove(s.Path(thread, id))
	}
	return err
}

// PurgeThread removes every workspace row, file and the scratchpad of a thread: the
// explicit thread-delete path. Archiving a thread never calls it.
func (s *Store) PurgeThread(ctx context.Context, thread string) error {
	err := s.DB.Writer.Submit(ctx, "chat_workspace_purge", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_workspace_items WHERE thread_ref=?`, thread); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM chat_scratchpads WHERE thread_ref=?`, thread)
		return err
	})
	if err == nil {
		_ = os.RemoveAll(filepath.Dir(s.Path(thread, "x")))
	}
	return err
}

func (s *Store) Scratchpad(ctx context.Context, thread string) (Scratchpad, error) {
	sp := Scratchpad{ThreadRef: thread}
	var updated int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT body,revision,updated_ns FROM chat_scratchpads WHERE thread_ref=?`, thread).Scan(&sp.Body, &sp.Revision, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return sp, nil
	}
	sp.UpdatedAt = updated / 1e6
	return sp, err
}

// SaveScratchpad replaces the body when expected equals the current revision (0 = none yet).
func (s *Store) SaveScratchpad(ctx context.Context, thread, body string, expected int64) (Scratchpad, error) {
	now := s.now()
	err := s.DB.Writer.Submit(ctx, "chat_scratchpad", func(ctx context.Context, tx *sql.Tx) error {
		var rev int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM chat_scratchpads WHERE thread_ref=?`, thread).Scan(&rev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if rev != expected {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_scratchpads(thread_ref,body,revision,updated_ns) VALUES(?,?,?,?) ON CONFLICT(thread_ref) DO UPDATE SET body=excluded.body,revision=excluded.revision,updated_ns=excluded.updated_ns`, thread, body, rev+1, now.UnixNano())
		return err
	})
	if err != nil {
		return Scratchpad{}, err
	}
	return s.Scratchpad(ctx, thread)
}

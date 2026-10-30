// Package chattopics holds a chat thread's topics (named jump points) and bookmarks.
package chattopics

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	MaxTopics    = 200
	MaxBookmarks = 500
	MaxTitle     = 200
	MaxNote      = 500
)

var (
	ErrNotFound = errors.New("no such topic or bookmark")
	ErrLimit    = errors.New("limit reached")
	ErrInvalid  = errors.New("invalid topic or bookmark")
)

type Topic struct {
	ID         string `json:"id"`
	ThreadRef  string `json:"thread_ref"`
	Title      string `json:"title"`
	MessageRef string `json:"message_ref"`
	Seq        *int64 `json:"seq"`
	AddedBy    string `json:"added_by"`
	CreatedAt  int64  `json:"created_at"`
}

type Bookmark struct {
	ID         string `json:"id"`
	ThreadRef  string `json:"thread_ref"`
	MessageRef string `json:"message_ref"`
	Note       string `json:"note"`
	CreatedAt  int64  `json:"created_at"`
}

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

func (s *Store) Topics(ctx context.Context, thread string) ([]Topic, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT id,thread_ref,title,message_ref,seq,added_by,created_ns FROM chat_topics WHERE thread_ref=? ORDER BY COALESCE(seq,9223372036854775807),created_ns,id`, thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Topic{}
	for rows.Next() {
		var t Topic
		var seq sql.NullInt64
		var ns int64
		if err := rows.Scan(&t.ID, &t.ThreadRef, &t.Title, &t.MessageRef, &seq, &t.AddedBy, &ns); err != nil {
			return nil, err
		}
		if seq.Valid {
			t.Seq = &seq.Int64
		}
		t.CreatedAt = ns / 1e6
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddTopic validates and stores a topic. addedBy is "user" or "agent".
func (s *Store) AddTopic(ctx context.Context, thread, title, messageRef string, seq *int64, addedBy string) (Topic, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > MaxTitle || messageRef == "" || len(messageRef) > 200 || (addedBy != "user" && addedBy != "agent") {
		return Topic{}, ErrInvalid
	}
	t := Topic{ID: "tp_" + uuid.NewString(), ThreadRef: thread, Title: title, MessageRef: messageRef, Seq: seq, AddedBy: addedBy}
	now := s.now()
	t.CreatedAt = now.UnixMilli()
	err := s.DB.Writer.Submit(ctx, "chat_topic_add", func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_topics WHERE thread_ref=?`, thread).Scan(&n); err != nil {
			return err
		}
		if n >= MaxTopics {
			return ErrLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_topics(id,thread_ref,title,message_ref,seq,added_by,created_ns) VALUES(?,?,?,?,?,?,?)`, t.ID, thread, title, messageRef, seq, addedBy, now.UnixNano())
		return err
	})
	return t, err
}

func (s *Store) DeleteTopic(ctx context.Context, thread, id string) error {
	return s.deleteRow(ctx, "chat_topics", thread, id)
}

func (s *Store) DeleteBookmark(ctx context.Context, thread, id string) error {
	return s.deleteRow(ctx, "chat_bookmarks", thread, id)
}

func (s *Store) deleteRow(ctx context.Context, table, thread, id string) error {
	return s.DB.Writer.Submit(ctx, "chat_delete_"+table, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE thread_ref=? AND id=?`, thread, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *Store) Bookmarks(ctx context.Context, thread string) ([]Bookmark, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT id,thread_ref,message_ref,note,created_ns FROM chat_bookmarks WHERE thread_ref=? ORDER BY created_ns,id`, thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bookmark{}
	for rows.Next() {
		var b Bookmark
		var ns int64
		if err := rows.Scan(&b.ID, &b.ThreadRef, &b.MessageRef, &b.Note, &ns); err != nil {
			return nil, err
		}
		b.CreatedAt = ns / 1e6
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddBookmark is idempotent per (thread, message): a repeat updates the note when one is
// given and returns the existing row. created reports whether a row was inserted.
func (s *Store) AddBookmark(ctx context.Context, thread, messageRef, note string) (b Bookmark, created bool, err error) {
	note = strings.TrimSpace(note)
	if messageRef == "" || len(messageRef) > 200 || len(note) > MaxNote {
		return Bookmark{}, false, ErrInvalid
	}
	now := s.now()
	err = s.DB.Writer.Submit(ctx, "chat_bookmark_add", func(ctx context.Context, tx *sql.Tx) error {
		var ns int64
		e := tx.QueryRowContext(ctx, `SELECT id,thread_ref,message_ref,note,created_ns FROM chat_bookmarks WHERE thread_ref=? AND message_ref=?`, thread, messageRef).Scan(&b.ID, &b.ThreadRef, &b.MessageRef, &b.Note, &ns)
		if e == nil {
			if note != "" && note != b.Note {
				b.Note = note
				_, e = tx.ExecContext(ctx, `UPDATE chat_bookmarks SET note=? WHERE id=?`, note, b.ID)
			}
			b.CreatedAt = ns / 1e6
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_bookmarks WHERE thread_ref=?`, thread).Scan(&n); e != nil {
			return e
		}
		if n >= MaxBookmarks {
			return ErrLimit
		}
		b = Bookmark{ID: "bk_" + uuid.NewString(), ThreadRef: thread, MessageRef: messageRef, Note: note, CreatedAt: now.UnixMilli()}
		created = true
		_, e = tx.ExecContext(ctx, `INSERT INTO chat_bookmarks(id,thread_ref,message_ref,note,created_ns) VALUES(?,?,?,?,?)`, b.ID, thread, messageRef, note, now.UnixNano())
		return e
	})
	return
}

// PurgeThread removes a thread's topics and bookmarks (the explicit thread-delete path).
func (s *Store) PurgeThread(ctx context.Context, thread string) error {
	return s.DB.Writer.Submit(ctx, "chat_topics_purge", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_topics WHERE thread_ref=?`, thread); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM chat_bookmarks WHERE thread_ref=?`, thread)
		return err
	})
}

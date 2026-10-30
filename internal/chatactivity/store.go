// Package chatactivity holds a chat thread's activity feed (append-only), the panel-open
// signals agents may send, and the cached context meter.
package chatactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	KeepPerThread = 5000
	MaxPayload    = 4 << 10
	MaxComment    = 2000
)

var (
	ErrInvalid     = errors.New("invalid activity")
	ErrRateLimited = errors.New("rate limited")
)

// Types is the closed set of activity types. "note" is the only free-form agent type.
var Types = map[string]bool{
	"tool.call": true, "card.sent": true, "card.answered": true, "approval.decided": true, "file.shared": true,
	"plan.changed": true, "task.closed": true, "panel.opened": true, "terminal.run": true,
	"topic.added": true, "bookmark.added": true, "comment": true, "note": true,
}

type Cite struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

// Entry is one activity row. Payload is a mini-card {title, cite, summary, review_ref} or, for a
// comment, {body}.
type Entry struct {
	Seq       int64           `json:"seq"`
	ThreadRef string          `json:"thread_ref"`
	Type      string          `json:"type"`
	Actor     string          `json:"actor"`
	Cite      []Cite          `json:"cite"`
	Payload   json.RawMessage `json:"payload"`
	Source    string          `json:"source"`
	At        int64           `json:"at"`
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

func clip(v string, n int) string {
	v = strings.TrimSpace(redact.Text(v))
	if r := []rune(v); len(r) > n {
		return string(r[:n])
	}
	return v
}

// Card builds a mini-card payload with every text bounded and redacted.
func Card(title, summary string, review *string) json.RawMessage {
	m := map[string]any{"title": clip(title, 120), "summary": clip(summary, 200), "review_ref": nil}
	if review != nil && *review != "" {
		m["review_ref"] = clip(*review, 200)
	}
	b, _ := json.Marshal(m)
	return b
}

func cleanCites(in []Cite) ([]Cite, error) {
	if len(in) > 8 {
		return nil, ErrInvalid
	}
	out := make([]Cite, 0, len(in))
	for _, c := range in {
		if (c.Kind != "task" && c.Kind != "pr" && c.Kind != "file") || strings.TrimSpace(c.Ref) == "" {
			return nil, ErrInvalid
		}
		out = append(out, Cite{Kind: c.Kind, Ref: clip(c.Ref, 200), Label: clip(c.Label, 80)})
	}
	return out, nil
}

// Append adds a row. The actor and source are the caller's to set; this store never invents them.
func (s *Store) Append(ctx context.Context, e Entry) (Entry, error) {
	if e.ThreadRef == "" || !Types[e.Type] || e.Actor == "" || e.Source == "" || len(e.Payload) > MaxPayload || (len(e.Payload) > 0 && !json.Valid(e.Payload)) {
		return Entry{}, ErrInvalid
	}
	cites, err := cleanCites(e.Cite)
	if err != nil {
		return Entry{}, err
	}
	e.Cite = cites
	cj, _ := json.Marshal(cites)
	var payload any
	if len(e.Payload) > 0 && string(e.Payload) != "null" {
		payload = string(e.Payload)
	} else {
		e.Payload = nil
	}
	at := s.now()
	e.At = at.UnixMilli()
	err = s.DB.Writer.Submit(ctx, "chat_activity_append", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO chat_activity(thread_ref,type,actor,cite_json,payload_json,source,at_ns) VALUES(?,?,?,?,?,?,?)`,
			e.ThreadRef, e.Type, e.Actor, string(cj), payload, e.Source, at.UnixNano())
		if err != nil {
			return err
		}
		if e.Seq, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM chat_activity WHERE thread_ref=? AND seq<=? - ?`, e.ThreadRef, e.Seq, KeepPerThread)
		return err
	})
	return e, err
}

type Query struct {
	Before int64
	Limit  int
	Type   string
	Actor  string
	Q      string
}

// List returns newest first. has_more is true when older rows match.
func (s *Store) List(ctx context.Context, thread string, q Query) ([]Entry, bool, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	sqlq := `SELECT seq,type,actor,cite_json,payload_json,source,at_ns FROM chat_activity WHERE thread_ref=?`
	args := []any{thread}
	if q.Before > 0 {
		sqlq += ` AND seq<?`
		args = append(args, q.Before)
	}
	if q.Type != "" {
		sqlq += ` AND type=?`
		args = append(args, q.Type)
	}
	if q.Actor != "" {
		sqlq += ` AND actor=?`
		args = append(args, q.Actor)
	}
	if q.Q != "" {
		sqlq += ` AND (LOWER(COALESCE(payload_json,'')) LIKE ? ESCAPE '\' OR LOWER(type) LIKE ? ESCAPE '\' OR LOWER(cite_json) LIKE ? ESCAPE '\')`
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q.Q)) + "%"
		args = append(args, like, like, like)
	}
	sqlq += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := s.DB.Reader.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e := Entry{ThreadRef: thread}
		var cj string
		var pj sql.NullString
		var ns int64
		if err := rows.Scan(&e.Seq, &e.Type, &e.Actor, &cj, &pj, &e.Source, &ns); err != nil {
			return nil, false, err
		}
		_ = json.Unmarshal([]byte(cj), &e.Cite)
		if e.Cite == nil {
			e.Cite = []Cite{}
		}
		if pj.Valid {
			e.Payload = json.RawMessage(pj.String)
		}
		e.At = ns / int64(time.Millisecond)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	return out, more, nil
}

// Context is the thread's context meter. Used and Window are null until the runtime reports them.
type Context struct {
	Used      *int64         `json:"used"`
	Window    *int64         `json:"window"`
	Model     *string        `json:"model"`
	Source    string         `json:"source"` // runtime | unknown
	Breakdown []BreakdownRow `json:"breakdown"`
	At        int64          `json:"at"`
}

type BreakdownRow struct {
	Label  string `json:"label"`
	Tokens int64  `json:"tokens"`
}

// SaveContext keeps the latest report for the thread. Zero or negative numbers are dropped to null.
func (s *Store) SaveContext(ctx context.Context, thread string, used, window int64, model string) (Context, error) {
	at := s.now()
	var u, w, m any
	if used > 0 {
		u = used
	}
	if window > 0 {
		w = window
	}
	if m2 := clip(model, 100); m2 != "" {
		m = m2
	}
	err := s.DB.Writer.Submit(ctx, "chat_context_save", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_context(thread_ref,used,window,model,at_ns) VALUES(?,?,?,?,?)
 ON CONFLICT(thread_ref) DO UPDATE SET used=COALESCE(excluded.used,used), window=COALESCE(excluded.window,window), model=COALESCE(excluded.model,model), at_ns=excluded.at_ns`,
			thread, u, w, m, at.UnixNano())
		return err
	})
	if err != nil {
		return Context{}, err
	}
	return s.Context(ctx, thread)
}

// Context returns the cached meter; a thread that never reported has source "unknown".
func (s *Store) Context(ctx context.Context, thread string) (Context, error) {
	c := Context{Source: "unknown", Breakdown: []BreakdownRow{}, At: s.now().UnixMilli()}
	var used, window sql.NullInt64
	var model sql.NullString
	var ns int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT used,window,model,at_ns FROM chat_context WHERE thread_ref=?`, thread).Scan(&used, &window, &model, &ns)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.Source, c.At = "runtime", ns/int64(time.Millisecond)
	if used.Valid {
		c.Used = &used.Int64
	}
	if window.Valid {
		c.Window = &window.Int64
	}
	if model.Valid {
		c.Model = &model.String
	}
	return c, nil
}

// PurgeThread removes a deleted thread's feed and meter.
func (s *Store) PurgeThread(ctx context.Context, thread string) error {
	return s.DB.Writer.Submit(ctx, "chat_activity_purge", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_activity WHERE thread_ref=?`, thread); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM chat_context WHERE thread_ref=?`, thread)
		return err
	})
}

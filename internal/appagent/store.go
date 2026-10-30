// Package appagent is Tachyon's chat with its own in-app agent. Tachyon owns the
// threads and messages (one writer, no mirror); the agent runs behind a Runtime
// port so Nanite today and an embedded runtime later are interchangeable.
package appagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	RoleOperator = "operator"
	RoleAgent    = "agent"

	// Operator message states.
	StateSent = "sent" // stored by Tachyon; the runtime has not yet confirmed
	// Agent message states (operator messages use sent/failed).
	StateStreaming = "streaming"
	StateComplete  = "complete"
	StateFailed    = "failed"
	// StateBlocked is a reply that ended because the agent needs the operator
	// (runtime input_required). It is a blocker, not an error.
	StateBlocked = "blocked"
)

var (
	ErrNotFound = errors.New("app agent thread or message not found")
	ErrBusy     = errors.New("a reply is still in progress")
	ErrInvalid  = errors.New("invalid app agent request")
)

// Tool is one tool call shown as a chip on an agent message.
type Tool struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // running | ok | error
	Detail string `json:"detail,omitempty"`
}

type Message struct {
	ID          string    `json:"id"`
	ThreadID    string    `json:"thread_id"`
	Seq         int64     `json:"seq"`
	Role        string    `json:"role"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	Error       string    `json:"error,omitempty"`
	RuntimeTurn string    `json:"runtime_turn,omitempty"`
	Tools       []Tool    `json:"tools"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Thread struct {
	ID             string `json:"id"`
	Agent          string `json:"agent"`
	Title          string `json:"title"`
	Owner          string `json:"owner"`
	Runtime        string `json:"runtime"`
	RuntimeSession string `json:"-"`
	Unread         int    `json:"unread"`
	// Blocked is true when the newest message is a reply waiting on the operator.
	Blocked     bool      `json:"blocked"`
	LastAt      time.Time `json:"last_at"`
	LastPreview string    `json:"last_preview,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store persists threads and messages through Tachyon's one SQLite writer.
type Store struct{ DB *localstore.Store }

func ns(t time.Time) int64 { return t.UnixNano() }

func (s *Store) CreateThread(ctx context.Context, id, agent, title string, now time.Time) (Thread, error) {
	if id == "" {
		id = "app-" + uuid.NewString()
	}
	err := s.DB.Writer.Submit(ctx, "app_agent_thread_create", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_agent_threads(id,agent,title,created_ns,updated_ns) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, id, agent, title, ns(now), ns(now))
		return err
	})
	if err != nil {
		return Thread{}, err
	}
	return s.Thread(ctx, id)
}

const threadSelect = `SELECT t.id,t.agent,t.title,t.owner,t.runtime,t.runtime_session,t.created_ns,
 COALESCE((SELECT COUNT(*) FROM app_agent_messages m WHERE m.thread_id=t.id AND m.role='agent' AND m.state IN ('complete','failed','blocked') AND m.seq>t.read_seq),0),
 COALESCE((SELECT m.updated_ns FROM app_agent_messages m WHERE m.thread_id=t.id ORDER BY m.seq DESC LIMIT 1),t.updated_ns),
 COALESCE((SELECT substr(m.body,1,160) FROM app_agent_messages m WHERE m.thread_id=t.id ORDER BY m.seq DESC LIMIT 1),''),
 COALESCE((SELECT m.state='blocked' FROM app_agent_messages m WHERE m.thread_id=t.id ORDER BY m.seq DESC LIMIT 1),0)
 FROM app_agent_threads t`

func scanThread(row interface{ Scan(...any) error }) (Thread, error) {
	var t Thread
	var created, last int64
	if err := row.Scan(&t.ID, &t.Agent, &t.Title, &t.Owner, &t.Runtime, &t.RuntimeSession, &created, &t.Unread, &last, &t.LastPreview, &t.Blocked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Thread{}, ErrNotFound
		}
		return Thread{}, err
	}
	t.CreatedAt = time.Unix(0, created).UTC()
	t.LastAt = time.Unix(0, last).UTC()
	return t, nil
}

func (s *Store) Thread(ctx context.Context, id string) (Thread, error) {
	return scanThread(s.DB.Reader.QueryRowContext(ctx, threadSelect+" WHERE t.id=?", id))
}

// Threads returns every thread, most recently active first.
func (s *Store) Threads(ctx context.Context) ([]Thread, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, threadSelect+" ORDER BY 9 DESC, t.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetSession(ctx context.Context, threadID, session string) error {
	return s.DB.Writer.Submit(ctx, "app_agent_session", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE app_agent_threads SET runtime_session=? WHERE id=?`, session, threadID)
		return err
	})
}

const messageColumns = "id,thread_id,seq,role,body,state,error,runtime_turn,tools,created_ns,updated_ns"

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var tools string
	var created, updated int64
	if err := row.Scan(&m.ID, &m.ThreadID, &m.Seq, &m.Role, &m.Body, &m.State, &m.Error, &m.RuntimeTurn, &tools, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		return Message{}, err
	}
	m.Tools = []Tool{}
	if err := json.Unmarshal([]byte(tools), &m.Tools); err != nil {
		return Message{}, err
	}
	m.CreatedAt = time.Unix(0, created).UTC()
	m.UpdatedAt = time.Unix(0, updated).UTC()
	return m, nil
}

func (s *Store) Message(ctx context.Context, id string) (Message, error) {
	return scanMessage(s.DB.Reader.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM app_agent_messages WHERE id=?", id))
}

// AppendTurn stores the operator's message and an empty streaming agent message in
// one transaction, before anything is sent to a runtime. A thread with a reply still
// in progress refuses a new turn.
func (s *Store) AppendTurn(ctx context.Context, threadID, body string, now time.Time) (operator, agent Message, err error) {
	operator = Message{ID: uuid.NewString(), ThreadID: threadID, Role: RoleOperator, Body: body, State: StateSent, Tools: []Tool{}, CreatedAt: now, UpdatedAt: now}
	agent = Message{ID: uuid.NewString(), ThreadID: threadID, Role: RoleAgent, State: StateStreaming, Tools: []Tool{}, CreatedAt: now, UpdatedAt: now}
	err = s.DB.Writer.Submit(ctx, "app_agent_append_turn", func(ctx context.Context, tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM app_agent_threads WHERE id=?`, threadID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var busy int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_agent_messages WHERE thread_id=? AND state='streaming'`, threadID).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 {
			return ErrBusy
		}
		var seq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM app_agent_messages WHERE thread_id=?`, threadID).Scan(&seq); err != nil {
			return err
		}
		operator.Seq, agent.Seq = seq+1, seq+2
		for _, m := range []Message{operator, agent} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_agent_messages(`+messageColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
				m.ID, m.ThreadID, m.Seq, m.Role, m.Body, m.State, m.Error, m.RuntimeTurn, "[]", ns(m.CreatedAt), ns(m.UpdatedAt)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE app_agent_threads SET updated_ns=? WHERE id=?`, ns(now), threadID)
		return err
	})
	return operator, agent, err
}

// SaveAgent writes an agent message's current body, tools, state and error.
func (s *Store) SaveAgent(ctx context.Context, id, body string, tools []Tool, state, errText, runtimeTurn string, now time.Time) error {
	if tools == nil {
		tools = []Tool{}
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	return s.DB.Writer.Submit(ctx, "app_agent_save", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE app_agent_messages SET body=?,tools=?,state=?,error=?,runtime_turn=CASE WHEN ?<>'' THEN ? ELSE runtime_turn END,updated_ns=? WHERE id=?`,
			body, string(raw), state, errText, runtimeTurn, runtimeTurn, ns(now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Messages returns up to limit messages older than beforeSeq (0 = newest), oldest first.
func (s *Store) Messages(ctx context.Context, threadID string, beforeSeq int64, limit int) ([]Message, bool, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := "SELECT " + messageColumns + " FROM app_agent_messages WHERE thread_id=?"
	args := []any{threadID}
	if beforeSeq > 0 {
		q += " AND seq<?"
		args = append(args, beforeSeq)
	}
	q += " ORDER BY seq DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.DB.Reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, more, nil
}

// Streaming lists every agent message still in progress, across threads.
func (s *Store) Streaming(ctx context.Context) ([]Message, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT "+messageColumns+" FROM app_agent_messages WHERE state='streaming' ORDER BY thread_id,seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkRead records that the operator has seen everything up to the newest message.
func (s *Store) MarkRead(ctx context.Context, threadID string) error {
	return s.DB.Writer.Submit(ctx, "app_agent_read", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE app_agent_threads SET read_seq=(SELECT COALESCE(MAX(seq),0) FROM app_agent_messages WHERE thread_id=?) WHERE id=?`, threadID, threadID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ThreadsWithText returns the ids of threads having a message body that contains
// text, case-insensitively. At most limit matches are returned.
func (s *Store) ThreadsWithText(ctx context.Context, text string, limit int) (map[string]bool, error) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT DISTINCT thread_id FROM app_agent_messages WHERE body LIKE ? ESCAPE '\' LIMIT ?`, "%"+esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

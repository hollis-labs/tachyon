// Package chatthreads serves the chat thread list, thread overlay (pin, archive,
// title, project) and the per-thread event stream for both thread sources.
package chatthreads

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// keepEvents bounds the replay log per thread. A client further behind than this
// must refetch state, as it must after any reconnect that misses live signals.
const keepEvents = 500

// Frame is one SSE frame. ID is zero for live-only signals.
type Frame struct {
	ID   int64
	Data []byte
}

// Sub is a live subscription. A subscriber that cannot keep up is dropped: C is
// closed and the client reconnects and refetches.
type Sub struct {
	ref string // "" = global spine
	C   chan Frame
}

// Hub publishes thread events. Activity-class events (chatapi.IDed) are logged first,
// and the log's seq is the SSE id, so Last-Event-ID can replay them.
type Hub struct {
	DB  *localstore.Store
	Now func() time.Time

	mu   sync.Mutex
	subs map[*Sub]struct{}
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

// Subscribe registers for one thread's events, or for the global spine (ref "").
// The global spine receives only `thread` events.
func (h *Hub) Subscribe(ref string) *Sub {
	s := &Sub{ref: ref, C: make(chan Frame, 256)}
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[*Sub]struct{}{}
	}
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.C)
	}
	h.mu.Unlock()
}

// Publish sends {type, thread_ref, at, ...fields}. It returns the event's id (0 when
// the type is live-only).
func (h *Hub) Publish(ctx context.Context, ref, typ string, fields map[string]any) (int64, error) {
	at := h.now()
	env := map[string]any{}
	for k, v := range fields {
		env[k] = v
	}
	env["type"], env["thread_ref"], env["at"] = typ, ref, at.UnixMilli()
	body, err := json.Marshal(env)
	if err != nil {
		return 0, err
	}
	var seq int64
	if chatapi.IDed(typ) {
		err = h.DB.Writer.Submit(ctx, "chat_event", func(ctx context.Context, tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `INSERT INTO chat_events(thread_ref,type,body,at_ns) VALUES(?,?,?,?)`, ref, typ, body, at.UnixNano())
			if err != nil {
				return err
			}
			if seq, err = res.LastInsertId(); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `DELETE FROM chat_events WHERE thread_ref=? AND seq<=? - ?`, ref, seq, keepEvents)
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.ref != ref && !(s.ref == "" && typ == chatapi.EventThread) {
			continue
		}
		select {
		case s.C <- Frame{ID: seq, Data: body}:
		default:
			delete(h.subs, s)
			close(s.C)
		}
	}
	return seq, nil
}

// Replay returns the logged events of one thread after the given id, oldest first.
func (h *Hub) Replay(ctx context.Context, ref string, after int64) ([]Frame, error) {
	rows, err := h.DB.Reader.QueryContext(ctx, `SELECT seq,body FROM chat_events WHERE thread_ref=? AND seq>? ORDER BY seq LIMIT 1000`, ref, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Frame
	for rows.Next() {
		var f Frame
		if err := rows.Scan(&f.ID, &f.Data); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

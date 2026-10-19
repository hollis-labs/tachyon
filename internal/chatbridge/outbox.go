package chatbridge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

type Sent struct {
	Key            string `json:"key"`
	LaunchID       string `json:"launch_id"`
	Body           string `json:"body"`
	At             int64  `json:"at"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	LocalSessionID string `json:"local_session_id,omitempty"`
}
type Outbox struct {
	Store    *localstore.Store
	SourceID string
	Identity func() (string, error)
	Next     http.Handler
	mu       sync.Mutex
}

const ChatPrefix = "/api/plugins/console/v1/chat"

func (o *Outbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, err := o.Identity()
	if err != nil {
		fail(w, 503, "configuration", "Tether chat credential is unavailable")
		return
	}
	if r.Method == "GET" && r.URL.Path == ChatPrefix+"/outbox" {
		o.list(w, r, identity)
		return
	}
	launch := r.PathValue("launch")
	if launch == "" || r.Method != "POST" {
		fail(w, 404, "not_found", "Unknown chat outbox operation")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	var in struct {
		Text string `json:"text"`
		Key  string `json:"idempotency_key"`
	}
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.Text) == "" || in.Key == "" || len(in.Key) > 200 {
		fail(w, 400, "invalid_args", "Message text and an idempotency key are required")
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var old Sent
	e := o.Store.Reader.QueryRowContext(r.Context(), "SELECT launch_id,body,created_at,status,error,session_id FROM chat_outbox WHERE identity=? AND id=?", identity, in.Key).Scan(&old.LaunchID, &old.Body, &old.At, &old.Status, &old.Error, &old.SessionID)
	if e != nil && e != sql.ErrNoRows {
		fail(w, 503, "storage", "Could not read the chat outbox")
		return
	}
	if e == nil {
		if old.LaunchID != launch || old.Body != in.Text {
			fail(w, 409, "conflict", "This message key already belongs to different text")
			return
		}
		if old.Status == "sent" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": old.SessionID, "replayed": true})
			return
		}
		if time.Since(time.UnixMilli(old.At)) >= 24*time.Hour {
			fail(w, 409, "delivery_unknown", "This unconfirmed send is too old to retry safely; check the conversation first")
			return
		}
	}
	now := time.Now().UnixMilli()
	if old.At != 0 {
		now = old.At
	}
	err = o.Store.Writer.Submit(r.Context(), "chat_outbox_send", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO chat_outbox(identity,id,launch_id,body,created_at,status,error,session_id) VALUES(?,?,?,?,?,'sending','','') ON CONFLICT(identity,id) DO UPDATE SET status='sending',error=''`, identity, in.Key, launch, in.Text, now)
		return e
	})
	if err != nil {
		fail(w, 503, "storage", "Could not save the outgoing message; nothing was sent")
		return
	}
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(raw))
	capture := &response{header: make(http.Header), status: 200}
	o.Next.ServeHTTP(capture, req)
	status := "failed"
	detail := ""
	session := ""
	var result struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
		Code      string `json:"code"`
		Error     struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(capture.body.Bytes(), &result)
	if capture.status >= 200 && capture.status < 300 {
		status = "sent"
		session = result.SessionID
	} else {
		detail = result.Message
		if detail == "" {
			detail = result.Error.Message
		}
		if detail == "" {
			detail = "Send was not confirmed. Check the connection and retry with the same message key."
		}
	}
	// A disconnected browser must not prevent the upstream result being recorded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	err = o.Store.Writer.Submit(ctx, "chat_outbox_result", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE chat_outbox SET status=?,error=?,session_id=? WHERE identity=? AND id=?", status, detail, session, identity, in.Key)
		if e != nil {
			return e
		}
		return linkOutboxSession(ctx, tx, o.SourceID, identity, in.Key)
	})
	if err != nil {
		capture.header.Set("X-Tachyon-Outbox-Error", "storage")
	}
	for k, vs := range capture.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(capture.status)
	_, _ = w.Write(capture.body.Bytes())
}
func (o *Outbox) list(w http.ResponseWriter, r *http.Request, identity string) {
	rows, err := o.Store.Reader.QueryContext(r.Context(), "SELECT t.id,t.launch_id,t.body,t.created_at,t.status,t.error,t.session_id,COALESCE(l.session_object_id,'') FROM chat_outbox t LEFT JOIN chat_outbox_session_links l ON l.identity=t.identity AND l.id=t.id WHERE t.identity=? AND t.launch_id=? ORDER BY t.created_at DESC LIMIT 500", identity, r.URL.Query().Get("launch_id"))
	if err != nil {
		fail(w, 503, "storage", "Could not load outgoing chat history")
		return
	}
	defer rows.Close()
	out := []Sent{}
	for rows.Next() {
		var s Sent
		if err = rows.Scan(&s.Key, &s.LaunchID, &s.Body, &s.At, &s.Status, &s.Error, &s.SessionID, &s.LocalSessionID); err != nil {
			fail(w, 503, "storage", "Could not read outgoing chat history")
			return
		}
		out = append(out, s)
	}
	if rows.Err() != nil {
		fail(w, 503, "storage", "Could not read outgoing chat history")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type response struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *response) Header() http.Header         { return r.header }
func (r *response) WriteHeader(s int)           { r.status = s }
func (r *response) Write(b []byte) (int, error) { return r.body.Write(b) }

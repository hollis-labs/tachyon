package chatbridge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The send side of the chat outbox (slice B6c): POST <chat prefix>/agents/{launch}/send, the old
// build's Outbox in front of the console plugin's Tether proxy. A send is recorded as 'sending',
// forwarded to Tether's /chat/agents/{launch}/send with the caller's idempotency key, and its result
// recorded. There is no background delivery: a row that is not 'sent' is retried only when the
// operator retries with the same key (within 24 hours), and Tether receives the same key again.

// SendPath is the send route under the chat prefix.
const SendPath = "/agents/{launch}/send"

var launchID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Sender serves sends for one outbox identity (OutboxIdentity of the Tether address).
type Sender struct {
	DB       *store.Store
	Identity string // the owner key of the rows
	SourceID string // the Tether address, for the session links
	// Enabled is tachyon.features.chat_sync: off, a send is refused before anything is recorded.
	Enabled bool
	post    func(ctx context.Context, launch string, body []byte) (int, http.Header, []byte, error)
	mu      sync.Mutex
}

// NewSender is the sender over the Tether at addr. With no address, or with enabled off, every send
// is refused and nothing is dialed.
func NewSender(db *store.Store, addr string, token upstreamauth.Token, enabled bool) *Sender {
	s := &Sender{DB: db, Identity: OutboxIdentity(addr), SourceID: addr, Enabled: enabled}
	if strings.TrimSpace(addr) == "" {
		return s
	}
	src, err := NewSource(addr, token)
	if err != nil {
		return s
	}
	base := src.(*source)
	// The old console plugin gave a send 25 s.
	client := &http.Client{Transport: base.http.Transport, Timeout: 25 * time.Second, CheckRedirect: base.http.CheckRedirect}
	s.post = func(ctx context.Context, launch string, body []byte) (int, http.Header, []byte, error) {
		// base.base is the operator-configured Tether address; launch is checked against launchID.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.base+"/chat/agents/"+launch+"/send", bytes.NewReader(body)) //nolint:gosec // see above
		if err != nil {
			return 0, nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req) //nolint:gosec // the configured Tether address
		if err != nil {
			return 0, nil, nil, err
		}
		defer func() { _ = res.Body.Close() }()
		out, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		return res.StatusCode, res.Header, out, err
	}
	return s
}

// ServeHTTP sends one message, as the old build's Outbox did.
func (s *Sender) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	launch := r.PathValue("launch")
	if !launchID.MatchString(launch) {
		fail(w, http.StatusNotFound, "not_found", "Unknown chat operation")
		return
	}
	if !s.Enabled {
		fail(w, http.StatusServiceUnavailable, "disabled", "Sending to agents is off (tachyon.features.chat_sync); nothing was sent")
		return
	}
	if s.post == nil {
		fail(w, http.StatusServiceUnavailable, "configuration", "Tether chat connection or credential is not configured")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	var in struct {
		Text string `json:"text"`
		Key  string `json:"idempotency_key"`
	}
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.Text) == "" || in.Key == "" || len(in.Key) > 200 {
		fail(w, http.StatusBadRequest, "invalid_args", "Message text and an idempotency key are required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var old Sent
	e := s.DB.Reader.QueryRowContext(r.Context(), "SELECT launch_id,body,created_at,status,error,session_id FROM chat_outbox WHERE identity=? AND id=?", s.Identity, in.Key).Scan(&old.LaunchID, &old.Body, &old.At, &old.Status, &old.Error, &old.SessionID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		fail(w, http.StatusServiceUnavailable, "storage", "Could not read the chat outbox")
		return
	}
	if e == nil {
		if old.LaunchID != launch || old.Body != in.Text {
			fail(w, http.StatusConflict, "conflict", "This message key already belongs to different text")
			return
		}
		if old.Status == "sent" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": old.SessionID, "replayed": true})
			return
		}
		if time.Since(time.UnixMilli(old.At)) >= 24*time.Hour {
			fail(w, http.StatusConflict, "delivery_unknown", "This unconfirmed send is too old to retry safely; check the conversation first")
			return
		}
	}
	now := time.Now().UnixMilli()
	if old.At != 0 {
		now = old.At
	}
	err = s.DB.Writer.Submit(r.Context(), "chat_outbox_send", func(ctx context.Context, tx store.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO chat_outbox(identity,id,launch_id,body,created_at,status,error,session_id) VALUES(?,?,?,?,?,'sending','','') ON CONFLICT(identity,id) DO UPDATE SET status='sending',error=''`, s.Identity, in.Key, launch, in.Text, now)
		return e
	})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "storage", "Could not save the outgoing message; nothing was sent")
		return
	}
	code, header, body, err := s.post(r.Context(), launch, raw)
	status, detail, session := "failed", "", ""
	var result struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
		Error     struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err != nil {
		code, header, body = http.StatusBadGateway, http.Header{"Content-Type": {"application/json"}}, mustJSON(map[string]string{"code": "upstream_unavailable", "message": "Tether chat send unavailable"})
	}
	_ = json.Unmarshal(body, &result)
	if code >= 200 && code < 300 {
		status, session = "sent", result.SessionID
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
	err = s.DB.Writer.Submit(ctx, "chat_outbox_result", func(ctx context.Context, tx store.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE chat_outbox SET status=?,error=?,session_id=? WHERE identity=? AND id=?", status, detail, session, s.Identity, in.Key); e != nil {
			return e
		}
		return linkOutboxSession(ctx, tx, s.SourceID, s.Identity, in.Key)
	})
	if err != nil {
		w.Header().Set("X-Tachyon-Outbox-Error", "storage")
	}
	if ct := header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

package chatbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/tachyon/next/internal/store"
)

// The read side of the chat outbox: the messages the operator sent to an agent launch, as the old
// build listed them (GET <chat prefix>/outbox?launch_id=). Sending, and the outbox delivery behind
// it, are outbound delivery (slice B6c).

// OutboxPath is the outbox list route under the chat prefix.
const OutboxPath = "/outbox"

// Sent is one outgoing message, the old build's JSON shape.
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

// OutboxIdentity is the owner key of chat outbox rows: the old build's digest of the Tether address
// (apicache.Digest("chat-owner\n" + address)). Like the turns' source, it depends on the address being
// spelled as the old build spelled it.
func OutboxIdentity(tetherAddr string) string {
	sum := sha256.Sum256([]byte("chat-owner\n" + tetherAddr))
	return hex.EncodeToString(sum[:])
}

// OutboxList serves the outbox list for one launch: a bare array, newest first, at most 500, only the
// rows owned by identity.
func OutboxList(db *store.Store, identity string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		rows, err := db.Reader.QueryContext(r.Context(), "SELECT t.id,t.launch_id,t.body,t.created_at,t.status,t.error,t.session_id,COALESCE(l.session_object_id,'') FROM chat_outbox t LEFT JOIN chat_outbox_session_links l ON l.identity=t.identity AND l.id=t.id WHERE t.identity=? AND t.launch_id=? ORDER BY t.created_at DESC LIMIT 500", identity, r.URL.Query().Get("launch_id"))
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "storage", "Could not load outgoing chat history")
			return
		}
		defer func() { _ = rows.Close() }()
		out := []Sent{}
		for rows.Next() {
			var s Sent
			if err = rows.Scan(&s.Key, &s.LaunchID, &s.Body, &s.At, &s.Status, &s.Error, &s.SessionID, &s.LocalSessionID); err != nil {
				fail(w, http.StatusServiceUnavailable, "storage", "Could not read outgoing chat history")
				return
			}
			out = append(out, s)
		}
		if rows.Err() != nil {
			fail(w, http.StatusServiceUnavailable, "storage", "Could not read outgoing chat history")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

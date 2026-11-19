package chatbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
)

var ErrConflict = errors.New("conversation changed; refresh before replying")
var ErrNotReplyable = errors.New("this message cannot receive a reply in the current session")

func (o *Owned) Reply(ctx context.Context, id string, revision int64, text string) (Turn, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, err := o.get(ctx, id)
	if err != nil {
		return t, err
	}
	if strings.TrimSpace(text) == "" || len(text) > 65536 {
		return t, errors.New("message text is empty or too large")
	}
	var key, oldBody, receipt, state string
	err = o.Store.Reader.QueryRowContext(ctx, "SELECT request_key,body,reply_id,state FROM chat_reply_intents WHERE turn_id=?", id).Scan(&key, &oldBody, &receipt, &state)
	if err != nil && err != sql.ErrNoRows {
		return t, err
	}
	if err == nil {
		if oldBody != text {
			return t, ErrConflict
		}
		if receipt != "" {
			return t, nil
		} // accepted already; never manufacture another send
	} else {
		if t.Revision != revision || t.Resolution != nil {
			return t, ErrConflict
		}
		key = "tachyon-reply-" + uuid.NewString()
	}
	if !t.Replyable || t.Source == nil || t.Source.Origin != "routed" || t.Source.MessageID == "" || t.Source.Sender != "msg://session/local/"+t.SessionID || !(t.Kind == "terminal" || t.Kind == "question" || t.Kind == "failure") {
		return t, ErrNotReplyable
	}
	if o.Source == nil {
		return t, errors.New("configuration: Tether connection unavailable")
	}
	agents, err := o.Source.Agents(ctx)
	if err != nil {
		return t, err
	}
	current := false
	for _, a := range agents {
		if a.LaunchID == t.Source.Attribution["launch_id"] && a.Session != nil && a.Session.ID == t.SessionID && a.Session.State == "running" {
			current = true
		}
	}
	if !current {
		return t, ErrNotReplyable
	}
	caps, err := o.Source.RoutingCapabilities(ctx, t.SessionID)
	if err != nil {
		return t, err
	}
	if caps.SessionID != t.SessionID || !caps.ReplyToSender {
		return t, ErrNotReplyable
	}
	// Save intent and its immutable key before crossing the network. An interrupted
	// attempt remains retryable with that key; only an observed receipt is accepted.
	if t.Resolution == nil {
		t.Resolution = &Resolution{ID: key, Text: text, At: time.Now().UTC().Format(time.RFC3339Nano)}
		t.State = "resolved"
		t.Delivery = "retryable_failure"
		t.Revision++
		err = o.Store.Writer.Submit(ctx, "chat_reply_prepare", func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "INSERT INTO chat_reply_intents(turn_id,request_key,body,state,created_at) VALUES(?,?,?,'prepared',?)", id, key, text, t.Resolution.At)
			if e != nil {
				return e
			}
			return o.updateTurn(ctx, tx, t)
		})
		if err != nil {
			return t, err
		}
	}
	received, sendErr := o.Source.Reply(ctx, t.Source.MessageID, text, tether.ReplyOptions{IdempotencyKey: key})
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	state = "failed"
	detail := ""
	receipt = ""
	if sendErr == nil {
		if received.ParentID != t.Source.MessageID || received.TargetSessionID != t.SessionID || received.ReplyID == "" {
			sendErr = errors.New("invalid_response: reply receipt does not match the original session")
		} else {
			receipt = received.ReplyID
			state = received.State
			t.Delivery = delivery(state)
		}
	}
	if sendErr != nil {
		detail = problem(sendErr)
		t.Delivery = "retryable_failure"
	}
	t.Revision++
	err = o.Store.Writer.Submit(persist, "chat_reply_result", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE chat_reply_intents SET reply_id=?,state=?,error=? WHERE turn_id=?", receipt, state, detail, id)
		if e != nil {
			return e
		}
		return o.updateTurn(ctx, tx, t)
	})
	return t, errors.Join(sendErr, err)
}
func delivery(state string) string {
	switch state {
	case "delivered":
		return "delivered"
	case "undeliverable":
		return "terminal_failure"
	default:
		return "queued"
	}
}
func (o *Owned) pollDeliveries(ctx context.Context) error {
	rows, err := o.Store.Reader.QueryContext(ctx, `SELECT i.turn_id,i.reply_id,i.state FROM chat_reply_intents i JOIN chat_turns t ON t.id=i.turn_id WHERE t.source=? AND i.reply_id<>'' AND i.state NOT IN ('delivered','undeliverable') ORDER BY i.created_at LIMIT 100`, o.SourceID)
	if err != nil {
		return err
	}
	type pending struct{ id, reply, state string }
	var pendingItems []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.reply, &p.state); err != nil {
			rows.Close()
			return err
		}
		pendingItems = append(pendingItems, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pendingItems {
		d, e := o.Source.ReplyDelivery(ctx, p.reply)
		if e != nil {
			return e
		}
		t, e := o.get(ctx, p.id)
		if e != nil {
			return e
		}
		if t.Source == nil || d.ReplyID != p.reply || d.ParentID != t.Source.MessageID || d.OriginalSessionID != t.SessionID {
			return fmt.Errorf("invalid_response: reply delivery identity")
		}
		if p.state == string(d.State) {
			continue
		}
		t.Delivery = delivery(string(d.State))
		t.Revision++
		e = o.Store.Writer.Submit(ctx, "chat_reply_observed", func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "UPDATE chat_reply_intents SET state=?,error=? WHERE turn_id=?", string(d.State), d.Reason, p.id)
			if e != nil {
				return e
			}
			return o.updateTurn(ctx, tx, t)
		})
		if e != nil {
			return e
		}
		if o.Changed != nil {
			o.Changed(ctx, "delivery", t)
		}
	}
	return nil
}

// Import preserves complete historical records and their source IDs. Repeated
// imports match existing publications and never requeue an operator reply.
func (o *Owned) Import(ctx context.Context, raw []byte) (int, error) {
	var in Inbox
	if err := json.Unmarshal(raw, &in); err != nil {
		return 0, err
	}
	if in.Pending == nil || in.History == nil {
		return 0, errors.New("invalid history snapshot")
	}
	items := append(in.Pending, in.History...)
	seen := map[string]bool{}
	for _, t := range items {
		if t.ID == "" || t.Revision < 1 || seen[t.ID] {
			return 0, errors.New("invalid or duplicate imported turn")
		}
		seen[t.ID] = true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	count := 0
	err := o.Store.Writer.Submit(ctx, "chat_import_tangent", func(ctx context.Context, tx *sql.Tx) error {
		for _, t := range items {
			// If direct ingestion already saw this publication, attach the historic
			// resolution and trace without replacing a Tachyon-owned reply.
			if t.Source != nil && t.Source.MessageID != "" {
				var id string
				var existing []byte
				e := tx.QueryRowContext(ctx, "SELECT id,body FROM chat_turns WHERE source=? AND channel=? AND publication_id=?", o.SourceID, t.Source.Channel, t.Source.MessageID).Scan(&id, &existing)
				if e == nil {
					var old Turn
					if e = json.Unmarshal(existing, &old); e != nil {
						return e
					}
					if old.Resolution == nil && t.Resolution != nil {
						old.Resolution = t.Resolution
						old.Delivery = t.Delivery
						old.State = t.State
						old.Revision++
					}
					if len(old.Trace) == 0 {
						old.Trace = t.Trace
					}
					b, e := json.Marshal(old)
					if e != nil {
						return e
					}
					_, e = tx.ExecContext(ctx, "UPDATE chat_turns SET body=?,revision=?,imported=1 WHERE id=?", b, old.Revision, id)
					if e != nil {
						return e
					}
					if e = linkTurnSession(ctx, tx, o.SourceID, id); e != nil {
						return e
					}
					count++
					continue
				}
				if e != sql.ErrNoRows {
					return e
				}
			}
			if e := o.saveTurn(ctx, tx, t, true); e != nil {
				return e
			}
			count++
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO chat_imports(source,checksum,imported_at,turn_count,snapshot) VALUES(?,?,?,?,?) ON CONFLICT(source) DO UPDATE SET checksum=excluded.checksum,imported_at=excluded.imported_at,turn_count=excluded.turn_count,snapshot=excluded.snapshot`, o.SourceID, digest(string(raw)), time.Now().UTC().Format(time.RFC3339Nano), count, raw)
		return e
	})
	return count, err
}

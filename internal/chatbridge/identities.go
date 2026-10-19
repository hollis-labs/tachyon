package chatbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

// Keep the transport's original source/session strings untouched. The object
// key uses the same address normalization as Tether inventory adapters.
func sessionKey(source, id string) objects.SourceKey {
	instance := strings.TrimSpace(source)
	if !strings.HasPrefix(instance, "unix:") {
		instance = strings.TrimRight(instance, "/")
		if rest, ok := strings.CutPrefix(instance, "tcp:"); ok {
			instance = "http://" + rest
		}
	}
	return objects.SourceKey{SourceScope: objects.SourceScope{Provider: "tether", Instance: instance, Collection: "sessions", Kind: objects.SessionKind}, ExternalID: id}
}
func linkTurnSession(ctx context.Context, tx *sql.Tx, source, id string) error {
	var external string
	err := tx.QueryRowContext(ctx, `SELECT session_id FROM chat_turns WHERE source=? AND id=?`, source, id).Scan(&external)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if external == "" {
		return nil
	}
	r, err := objects.EnsureSessionIdentityTx(ctx, tx, sessionKey(source, external), "chat_identity")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_session_links(turn_id,session_object_id,linked_at) VALUES(?,?,?) ON CONFLICT(turn_id) DO NOTHING`, id, r.ID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func linkOutboxSession(ctx context.Context, tx *sql.Tx, source, identity, id string) error {
	if source == "" {
		return nil
	}
	var external string
	err := tx.QueryRowContext(ctx, `SELECT session_id FROM chat_outbox WHERE identity=? AND id=?`, identity, id).Scan(&external)
	if err != nil {
		return err
	}
	if external == "" {
		return nil
	}
	r, err := objects.EnsureSessionIdentityTx(ctx, tx, sessionKey(source, external), "chat_identity")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_outbox_session_links(identity,id,session_object_id,linked_at) VALUES(?,?,?,?) ON CONFLICT(identity,id) DO NOTHING`, identity, id, r.ID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// BackfillSessionLinks adds associations in bounded queued batches without
// changing any original turn, revision, reply intent, cursor or outbox row.
// Only the explicitly selected source and owned outbox identity are considered.
func (o *Owned) BackfillSessionLinks(ctx context.Context, outboxIdentity string) (int, error) {
	if strings.TrimSpace(o.SourceID) == "" {
		return 0, nil
	}
	count := 0
	for _, outbox := range []bool{false, true} {
		if outbox && outboxIdentity == "" {
			continue
		}
		for {
			batch := 0
			err := o.Store.Writer.Submit(ctx, "chat_identity_backfill", func(ctx context.Context, tx *sql.Tx) error {
				query := `SELECT t.id FROM chat_turns t LEFT JOIN chat_session_links l ON l.turn_id=t.id WHERE t.source=? AND t.session_id<>'' AND l.turn_id IS NULL ORDER BY t.id LIMIT 200`
				key := o.SourceID
				if outbox {
					query = `SELECT t.id FROM chat_outbox t LEFT JOIN chat_outbox_session_links l ON l.identity=t.identity AND l.id=t.id WHERE t.identity=? AND t.session_id<>'' AND l.id IS NULL ORDER BY t.id LIMIT 200`
					key = outboxIdentity
				}
				rows, err := tx.QueryContext(ctx, query, key)
				if err != nil {
					return err
				}
				var ids []string
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						rows.Close()
						return err
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				for _, id := range ids {
					if outbox {
						err = linkOutboxSession(ctx, tx, o.SourceID, outboxIdentity, id)
					} else {
						err = linkTurnSession(ctx, tx, o.SourceID, id)
					}
					if err != nil {
						return err
					}
				}
				batch = len(ids)
				return nil
			})
			if err != nil {
				return count, err
			}
			count += batch
			if batch < 200 {
				break
			}
		}
	}
	return count, nil
}

// SessionTurns lists owned history by local session UUID, including history of
// deleted sessions. It is scoped to this transport instance and keyset paginated
// by original turn ID. LocalSessionID is a read projection, never stored in body.
func (o *Owned) SessionTurns(ctx context.Context, localID, afterID string, limit int) ([]Turn, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return nil, objects.ErrInvalid
	}
	rows, err := o.Store.Reader.QueryContext(ctx, `SELECT t.body,l.session_object_id FROM chat_turns t JOIN chat_session_links l ON l.turn_id=t.id WHERE t.source=? AND l.session_object_id=? AND t.id>? ORDER BY t.id LIMIT ?`, o.SourceID, localID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Turn{}
	for rows.Next() {
		t, err := scanLinkedTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func scanLinkedTurn(row interface{ Scan(...any) error }) (Turn, error) {
	var t Turn
	var raw []byte
	var local string
	if err := row.Scan(&raw, &local); err != nil {
		return t, err
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, err
	}
	t.LocalSessionID = local
	return t, nil
}

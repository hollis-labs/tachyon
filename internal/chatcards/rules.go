package chatcards

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
)

// Rule is the chatapi ApprovalRule: "Always in this chat" for one tool. A rule only allows.
type Rule struct {
	ID        string         `json:"id"`
	ThreadRef string         `json:"thread_ref"`
	AgentID   string         `json:"agent_id"`
	Tool      string         `json:"tool"`
	Match     map[string]any `json:"match"`
	Effect    string         `json:"effect"`
	ExpiresAt *int64         `json:"expires_at"`
	CreatedAt int64          `json:"created_at"`
	RevokedAt *int64         `json:"revoked_at"`
}

// ErrNotLow is returned when a rule is asked for anything above low class: those always go to the operator.
var ErrNotLow = errors.New("only low-class approvals can be allowed for the chat")

// AddRule records an allow rule. class is the class of the approval the operator just answered; anything
// but low is refused. match is compared key by key to a request's arguments (an empty match covers the tool).
func (s *Store) AddRule(ctx context.Context, thread, agent, tool, class string, match map[string]any, expiresMS *int64) (Rule, error) {
	if class != "low" {
		return Rule{}, ErrNotLow
	}
	if tool == "" {
		return Rule{}, errors.New("tool is required")
	}
	if match == nil {
		match = map[string]any{}
	}
	mj, _ := json.Marshal(match)
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	r := Rule{ID: "rul_" + hex.EncodeToString(b), ThreadRef: thread, AgentID: agent, Tool: tool, Match: match, Effect: "allow", ExpiresAt: expiresMS, CreatedAt: s.now().UnixMilli()}
	var exp any
	if expiresMS != nil {
		exp = *expiresMS * 1_000_000
	}
	err := s.DB.Writer.Submit(ctx, "chat_rule_add", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_approval_rules(id, thread_ref, agent_id, tool, match_json, expires_ns, created_ns) VALUES (?,?,?,?,?,?,?)`,
			r.ID, thread, agent, tool, string(mj), exp, r.CreatedAt*1_000_000)
		return err
	})
	return r, err
}

// Rules lists the thread's rules that are still in force.
func (s *Store) Rules(ctx context.Context, thread string) ([]Rule, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT id, thread_ref, agent_id, tool, match_json, expires_ns, created_ns FROM chat_approval_rules
 WHERE thread_ref = ? AND revoked_ns IS NULL AND (expires_ns IS NULL OR expires_ns > ?) ORDER BY created_ns`, thread, s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		var mj string
		var exp sql.NullInt64
		var created int64
		if err := rows.Scan(&r.ID, &r.ThreadRef, &r.AgentID, &r.Tool, &mj, &exp, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(mj), &r.Match)
		if r.Match == nil {
			r.Match = map[string]any{}
		}
		r.Effect, r.CreatedAt = "allow", created/1_000_000
		if exp.Valid {
			ms := exp.Int64 / 1_000_000
			r.ExpiresAt = &ms
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revoke ends a rule. It reports false when the thread has no such rule in force.
func (s *Store) Revoke(ctx context.Context, thread, id string) (bool, error) {
	var n int64
	err := s.DB.Writer.Submit(ctx, "chat_rule_revoke", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE chat_approval_rules SET revoked_ns = ? WHERE id = ? AND thread_ref = ? AND revoked_ns IS NULL`, s.now().UnixNano(), id, thread)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n > 0, err
}

// Allows reports whether a rule in force lets the agent's tool call run without asking. Anything that is not
// low class is never allowed here.
func (s *Store) Allows(ctx context.Context, thread, agent, tool, class string, args map[string]any) bool {
	if class != "low" {
		return false
	}
	rules, err := s.Rules(ctx, thread)
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.Tool != tool || (r.AgentID != "" && r.AgentID != agent) {
			continue
		}
		ok := true
		for k, want := range r.Match {
			if got, has := args[k]; !has || !reflect.DeepEqual(got, want) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// RuleRoutes are mounted explicitly under chatapi.Prefix.
var RuleRoutes = []string{"GET /threads/{ref}/approval-rules", "DELETE /threads/{ref}/approval-rules/{id}"}

func ruleHandlers(m *http.ServeMux, prefix string, s *Store, exists func(context.Context, string) error) {
	m.HandleFunc("GET "+prefix+"/threads/{ref}/approval-rules", func(w http.ResponseWriter, r *http.Request) {
		thread, ok := threadOf(w, r, exists)
		if !ok {
			return
		}
		rules, err := s.Rules(r.Context(), thread)
		if err != nil {
			reply(w, 500, map[string]string{"code": "internal", "message": "The rules could not be read."})
			return
		}
		reply(w, 200, map[string]any{"rules": rules})
	})
	m.HandleFunc("DELETE "+prefix+"/threads/{ref}/approval-rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		thread, ok := threadOf(w, r, exists)
		if !ok {
			return
		}
		found, err := s.Revoke(r.Context(), thread, r.PathValue("id"))
		switch {
		case err != nil:
			reply(w, 500, map[string]string{"code": "internal", "message": "The rule could not be removed."})
		case !found:
			reply(w, 404, map[string]string{"code": "not_found", "message": "That rule was not found."})
		default:
			reply(w, 200, map[string]any{"ok": true})
		}
	})
}

// Package forgeaudit records the forge writes (merge, review, assign, comment) in forge_audit and serves
// GET /audit. Rows carry the PR, the actor reference, the outcome and GitHub's reference; never a body.
package forgeaudit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const retention = 90 * 24 * time.Hour

// Log writes and reads forge_audit.
type Log struct {
	DB        *localstore.Store
	Principal func(*http.Request) string
	now       func() time.Time
}

func (l *Log) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

type capture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capture) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *capture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.body.Len() < 16<<10 {
		c.body.Write(p)
	}
	return c.ResponseWriter.Write(p)
}

func (c *capture) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Wrap records one row per call of next after it answers. Mount it inside the operator guard, so only
// authorised attempts are recorded; the route pattern must carry {owner}, {name} and {num}.
func (l *Log) Wrap(op string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &capture{ResponseWriter: w}
		next.ServeHTTP(c, r)
		if c.status == 0 {
			c.status = http.StatusOK
		}
		l.record(r, op, c.status, c.body.Bytes())
	})
}

func (l *Log) record(r *http.Request, op string, status int, body []byte) {
	if l == nil || l.DB == nil {
		return
	}
	prKey := "github:" + r.PathValue("owner") + "/" + r.PathValue("name") + "#" + r.PathValue("num")
	outcome, code := "ok", ""
	var ref string
	var reply map[string]any
	_ = json.Unmarshal(body, &reply)
	if status >= 400 {
		outcome = "refused"
		if status >= 500 {
			outcome = "failed"
		}
		code = errorCode(reply)
	}
	for _, k := range []string{"sha", "id", "merge_sha"} {
		switch v := reply[k].(type) {
		case string:
			ref = v
		case float64:
			ref = strconv.FormatInt(int64(v), 10)
		}
		if ref != "" {
			break
		}
	}
	var class any
	if op == "merge" {
		class = "lifecycle"
	}
	actor := ""
	if l.Principal != nil {
		actor = l.Principal(r)
	}
	err := l.DB.Writer.Submit(r.Context(), "forge_audit", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO forge_audit(ts_ms,op,pr_key,actor_ref,assurance,outcome,error_code,github_ref,audit_class) VALUES(?,?,?,?,?,?,?,?,?)`,
			l.clock().UnixMilli(), op, clip(prKey, 200), clip(actor, 128), "browser-session", outcome, nullIf(clip(code, 80)), nullIf(clip(ref, 80)), class)
		return err
	})
	if err != nil {
		slog.Warn("forge audit row not written", "op", op, "err", err)
	}
}

func errorCode(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	if c, ok := m["code"].(string); ok {
		return c
	}
	return "error"
}

func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Handler serves GET /audit: newest first, ?limit 1..200 (default 50). Reading it is an operator act, so mount
// it behind RequireOperatorReads.
func (l *Log) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 200 {
				reply(w, 400, map[string]any{"error": map[string]string{"code": "invalid_args", "message": "limit is 1 to 200."}})
				return
			}
			limit = n
		}
		rows, err := l.DB.Reader.QueryContext(r.Context(), `SELECT id,ts_ms,op,pr_key,actor_ref,assurance,outcome,error_code,github_ref,head_sha,audit_class FROM forge_audit ORDER BY id DESC LIMIT ?`, limit)
		if err != nil {
			reply(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "Audit could not be read."}})
			return
		}
		defer rows.Close()
		entries := []map[string]any{}
		for rows.Next() {
			var id, ts int64
			var op, pr, actor, assurance, outcome string
			var ec, gh, head, class sql.NullString
			if rows.Scan(&id, &ts, &op, &pr, &actor, &assurance, &outcome, &ec, &gh, &head, &class) != nil {
				continue
			}
			e := map[string]any{"id": id, "ts_ms": ts, "op": op, "pr_key": pr, "actor_ref": actor, "assurance": assurance, "outcome": outcome}
			for k, v := range map[string]sql.NullString{"error_code": ec, "github_ref": gh, "head_sha": head, "audit_class": class} {
				if v.Valid {
					e[k] = v.String
				}
			}
			entries = append(entries, e)
		}
		reply(w, 200, map[string]any{"entries": entries})
	})
}

// Prune drops rows past retention.
func (l *Log) Prune(ctx context.Context) {
	cut := l.clock().Add(-retention).UnixMilli()
	_ = l.DB.Writer.Submit(ctx, "forge_audit_prune", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM forge_audit WHERE ts_ms<?`, cut)
		return err
	})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

package fileview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Audit kinds, sources, outcomes and deny reasons the table accepts.
var (
	auditKinds   = []string{"stat", "open", "frame", "caster"}
	auditSources = []string{"host", "caster"}
	auditOutcome = []string{"ok", "denied", "error"}
	denyReasons  = []string{"outside_roots", "secret_pattern", "symlink_escape", "too_large", "not_found_or_hidden"}
)

// AuditEntry is one row. Times are unix ms. Nothing here is file content.
type AuditEntry struct {
	ID           int64   `json:"id,omitempty"`
	TSMS         int64   `json:"ts_ms"`
	Principal    string  `json:"principal"`
	Kind         string  `json:"kind"`
	Source       string  `json:"source"`
	PathOrRef    string  `json:"path_or_ref"`
	ResolvedRoot *string `json:"resolved_root"`
	Outcome      string  `json:"outcome"`
	DenyReason   *string `json:"deny_reason"`
	BytesServed  *int64  `json:"bytes_served"`
	ETag         *string `json:"etag"`
	// Count is the number of paths in a stat batch; 1 for everything else.
	Count int `json:"count"`
}

// Auditor writes and reads fileview_audit through the serialized writer.
type Auditor struct {
	DB  *localstore.Store
	Now func() time.Time

	dropped atomic.Int64
}

func (a *Auditor) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Dropped counts async records that failed or timed out (best-effort stat rows only).
func (a *Auditor) Dropped() int64 { return a.dropped.Load() }

func oneOf(s string, set []string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// cleanRef makes a path or caster ref safe to store: one line, no control characters, no query string or
// fragment on a caster ref, at most 1024 bytes.
func cleanRef(kind, s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if strings.HasPrefix(out, "caster://") {
		if i := strings.IndexAny(out, "?#"); i >= 0 {
			out = out[:i]
		}
	}
	if len(out) > 1024 {
		out = out[:1024]
	}
	return out
}

func normalize(e AuditEntry, now time.Time) (AuditEntry, error) {
	switch {
	case !oneOf(e.Kind, auditKinds), !oneOf(e.Source, auditSources), !oneOf(e.Outcome, auditOutcome):
		return e, errors.New("audit: kind, source and outcome must be known values")
	case e.DenyReason != nil && !oneOf(*e.DenyReason, denyReasons):
		return e, errors.New("audit: unknown deny_reason")
	case e.Outcome == "denied" && e.DenyReason == nil:
		return e, errors.New("audit: a denial needs a deny_reason")
	}
	if e.TSMS == 0 {
		e.TSMS = now.UnixMilli()
	}
	if e.Count < 1 {
		e.Count = 1
	}
	e.PathOrRef = cleanRef(e.Kind, e.PathOrRef)
	if len(e.Principal) > 128 {
		e.Principal = e.Principal[:128]
	}
	if e.ETag != nil && len(*e.ETag) > 128 {
		t := (*e.ETag)[:128]
		e.ETag = &t
	}
	return e, nil
}

// Record writes one row and waits for it. Use it for open, frame, caster and every denial.
func (a *Auditor) Record(ctx context.Context, e AuditEntry) error {
	e, err := normalize(e, a.now())
	if err != nil {
		return err
	}
	return a.DB.Writer.Submit(ctx, "fileview_audit", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO fileview_audit(ts_ms,principal,kind,source,path_or_ref,resolved_root,outcome,deny_reason,bytes_served,etag,count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			e.TSMS, e.Principal, e.Kind, e.Source, e.PathOrRef, e.ResolvedRoot, e.Outcome, e.DenyReason, e.BytesServed, e.ETag, e.Count)
		return err
	})
}

// RecordAsync is the best-effort path for stat batches: it never blocks the request and a failure only bumps
// Dropped. A denied entry is refused here (denials are always synchronous).
func (a *Auditor) RecordAsync(e AuditEntry) {
	if e.Outcome == "denied" {
		_ = a.Record(context.Background(), e) // never lose a denial
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Record(ctx, e); err != nil {
			a.dropped.Add(1)
		}
	}()
}

// List returns entries newest first (by id) with ts_ms > since, at most limit. next is the newest ts_ms
// returned, to pass as since on the next poll; nil when nothing came back. When more than limit rows are newer
// than since the oldest of them are omitted: a poller that needs every row polls often or raises limit.
func (a *Auditor) List(ctx context.Context, since int64, limit int) ([]AuditEntry, *int64, error) {
	rows, err := a.DB.Reader.QueryContext(ctx, `SELECT id,ts_ms,principal,kind,source,path_or_ref,resolved_root,outcome,deny_reason,bytes_served,etag,count
 FROM fileview_audit WHERE ts_ms > ? ORDER BY id DESC LIMIT ?`, since, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	var next *int64
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TSMS, &e.Principal, &e.Kind, &e.Source, &e.PathOrRef, &e.ResolvedRoot, &e.Outcome, &e.DenyReason, &e.BytesServed, &e.ETag, &e.Count); err != nil {
			return nil, nil, err
		}
		out = append(out, e)
		if next == nil || e.TSMS > *next {
			t := e.TSMS
			next = &t
		}
	}
	return out, next, rows.Err()
}

// RetentionDays reads fileview.audit_retention_days (1..365), default 30.
func (a *Auditor) RetentionDays(ctx context.Context) int {
	var raw string
	if a.DB.Reader.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key='fileview.audit_retention_days'`).Scan(&raw) == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n >= 1 && n <= 365 {
			return n
		}
	}
	return 30
}

// Prune deletes rows older than the retention window and reports how many went.
func (a *Auditor) Prune(ctx context.Context) (int64, error) {
	cutoff := a.now().Add(-time.Duration(a.RetentionDays(ctx)) * 24 * time.Hour).UnixMilli()
	var n int64
	err := a.DB.Writer.Submit(ctx, "fileview_audit_prune", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM fileview_audit WHERE ts_ms < ?`, cutoff)
		if err == nil {
			n, err = res.RowsAffected()
		}
		return err
	})
	return n, err
}

// RunRetention prunes once now and then every interval until ctx ends. A failed or panicking pass is skipped;
// the next one tries again.
func (a *Auditor) RunRetention(ctx context.Context, interval time.Duration) {
	pass := func() {
		defer func() { _ = recover() }()
		_, _ = a.Prune(ctx)
	}
	pass()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pass()
		}
	}
}

// MountAudit registers GET /audit under fileviewapi.Prefix behind guard (RequireOperator: reading the audit
// log is an operator act). Query: limit 1..1000 (default 200), since = unix ms.
func (a *Auditor) MountAudit(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	if guard == nil {
		guard = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("GET "+fileviewapi.Prefix+"/audit", guard(http.HandlerFunc(a.get)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *Auditor) get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if k != "limit" && k != "since" {
			writeJSON(w, 400, map[string]string{"code": "invalid", "message": "Unknown parameter."})
			return
		}
	}
	limit := fileviewapi.DefaultAuditMax
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeJSON(w, 400, map[string]string{"code": "invalid", "message": "limit is 1 to 1000."})
			return
		}
		limit = n
	}
	var since int64
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, 400, map[string]string{"code": "invalid", "message": "since must be a non-negative unix-ms time."})
			return
		}
		since = n
	}
	entries, next, err := a.List(r.Context(), since, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"code": "internal", "message": "The request could not be completed."})
		return
	}
	writeJSON(w, 200, map[string]any{"entries": entries, "next_since": next})
}

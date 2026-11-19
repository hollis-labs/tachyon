package fileview

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func auditSetup(t *testing.T) (*Auditor, *http.ServeMux, *time.Time) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.UnixMilli(1793332800000)
	a := &Auditor{DB: db, Now: func() time.Time { return now }}
	mux := http.NewServeMux()
	a.MountAudit(mux, nil)
	return a, mux, &now
}

func ptr[T any](v T) *T { return &v }

func auditGet(mux *http.ServeMux, q string) (int, map[string]any) {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins/console/v1/files/audit"+q, nil))
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestRecordListAndOrder(t *testing.T) {
	a, mux, now := auditSetup(t)
	ctx := context.Background()
	for i, e := range []AuditEntry{
		{Principal: "bs_1", Kind: "open", Source: "host", PathOrRef: "/home/o/dev/a.md", ResolvedRoot: ptr("dev"), Outcome: "ok", BytesServed: ptr(int64(10)), ETag: ptr("e1")},
		{Principal: "bs_1", Kind: "open", Source: "host", PathOrRef: "/home/o/.ssh/id_ed25519", Outcome: "denied", DenyReason: ptr("secret_pattern")},
		{Principal: "bs_1", Kind: "stat", Source: "host", PathOrRef: "stat", Outcome: "ok", Count: 12},
	} {
		*now = now.Add(time.Second)
		if err := a.Record(ctx, e); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	code, m := auditGet(mux, "")
	es := m["entries"].([]any)
	if code != 200 || len(es) != 3 {
		t.Fatalf("%d %v", code, m)
	}
	first, last := es[0].(map[string]any), es[2].(map[string]any)
	if first["kind"] != "stat" || first["count"].(float64) != 12 || last["path_or_ref"] != "/home/o/dev/a.md" || last["resolved_root"] != "dev" || last["etag"] != "e1" {
		t.Errorf("order or fields: %v / %v", first, last)
	}
	if es[1].(map[string]any)["deny_reason"] != "secret_pattern" || es[1].(map[string]any)["bytes_served"] != nil {
		t.Errorf("denial: %v", es[1])
	}
	if m["next_since"].(float64) != float64(now.UnixMilli()) {
		t.Errorf("next_since %v", m["next_since"])
	}
	// since is exclusive and the poll returns only newer rows.
	_, m = auditGet(mux, "?since="+itoa(first["ts_ms"].(float64)))
	if len(m["entries"].([]any)) != 0 || m["next_since"] != nil {
		t.Errorf("nothing newer: %v", m)
	}
	_, m = auditGet(mux, "?limit=1")
	if len(m["entries"].([]any)) != 1 {
		t.Error("limit")
	}
}

func itoa(f float64) string { return strconv.FormatInt(int64(f), 10) }

func TestValidationAndSanitising(t *testing.T) {
	a, mux, _ := auditSetup(t)
	ctx := context.Background()
	bad := []AuditEntry{
		{Kind: "read", Source: "host", Outcome: "ok"},
		{Kind: "open", Source: "disk", Outcome: "ok"},
		{Kind: "open", Source: "host", Outcome: "maybe"},
		{Kind: "open", Source: "host", Outcome: "denied"}, // a denial needs a reason
		{Kind: "open", Source: "host", Outcome: "denied", DenyReason: ptr("because")},
	}
	for i, e := range bad {
		if a.Record(ctx, e) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if err := a.Record(ctx, AuditEntry{Kind: "caster", Source: "caster", Outcome: "ok", PathOrRef: "caster://designer/page@2?token=abc#frag\n"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(ctx, AuditEntry{Kind: "open", Source: "host", Outcome: "ok", PathOrRef: "/a/b\x1b[31m\n" + strings.Repeat("x", 3000), Principal: strings.Repeat("p", 500)}); err != nil {
		t.Fatal(err)
	}
	_, m := auditGet(mux, "")
	es := m["entries"].([]any)
	long := es[0].(map[string]any)
	if strings.ContainsAny(long["path_or_ref"].(string), "\x1b\n") || len(long["path_or_ref"].(string)) > 1024 || len(long["principal"].(string)) > 128 {
		t.Errorf("not sanitised: %d %d", len(long["path_or_ref"].(string)), len(long["principal"].(string)))
	}
	if ref := es[1].(map[string]any)["path_or_ref"]; ref != "caster://designer/page@2" {
		t.Errorf("caster ref keeps no query or fragment: %v", ref)
	}
}

func TestAuditQueryValidation(t *testing.T) {
	_, mux, _ := auditSetup(t)
	for _, q := range []string{"?limit=0", "?limit=1001", "?limit=x", "?since=-1", "?since=x", "?order=asc"} {
		if code, _ := auditGet(mux, q); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
}

func TestDenialsAreNeverDroppedAndAsyncIsBestEffort(t *testing.T) {
	a, mux, _ := auditSetup(t)
	a.RecordAsync(AuditEntry{Kind: "open", Source: "host", Outcome: "denied", DenyReason: ptr("secret_pattern"), PathOrRef: "/x/.env"})
	_, m := auditGet(mux, "")
	if len(m["entries"].([]any)) != 1 {
		t.Fatal("a denial passed to RecordAsync must be written before it returns")
	}
	a.RecordAsync(AuditEntry{Kind: "stat", Source: "host", Outcome: "ok", Count: 3})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, m := auditGet(mux, ""); len(m["entries"].([]any)) == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("async stat row never landed (dropped %d)", a.Dropped())
}

func TestPruneUsesRetentionSetting(t *testing.T) {
	a, _, now := auditSetup(t)
	ctx := context.Background()
	day := 24 * time.Hour
	base := *now
	for _, age := range []time.Duration{40 * day, 31 * day, 29 * day, day} {
		if err := a.Record(ctx, AuditEntry{Kind: "open", Source: "host", Outcome: "ok", TSMS: base.Add(-age).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := a.Prune(ctx); err != nil || n != 2 {
		t.Fatalf("default 30 days pruned %d (%v)", n, err)
	}
	set := func(v string) {
		if err := a.DB.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES('fileview.audit_retention_days',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, v)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	set("7")
	if n, _ := a.Prune(ctx); n != 1 {
		t.Errorf("7 days pruned %d, want the 29-day row", n)
	}
	for _, bad := range []string{"0", "366", "abc"} {
		set(bad)
		if got := a.RetentionDays(ctx); got != 30 {
			t.Errorf("%q -> %d, want the default 30", bad, got)
		}
	}
}

func TestRunRetentionStopsWithContextAndPrunesFirst(t *testing.T) {
	a, _, now := auditSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Record(ctx, AuditEntry{Kind: "open", Source: "host", Outcome: "ok", TSMS: now.Add(-90 * 24 * time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.RunRetention(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if es, _, _ := a.List(ctx, 0, 10); len(es) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if es, _, _ := a.List(ctx, 0, 10); len(es) != 0 {
		t.Error("the first pass must prune")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunRetention ignored cancellation")
	}
}

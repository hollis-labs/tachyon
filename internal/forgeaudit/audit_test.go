package forgeaudit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestWritesAreRecordedWithoutBodiesAndReadBackNewestFirst(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l := &Log{DB: db, Principal: func(*http.Request) string { return "browser-session:ab12" }}
	mux := http.NewServeMux()
	mux.Handle("PUT /pulls/{owner}/{name}/{num}/merge", l.Wrap("merge", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("num") == "2" {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"merge_blocked","message":"secret words here"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"merged":true,"sha":"8f1322a3"}`))
	})))
	mux.Handle("GET /audit", l.Handler())
	for _, n := range []string{"1", "2"} {
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/pulls/o/r/"+n+"/merge", strings.NewReader(`{"body":"private"}`)))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/audit", nil))
	var out struct{ Entries []map[string]any }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Entries) != 2 {
		t.Fatal(err, w.Body.String())
	}
	a, b := out.Entries[0], out.Entries[1]
	if a["outcome"] != "refused" || a["error_code"] != "merge_blocked" || a["pr_key"] != "github:o/r#2" || a["actor_ref"] != "browser-session:ab12" {
		t.Errorf("refused merge: %v", a)
	}
	if b["outcome"] != "ok" || b["github_ref"] != "8f1322a3" || b["audit_class"] != "lifecycle" {
		t.Errorf("ok merge: %v", b)
	}
	if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret words") {
		t.Error("no bodies in the audit")
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/audit?limit=0", nil))
	if w.Code != 400 {
		t.Errorf("limit 0: %d", w.Code)
	}
}

package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

type fakeProvider struct {
	level assurance.Level
	none  bool
}

func (f *fakeProvider) Status(*http.Request) (assurance.Status, *assurance.Error) {
	if f.none {
		return assurance.Status{}, &assurance.Error{Status: 401, Code: "session_required", Message: "Sign in."}
	}
	return assurance.Status{Level: f.level, CanElevate: f.level < assurance.LevelOperatorToken, TTLPolicy: "30m"}, nil
}
func (f *fakeProvider) Require(r *http.Request, a assurance.Action) (assurance.Status, *assurance.Error) {
	return assurance.RequireOn(f, r, a)
}
func (f *fakeProvider) Elevate(http.ResponseWriter, *http.Request)   {}
func (f *fakeProvider) DeElevate(http.ResponseWriter, *http.Request) {}

func setup(t *testing.T) (*Service, *fakeProvider, http.Handler) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := &fakeProvider{level: assurance.LevelBrowserSession}
	s := &Service{DB: db, Provider: p, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, p, mux
}

func call(h http.Handler, method, body string, hdr ...string) (int, map[string]any) {
	path := settingsapi.AccountPath
	if method == "PUT" {
		path = settingsapi.ProfilePath
	}
	r := httptest.NewRequest(method, "https://tachyon.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://tachyon.test")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func get(m map[string]any, path ...string) any {
	var v any = m
	for _, k := range path {
		mm, _ := v.(map[string]any)
		v = mm[k]
	}
	return v
}

func TestWhoamiStatesAndProfileRoundTrip(t *testing.T) {
	_, p, h := setup(t)
	code, m := call(h, "GET", "")
	if code != 200 || get(m, "identity", "state") != "unknown" || get(m, "identity", "assurance") != "local" || get(m, "can_set_profile") != true {
		t.Fatalf("fresh: %d %v", code, m)
	}
	code, m = call(h, "PUT", `{"display_name":"  Sam ","email":"sam@example.com"}`)
	if code != 200 || get(m, "identity", "display_name") != "Sam" || get(m, "identity", "source") != "local-profile" || get(m, "profile_updated_at") == nil {
		t.Fatalf("save: %d %v", code, m)
	}
	if _, m = call(h, "GET", ""); get(m, "identity", "email") != "sam@example.com" || get(m, "identity", "assurance") != "local" {
		t.Fatalf("read back: %v", m)
	}
	p.level = assurance.LevelOperatorToken
	if _, m = call(h, "GET", ""); get(m, "identity", "assurance") != "verified" || get(m, "identity", "verified_by") != "operator_token" || get(m, "access", "state") != "elevated" {
		t.Fatalf("elevated: %v", m)
	}
	// Dropping the email clears it; the name stays required.
	if code, m = call(h, "PUT", `{"display_name":"Sam"}`); code != 200 || get(m, "identity", "email") != nil {
		t.Fatalf("clear email: %d %v", code, m)
	}
}

func TestProfileWriteChecks(t *testing.T) {
	_, p, h := setup(t)
	if code, m := call(h, "PUT", `{"display_name":"","email":"x"}`); code != 422 || len(get(m, "error", "errors").([]any)) != 2 {
		t.Fatalf("validation: %d %v", code, m)
	}
	if code, _ := call(h, "PUT", `{"display_name":"A","role":"admin"}`); code != 400 {
		t.Fatalf("unknown field = %d, want 400", code)
	}
	if code, _ := call(h, "PUT", `{"display_name":"A"} {}`); code != 400 {
		t.Fatalf("trailing JSON = %d, want 400", code)
	}
	if code, _ := call(h, "PUT", `{"display_name":"A"}`, "Origin", "https://evil.test"); code != 403 {
		t.Fatalf("cross-origin = %d, want 403", code)
	}
	if code, _ := call(h, "PUT", `{"display_name":"A"}`, "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Fatalf("cross-site = %d, want 403", code)
	}
	p.none = true
	if code, m := call(h, "PUT", `{"display_name":"A"}`); code != 401 || get(m, "code") != "session_required" {
		t.Fatalf("no session = %d %v", code, m)
	}
	if code, _ := call(h, "GET", ""); code != 401 {
		t.Fatalf("no session read = %d, want 401", code)
	}
}

func TestDamagedRowReadsAsNoProfile(t *testing.T) {
	s, _, h := setup(t)
	if err := s.DB.Writer.Submit(context.Background(), "t", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO app_settings(key,value) VALUES(?,?)", ProfileKey, "{not json")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if code, m := call(h, "GET", ""); code != 200 || get(m, "identity", "state") != "unknown" {
		t.Fatalf("damaged row: %d %v", code, m)
	}
	if code, _ := call(h, "PUT", `{"display_name":"Fixed"}`); code != 200 {
		t.Fatalf("save over damaged row = %d", code)
	}
}

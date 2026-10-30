package fileview

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

type env struct {
	svc  *Service
	home string
	mux  *http.ServeMux
}

func setup(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, d := range []string{"Downloads", "dev", "notes", "proj/a", ".ssh", ".config/gcloud", "dev/agent-os/workspaces"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := localstore.Open(context.Background(), filepath.Join(root, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &Service{DB: db, Home: home}
	mux := http.NewServeMux()
	svc.Mount(mux, nil, nil)
	return env{svc, home, mux}
}

func (e env) do(method, path, body string) (int, map[string]any) {
	r := httptest.NewRequest(method, "http://x/api/plugins/console/v1/files"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestDefaultsWhenNothingIsSaved(t *testing.T) {
	e := setup(t)
	code, m := e.do("GET", "/settings", "")
	app := m["app"].(map[string]any)
	if code != 200 || app["run_scripts_allowed"] != false || app["allow_user_roots"] != false || app["audit_retention_days"].(float64) != 30 ||
		len(app["roots"].([]any)) != 0 || len(app["caster_namespaces"].([]any)) != 2 || app["caps"].(map[string]any)["text_bytes"].(float64) != 2<<20 {
		t.Errorf("%d %v", code, m)
	}
	if len(m["user"].(map[string]any)["roots"].([]any)) != 0 {
		t.Error("user roots must be []")
	}
}

func TestPutMergesPersistsAndValidates(t *testing.T) {
	e := setup(t)
	notes := filepath.Join(e.home, "notes")
	code, m := e.do("PUT", "/settings", `{"app":{"roots":["`+notes+`/"],"deny_extra":["*.sqlite"],"run_scripts_allowed":true,"audit_retention_days":7}}`)
	app := m["app"].(map[string]any)
	if code != 200 || app["roots"].([]any)[0] != notes || app["run_scripts_allowed"] != true || app["audit_retention_days"].(float64) != 7 {
		t.Fatalf("%d %v", code, m)
	}
	// A second PUT changes only what it names.
	_, m = e.do("PUT", "/settings", `{"app":{"audit_retention_days":9}}`)
	app = m["app"].(map[string]any)
	if app["roots"].([]any)[0] != notes || app["audit_retention_days"].(float64) != 9 || app["deny_extra"].([]any)[0] != "*.sqlite" {
		t.Errorf("merge: %v", app)
	}
	// And it is durable: a new service over the same DB reads it back.
	got, err := (&Service{DB: e.svc.DB, Home: e.home}).Load(context.Background())
	if err != nil || got.App.AuditRetention != 9 || got.App.Roots[0] != notes {
		t.Errorf("reload: %+v %v", got, err)
	}
}

func TestRootValidation(t *testing.T) {
	e := setup(t)
	file := filepath.Join(e.home, "proj", "file.txt")
	os.WriteFile(file, []byte("x"), 0o644)
	link := filepath.Join(e.home, "proj", "sshlink")
	os.Symlink(filepath.Join(e.home, ".ssh"), link)
	parent := filepath.Dir(e.home)
	for name, root := range map[string]string{
		"slash":          "/",
		"home":           e.home,
		"parent of home": parent,
		"relative":       "proj/a",
		"missing":        filepath.Join(e.home, "nope"),
		"a file":         file,
		"ssh":            filepath.Join(e.home, ".ssh"),
		"inside ssh":     filepath.Join(e.home, ".ssh", "x"),
		"gcloud":         filepath.Join(e.home, ".config", "gcloud"),
		"symlink to ssh": link,
		"dotdot to ssh":  filepath.Join(e.home, "proj", "..", ".ssh"),
		"NUL":            e.home + "/proj\x00",
	} {
		b, _ := json.Marshal(map[string]any{"app": map[string]any{"roots": []string{root}}})
		if code, m := e.do("PUT", "/settings", string(b)); code != 400 {
			t.Errorf("%s accepted: %d %v", name, code, m)
		}
	}
	// .config itself is a parent of the gcloud secret dir.
	b, _ := json.Marshal(map[string]any{"app": map[string]any{"roots": []string{filepath.Join(e.home, ".config")}}})
	if code, _ := e.do("PUT", "/settings", string(b)); code != 400 {
		t.Errorf("parent of a secret dir accepted: %d", code)
	}
	if code, _ := e.do("PUT", "/settings", `{"app":{"roots":["`+filepath.Join(e.home, "proj", "a")+`"]}}`); code != 200 {
		t.Errorf("a plain directory must be accepted: %d", code)
	}
}

func TestOtherValidation(t *testing.T) {
	e := setup(t)
	for _, body := range []string{
		`{}`, `{"app":{}} x`, `{"unknown":1}`, `{"app":{"caps":{"text_bytes":1,"image_bytes":1,"html_bytes":1}}}`,
		`{"app":{"audit_retention_days":0}}`, `{"app":{"audit_retention_days":366}}`, `{"app":{"caster_namespaces":["Designer"]}}`,
		`{"app":{"caster_namespaces":[""]}}`, `{"app":{"deny_extra":["a/b"]}}`, `{"app":{"deny_extra":["["]}}`, `{"app":{"deny_extra":[""]}}`,
	} {
		if code, _ := e.do("PUT", "/settings", body); code != 400 {
			t.Errorf("%s: %d", body, code)
		}
	}
	big := `{"app":{"deny_extra":["` + strings.Repeat("a", 20<<10) + `"]}}`
	if code, _ := e.do("PUT", "/settings", big); code != 400 {
		t.Errorf("oversized body: %d", code)
	}
}

func TestUserRootsCannotUnlockThemselves(t *testing.T) {
	e := setup(t)
	a := filepath.Join(e.home, "proj", "a")
	body := `{"app":{"allow_user_roots":true},"user":{"roots":["` + a + `"]}}`
	if code, m := e.do("PUT", "/settings", body); code != 403 || m["code"] != "user_roots_not_allowed" {
		t.Errorf("same-request unlock: %d %v", code, m)
	}
	if _, m := e.do("GET", "/settings", ""); m["app"].(map[string]any)["allow_user_roots"] != false {
		t.Error("a refused PUT must change nothing")
	}
	if code, _ := e.do("PUT", "/settings", `{"app":{"allow_user_roots":true}}`); code != 200 {
		t.Fatal("app unlock")
	}
	if code, m := e.do("PUT", "/settings", `{"user":{"roots":["`+a+`"]}}`); code != 200 || len(m["user"].(map[string]any)["roots"].([]any)) != 1 {
		t.Errorf("after unlock: %d %v", code, m)
	}
	// Turning the switch off hides the roots again without deleting them.
	e.do("PUT", "/settings", `{"app":{"allow_user_roots":false}}`)
	eff, _, _ := e.svc.Effective(context.Background())
	for _, r := range eff {
		if r.Source == "user" {
			t.Error("user root served while allow_user_roots is false")
		}
	}
}

func TestRootsViewListsExistingDefaultsAndSavedRoots(t *testing.T) {
	e := setup(t)
	e.do("PUT", "/settings", `{"app":{"roots":["`+filepath.Join(e.home, "notes")+`"]}}`)
	code, m := e.do("GET", "/roots", "")
	if code != 200 {
		t.Fatal(code)
	}
	byLabel := map[string]map[string]any{}
	for _, r := range m["roots"].([]any) {
		byLabel[r.(map[string]any)["label"].(string)] = r.(map[string]any)
	}
	for label, disp := range map[string]string{"Downloads": "~/Downloads", "dev": "~/dev", "Workspaces": "~/dev/agent-os/workspaces", "notes": "~/notes"} {
		if byLabel[label]["path_display"] != disp || byLabel[label]["writable"] != false {
			t.Errorf("%s: %v", label, byLabel[label])
		}
	}
	if byLabel["notes"]["source"] != "app" || byLabel["Downloads"]["source"] != "default" {
		t.Error("sources")
	}
	if tmp, ok := byLabel["tmp"]; ok && tmp["owner_only"] != true {
		if fi, _ := os.Stat("/tmp"); fi != nil && fi.Mode().Perm()&0o002 != 0 {
			t.Errorf("a world-writable root must be owner_only: %v", tmp)
		}
	}
	if m["caps"].(map[string]any)["image_bytes"].(float64) != 25<<20 {
		t.Errorf("caps: %v", m["caps"])
	}
	// A saved root that disappears is dropped, not served.
	os.RemoveAll(filepath.Join(e.home, "notes"))
	_, m = e.do("GET", "/roots", "")
	for _, r := range m["roots"].([]any) {
		if r.(map[string]any)["label"] == "notes" {
			t.Error("vanished root still listed")
		}
	}
}

func TestStoredBadValuesFallBackToDefaults(t *testing.T) {
	e := setup(t)
	rows := map[string]string{KeyRoots: `["/"]`, KeyCaps: `{"text_bytes":1}`, KeyRetention: `0`, KeyCasterNS: `["BAD"]`, KeyDenyExtra: `not json`, KeyUserRoots: `["` + e.home + `"]`, KeyRunScripts: `true`}
	err := e.svc.DB.Writer.Submit(context.Background(), "t", func(ctx context.Context, tx *sql.Tx) error {
		for k, v := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES(?,?)`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.App.Roots) != 0 || got.App.Caps.Text != 2<<20 || got.App.AuditRetention != 30 || got.App.CasterNamespaces[0] != "designer" || len(got.App.DenyExtra) != 0 || len(got.User.Roots) != 0 {
		t.Errorf("a bad stored value must not widen access: %+v", got)
	}
	if !got.App.RunScripts {
		t.Error("a valid stored value must still load")
	}
}

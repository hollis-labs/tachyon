package settingsreg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

type provider struct{}

func (provider) Status(r *http.Request) (assurance.Status, *assurance.Error) {
	switch r.Header.Get("X-Level") {
	case "operator":
		return assurance.Status{Level: assurance.LevelOperatorToken}, nil
	case "browser":
		return assurance.Status{Level: assurance.LevelBrowserSession}, nil
	}
	return assurance.Status{}, &assurance.Error{Status: 401, Code: "session_required", Message: "Sign in."}
}
func (p provider) Require(r *http.Request, a assurance.Action) (assurance.Status, *assurance.Error) {
	return assurance.RequireOn(p, r, a)
}
func (provider) Elevate(http.ResponseWriter, *http.Request)   {}
func (provider) DeElevate(http.ResponseWriter, *http.Request) {}

func sc(v string) *admin.Scalar { s, _ := admin.String(v); return &s }
func ic(v int64) *admin.Scalar  { s, _ := admin.Integer(v); return &s }
func fp(v float64) *float64     { return &v }

func testGroup() Group {
	envV, _ := admin.Integer(8000)
	return Group{ID: "demo", Label: "Demo",
		Fields: []admin.Field{
			{Key: "fresh_s", Type: admin.IntegerType, Title: "Fresh", Default: ic(30), Minimum: fp(0), Maximum: fp(3600), Editable: true},
			{Key: "timeout_ms", Type: admin.IntegerType, Title: "Timeout", Default: ic(5000), ReadOnlyReason: "Set by an environment variable."},
			{Key: "ttl", Type: admin.StringType, Title: "TTL", Default: sc("never"), Pattern: `^(always|never|[1-9][0-9]{0,2}d)$`, Editable: true, RestartRequired: true, ApplyTarget: "tangent"},
		},
		Operator: map[string]struct{}{"ttl": {}},
		Layers: func(context.Context) map[string]map[admin.SourceKind]Layer {
			return map[string]map[admin.SourceKind]Layer{"timeout_ms": {admin.EnvSource: {Value: envV, Label: "TACHYON_X"}}}
		}}
}

type fx struct {
	t   *testing.T
	db  *localstore.Store
	reg *Registry
	h   http.Handler
}

func newFx(t *testing.T) *fx {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return mount(t, db)
}

func mount(t *testing.T, db *localstore.Store) *fx {
	reg := &Registry{DB: db}
	var applied []string
	g := testGroup()
	g.Applied = func(_ context.Context, ch []string) { applied = append(applied, ch...) }
	if err := reg.Register(g); err != nil {
		t.Fatal(err)
	}
	h, err := reg.Handler(provider{})
	if err != nil {
		t.Fatal(err)
	}
	return &fx{t: t, db: db, reg: reg, h: h}
}

func (f *fx) do(method, path, level, body string, hdr map[string]string) (int, http.Header, map[string]any) {
	r := httptest.NewRequest(method, "https://tachyon.test"+path, strings.NewReader(body))
	if level != "" {
		r.Header.Set("X-Level", level)
	}
	if method == "POST" {
		r.Header.Set("Origin", "https://tachyon.test")
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, w.Header(), out
}

func values(out map[string]any) map[string]any { return out["values"].(map[string]any) }

func TestReadLayersAndProvenance(t *testing.T) {
	f := newFx(t)
	st, hd, out := f.do("GET", settingsapi.AdminPrefix+"/settings/demo", "browser", "", nil)
	if st != 200 || hd.Get("ETag") == "" {
		t.Fatalf("%d %v", st, out)
	}
	v := values(out)
	fresh := v["fresh_s"].(map[string]any)
	if fresh["value"] != float64(30) || fresh["source"].(map[string]any)["kind"] != "default" || fresh["editable"] != true {
		t.Fatalf("default: %v", fresh)
	}
	to := v["timeout_ms"].(map[string]any)
	if to["value"] != float64(8000) || to["source"].(map[string]any)["kind"] != "env" || to["editable"] != false {
		t.Fatalf("env layer: %v", to)
	}
	if ttl := v["ttl"].(map[string]any); ttl["editable"] != false || !strings.Contains(ttl["read_only_reason"].(string), "Operator") {
		t.Fatalf("operator field below operator: %v", ttl)
	}
	_, _, op := f.do("GET", settingsapi.AdminPrefix+"/settings/demo", "operator", "", nil)
	if values(op)["ttl"].(map[string]any)["editable"] != true {
		t.Fatalf("operator can edit: %v", op)
	}
}

func TestUpdatePersistsAndETagConflicts(t *testing.T) {
	f := newFx(t)
	path := settingsapi.AdminPrefix + "/settings/demo"
	_, hd, _ := f.do("GET", path, "browser", "", nil)
	etag := hd.Get("ETag")
	st, _, out := f.do("POST", path+"/update", "browser", `{"revision":"`+f.reg.Revision()+`","set":{"fresh_s":99},"unset":[]}`, map[string]string{"If-Match": etag})
	if st != 200 || out["changed_keys"].([]any)[0] != "fresh_s" {
		t.Fatalf("update: %d %v", st, out)
	}
	// The same stale ETag again is a conflict, not a lost update.
	st, _, out = f.do("POST", path+"/update", "browser", `{"revision":"`+f.reg.Revision()+`","set":{"fresh_s":5},"unset":[]}`, map[string]string{"If-Match": etag})
	if st != 412 {
		t.Fatalf("stale etag: %d %v", st, out)
	}
	// A new registry over the same database sees the saved value (durable), from the override layer.
	g := mount(t, f.db)
	_, _, rd := g.do("GET", path, "browser", "", nil)
	fresh := values(rd)["fresh_s"].(map[string]any)
	if fresh["value"] != float64(99) || fresh["source"].(map[string]any)["kind"] != "override" || fresh["has_override"] != true {
		t.Fatalf("persisted: %v", fresh)
	}
	// Validation: out of range is 422 and nothing is written.
	_, hd2, _ := g.do("GET", path, "browser", "", nil)
	st, _, _ = g.do("POST", path+"/update", "browser", `{"revision":"`+g.reg.Revision()+`","set":{"fresh_s":99999},"unset":[]}`, map[string]string{"If-Match": hd2.Get("ETag")})
	if st != 422 {
		t.Fatalf("range: %d", st)
	}
	// Reset removes the override and the default shows again.
	st, _, out = g.do("POST", path+"/reset", "browser", `{"revision":"`+g.reg.Revision()+`","keys":["fresh_s"]}`, map[string]string{"If-Match": hd2.Get("ETag")})
	if st != 200 || values(out["snapshot"].(map[string]any))["fresh_s"].(map[string]any)["source"].(map[string]any)["kind"] != "default" {
		t.Fatalf("reset: %d %v", st, out)
	}
}

func TestOperatorFieldStepUpAndPendingRestart(t *testing.T) {
	f := newFx(t)
	path := settingsapi.AdminPrefix + "/settings/demo"
	_, hd, _ := f.do("GET", path, "browser", "", nil)
	body := `{"revision":"` + f.reg.Revision() + `","set":{"ttl":"7d"},"unset":[]}`
	st, _, out := f.do("POST", path+"/update", "browser", body, map[string]string{"If-Match": hd.Get("ETag")})
	e, _ := out["error"].(map[string]any)
	su, _ := e["step_up"].(map[string]any)
	if st != 403 || e["code"] != "operator_access_required" || su["ref"] != "demo.ttl" || su["action"] != "settings.write_operator" {
		t.Fatalf("browser session on an operator field: %d %v", st, out)
	}
	_, hd, _ = f.do("GET", path, "operator", "", nil)
	st, _, out = f.do("POST", path+"/update", "operator", body, map[string]string{"If-Match": hd.Get("ETag")})
	if st != 200 || out["restart_required"] != true || out["apply_targets"].([]any)[0] != "tangent" {
		t.Fatalf("operator update: %d %v", st, out)
	}
	ttl := values(out["snapshot"].(map[string]any))["ttl"].(map[string]any)
	if ttl["apply_state"] != "pending_restart" {
		t.Fatalf("apply state: %v", ttl)
	}
}

func TestGuards(t *testing.T) {
	f := newFx(t)
	path := settingsapi.AdminPrefix + "/settings/demo"
	if st, _, _ := f.do("GET", path, "", "", nil); st != 401 {
		t.Errorf("no session: %d", st)
	}
	if st, _, _ := f.do("GET", path, "browser", "", map[string]string{"Authorization": "Bearer x"}); st != 403 {
		t.Errorf("bearer: %d", st)
	}
	body := `{"revision":"x","set":{"fresh_s":1},"unset":[]}`
	if st, _, _ := f.do("POST", path+"/update", "browser", body, map[string]string{"Origin": "https://evil.test"}); st != 403 {
		t.Errorf("cross-origin: %d", st)
	}
	if st, _, _ := f.do("POST", path+"/update", "", body, nil); st != 401 {
		t.Errorf("no session write: %d", st)
	}
	if st, _, _ := f.do("GET", settingsapi.AdminPrefix+"/settings/nope", "browser", "", nil); st != 404 {
		t.Errorf("unknown group: %d", st)
	}
	// An env-sourced field cannot be written even with the right ETag.
	_, hd, _ := f.do("GET", path, "operator", "", nil)
	st, _, _ := f.do("POST", path+"/update", "operator", `{"revision":"`+f.reg.Revision()+`","set":{"timeout_ms":1},"unset":[]}`, map[string]string{"If-Match": hd.Get("ETag")})
	if st != 422 && st != 403 {
		t.Errorf("env field write: %d", st)
	}
}

func TestRegisterRefusals(t *testing.T) {
	r := &Registry{}
	g := testGroup()
	if err := r.Register(g); err != nil {
		t.Fatal(err)
	}
	if r.Register(g) == nil {
		t.Error("duplicate id")
	}
	bad := testGroup()
	bad.ID = "Bad-ID"
	if r.Register(bad) == nil {
		t.Error("bad id")
	}
	op := testGroup()
	op.ID, op.Operator = "other", map[string]struct{}{"missing": {}}
	if r.Register(op) == nil {
		t.Error("operator key that is not a field")
	}
	before := r.Revision()
	ng := testGroup()
	ng.ID = "second"
	if err := r.Register(ng); err != nil || r.Revision() == before {
		t.Errorf("revision must change when a group is added: %v", err)
	}
}

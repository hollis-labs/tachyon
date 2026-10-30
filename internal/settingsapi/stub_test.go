package settingsapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
)

type client struct {
	t *testing.T
	h http.Handler
}

func newClient(t *testing.T) (*client, *Stub) {
	s := NewStub()
	return &client{t, s.Handler()}, s
}

func (c *client) do(method, path, body string, hdr ...string) (int, http.Header, map[string]any) {
	c.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "tachyon.test"
	r.Header.Set("Origin", "http://tachyon.test")
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			r.Header.Del(hdr[i])
		} else {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, rec.Result().Header, m
}

func (c *client) want(code int, method, path, body string, hdr ...string) (http.Header, map[string]any) {
	c.t.Helper()
	got, h, m := c.do(method, path, body, hdr...)
	if got != code {
		c.t.Fatalf("%s %s = %d, want %d: %v", method, path, got, code, m)
	}
	return h, m
}

func field(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}

func TestManifestIsLibraryValidAndMatchesAdminLayout(t *testing.T) {
	c, _ := newClient(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", AdminPrefix+"/manifest", nil)
	c.h.ServeHTTP(rec, r)
	var m admin.Manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.ContractVersion != 1 || m.Revision != ManifestRevision || len(m.Settings) != len(Specs()) {
		t.Fatalf("manifest: %+v", m)
	}
	for _, g := range m.Settings {
		want := AdminRoutes(g.ID)
		eps := []*admin.Endpoint{g.Endpoints.Read, g.Endpoints.Validate, g.Endpoints.Update, g.Endpoints.Reset}
		for i, e := range eps {
			if e == nil {
				if g.ID == GroupTranscripts && i >= 2 {
					continue // read-only group declares no update or reset
				}
				t.Fatalf("%s: missing endpoint %d", g.ID, i)
			}
			if e.Method != want[i].Method || e.Path != want[i].Path {
				t.Fatalf("%s: endpoint %d = %s %s, contract says %s %s", g.ID, i, e.Method, e.Path, want[i].Method, want[i].Path)
			}
		}
	}
}

func TestSnapshotCoversSourcesAndApplyStates(t *testing.T) {
	c, _ := newClient(t)
	h, m := c.want(200, "GET", AdminPrefix+"/settings/"+GroupGeneral, "")
	if h.Get("ETag") == "" {
		t.Fatal("snapshot needs an ETag")
	}
	kinds := map[string]string{}
	for k, v := range field(m, "values").(map[string]any) {
		kinds[k], _ = field(v.(map[string]any), "source", "kind").(string)
	}
	if kinds["cache_fresh_s"] != "file" || kinds["cache_timeout_ms"] != "env" || kinds["snapshot_first"] != "default" {
		t.Fatalf("sources: %v", kinds)
	}
	if field(m, "values", "cache_timeout_ms", "editable") != false || field(m, "values", "cache_timeout_ms", "read_only_reason") == "" {
		t.Fatalf("env value must be read-only with a reason: %v", m["values"])
	}
	if field(m, "values", "elevation_ttl", "editable") != false {
		t.Fatalf("operator field must read as not editable without elevation")
	}
	_, ro := c.want(200, "GET", AdminPrefix+"/settings/"+GroupTranscripts, "")
	if field(ro, "values", "max_sessions", "source", "kind") != "env" {
		t.Fatalf("transcripts: %v", ro)
	}
}

func update(c *client, group, etag, body string, hdr ...string) (int, http.Header, map[string]any) {
	h := append([]string{}, hdr...)
	if etag != "" {
		h = append(h, "If-Match", etag)
	}
	return c.do("POST", AdminPrefix+"/settings/"+group+"/update", body, h...)
}

func TestUpdateFlowAndErrors(t *testing.T) {
	c, _ := newClient(t)
	base := AdminPrefix + "/settings/" + GroupChat
	h, _ := c.want(200, "GET", base, "")
	etag := h.Get("ETag")
	body := `{"revision":"` + ManifestRevision + `","set":{"scratchpad_placement":"bottom"},"unset":[]}`

	if code, _, _ := update(c, GroupChat, "", body); code != 428 {
		t.Fatalf("no If-Match = %d, want 428", code)
	}
	if code, _, _ := update(c, GroupChat, `"nope"`, body); code != 412 {
		t.Fatalf("stale etag = %d, want 412", code)
	}
	stale := strings.Replace(body, ManifestRevision, "old", 1)
	if code, _, _ := update(c, GroupChat, etag, stale); code != 409 {
		t.Fatalf("stale manifest = %d, want 409", code)
	}
	bad := `{"revision":"` + ManifestRevision + `","set":{"scratchpad_placement":"middle"},"unset":[]}`
	code, _, m := update(c, GroupChat, etag, bad)
	if code != 422 || field(m, "error", "code") != "validation_failed" || field(m, "error", "errors").([]any)[0].(map[string]any)["path"] != "/scratchpad_placement" {
		t.Fatalf("invalid value: %d %v", code, m)
	}
	code, h2, m := update(c, GroupChat, etag, body)
	if code != 200 || h2.Get("ETag") == etag || field(m, "changed_keys").([]any)[0] != "scratchpad_placement" ||
		field(m, "snapshot", "values", "scratchpad_placement", "source", "kind") != "override" {
		t.Fatalf("update: %d %v", code, m)
	}
	if code, _, _ := update(c, GroupChat, etag, body); code != 412 {
		t.Fatalf("replaying the old etag = %d, want 412", code)
	}
	reset := `{"revision":"` + ManifestRevision + `","keys":["scratchpad_placement"]}`
	code, _, m = c.do("POST", base+"/reset", reset, "If-Match", h2.Get("ETag"))
	if code != 200 || field(m, "snapshot", "values", "scratchpad_placement", "source", "kind") != "default" {
		t.Fatalf("reset: %d %v", code, m)
	}
	// validate answers without saving
	code, _, m = c.do("POST", base+"/validate", bad)
	if code != 200 || m["valid"] != false {
		t.Fatalf("validate: %d %v", code, m)
	}
}

func TestOperatorFieldNeedsStepUpThenSavesPendingRestart(t *testing.T) {
	c, _ := newClient(t)
	base := AdminPrefix + "/settings/" + GroupGeneral
	h, _ := c.want(200, "GET", base, "")
	body := `{"revision":"` + ManifestRevision + `","set":{"elevation_ttl":"7d"},"unset":[]}`
	code, _, m := update(c, GroupGeneral, h.Get("ETag"), body)
	if code != 403 || field(m, "error", "code") != CodeOperatorAccess || field(m, "error", "step_up", "action") != "settings.write" ||
		len(field(m, "error", "methods").([]any)) == 0 {
		t.Fatalf("refusal: %d %v", code, m)
	}
	signIn(c)
	h, _ = c.want(200, "GET", base, "")
	if field(c.mustGet(base), "values", "elevation_ttl", "editable") != true {
		t.Fatal("elevated session should edit elevation_ttl")
	}
	code, _, m = update(c, GroupGeneral, h.Get("ETag"), body)
	if code != 200 || m["restart_required"] != true || m["apply_targets"].([]any)[0] != "tangent" ||
		field(m, "snapshot", "values", "elevation_ttl", "apply_state") != "pending_restart" {
		t.Fatalf("update: %d %v", code, m)
	}
}

func (c *client) mustGet(path string) map[string]any {
	_, m := c.want(200, "GET", path, "")
	return m
}

func signIn(c *client) {
	_, ch := c.want(200, "POST", StubAccessPrefix+"/challenge", `{"action":"elevate"}`)
	c.want(200, "POST", StubAccessPrefix+"/verify", `{"challenge_id":"`+ch["challenge_id"].(string)+`","method":"operator_token","credential":"stub-operator-token"}`)
}

func TestWritesNeedSameOriginAndRefuseAuthorization(t *testing.T) {
	c, _ := newClient(t)
	body := `{"revision":"` + ManifestRevision + `","set":{"scratchpad_placement":"bottom"},"unset":[]}`
	if code, _, m := c.do("POST", AdminPrefix+"/settings/chat/validate", body, "Origin", "http://evil.test"); code != 403 {
		t.Fatalf("cross-origin = %d %v", code, m)
	}
	if code, _, _ := c.do("GET", AdminPrefix+"/manifest", "", "Authorization", "Bearer x"); code != 403 {
		t.Fatalf("Authorization header = %d, want 403", code)
	}
	if code, _, _ := c.do("PUT", ProfilePath, `{"display_name":"A"}`, "Origin", "http://evil.test"); code != 403 {
		t.Fatalf("cross-origin profile = %d, want 403", code)
	}
}

func TestAccountProfileLifecycle(t *testing.T) {
	c, _ := newClient(t)
	_, m := c.want(200, "GET", AccountPath, "")
	if field(m, "identity", "state") != "unknown" || m["can_set_profile"] != true {
		t.Fatalf("fresh account: %v", m)
	}
	_, e := c.want(422, "PUT", ProfilePath, `{"display_name":"","email":"nope"}`)
	if n := len(field(e, "error", "errors").([]any)); n != 2 {
		t.Fatalf("errors: %v", e)
	}
	_, m = c.want(200, "PUT", ProfilePath, `{"display_name":" Sam ","email":"sam@example.com"}`)
	if field(m, "identity", "display_name") != "Sam" || field(m, "identity", "assurance") != "local" || field(m, "identity", "source") != "local-profile" {
		t.Fatalf("saved: %v", m)
	}
	c.want(400, "PUT", ProfilePath, `{"display_name":"A","admin":true}`)
	signIn(c)
	_, m = c.want(200, "GET", AccountPath, "")
	if field(m, "identity", "assurance") != "verified" || field(m, "identity", "verified_by") != "operator_token" {
		t.Fatalf("verified: %v", m)
	}
	_, m = c.want(200, "GET", AccountPath, "", "X-Stub-Scenario", "unknown")
	if field(m, "identity", "state") != "unknown" {
		t.Fatalf("scenario unknown: %v", m)
	}
}

func TestAccessStatesAndDialogFlow(t *testing.T) {
	c, s := newClient(t)
	now := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return now }

	_, a := c.want(200, "GET", StubAccessPrefix, "")
	if a["state"] != "none" || field(a, "session", "state") != "active" || field(a, "step_up", "supported") != true ||
		field(a, "required_for", "batch") != true || len(a["methods"].([]any)) != 3 {
		t.Fatalf("fresh access: %v", a)
	}
	_, a = c.want(200, "GET", StubAccessPrefix, "", "X-Stub-Scenario", "expired")
	if field(a, "session", "state") != "expired" {
		t.Fatalf("expired: %v", a)
	}
	_, a = c.want(200, "GET", StubAccessPrefix, "", "X-Stub-Scenario", "no_step_up")
	if field(a, "step_up", "supported") != false {
		t.Fatalf("no_step_up: %v", a)
	}
	c.want(501, "POST", StubAccessPrefix+"/challenge", `{"action":"x"}`, "X-Stub-Scenario", "no_step_up")

	_, ch := c.want(200, "POST", StubAccessPrefix+"/challenge", `{"action":"resolve_high","ref":"item_1"}`)
	if field(ch, "display", "risk") != "high" {
		t.Fatalf("risk: %v", ch)
	}
	id := ch["challenge_id"].(string)
	verify := func(code int, method, cred string) map[string]any {
		_, m := c.want(code, "POST", StubAccessPrefix+"/verify", `{"challenge_id":"`+id+`","method":"`+method+`","credential":"`+cred+`"}`)
		return m
	}
	if m := verify(403, "operator_token", "wrong"); m["code"] != CodeInvalidCredentials || m["attempts_left"] != nil {
		t.Fatalf("wrong: %v", m)
	}
	verify(400, "password", "x")
	for i := 0; i < LockoutAfter-2; i++ {
		verify(403, "operator_token", "wrong")
	}
	if m := verify(429, "operator_token", "wrong"); m["code"] != CodeLocked || m["retry_after_s"].(float64) != LockoutWindowS {
		t.Fatalf("lock: %v", m)
	}
	_, a = c.want(200, "GET", StubAccessPrefix, "")
	if field(a, "lockout", "locked") != true {
		t.Fatalf("lockout in access: %v", a)
	}
	now = now.Add(2 * time.Minute)
	_, ch2 := c.want(200, "POST", StubAccessPrefix+"/challenge", `{"action":"elevate"}`)
	id = ch2["challenge_id"].(string)
	if m := verify(200, "operator_token", "stub-operator-token"); m["assurance"] != "operator-token" || field(m, "grant", "scope") != "session" {
		t.Fatalf("grant: %v", m)
	}
	_, a = c.want(200, "GET", StubAccessPrefix, "")
	if a["state"] != "elevated" || a["expires_in_s"].(float64) <= 0 {
		t.Fatalf("elevated: %v", a)
	}
	now = now.Add(time.Hour)
	_, a = c.want(200, "GET", StubAccessPrefix, "")
	if a["state"] != "none" || field(a, "session", "state") != "expired" {
		t.Fatalf("after expiry: %v", a)
	}
	id = "ch_expired"
	verify(410, "operator_token", "stub-operator-token")
}

func TestForcedStatusAndRouteTable(t *testing.T) {
	c, _ := newClient(t)
	for _, code := range []int{401, 403, 503} {
		c.want(code, "GET", AccountPath, "", "X-Stub-Status", itoa(code))
	}
	if len(Routes()) != 9+4*len(Specs()) {
		t.Fatalf("route count: %d", len(Routes()))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestValidateProfile(t *testing.T) {
	for _, tc := range []struct {
		in   Profile
		errs int
	}{
		{Profile{DisplayName: "A"}, 0},
		{Profile{DisplayName: strings.Repeat("x", 81)}, 1},
		{Profile{DisplayName: "A\x00"}, 1},
		{Profile{DisplayName: "A", Email: "a@b"}, 1},
		{Profile{DisplayName: "A", Email: "Name <a@b.co>"}, 1},
		{Profile{DisplayName: "A", Email: "a@b.co"}, 0},
		{Profile{}, 1},
	} {
		if _, e := ValidateProfile(tc.in); len(e) != tc.errs {
			t.Errorf("%+v: %d errors, want %d: %v", tc.in, len(e), tc.errs, e)
		}
	}
}

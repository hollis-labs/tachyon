package alertsapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://x"+path, strings.NewReader(body))
	if method != "GET" {
		r.Header.Set("Origin", "http://x")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func obj(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, w.Body.String())
	}
	return m
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
	n := 0
	for _, dir := range []string{"fixtures", "rulepack"} {
		err := fs.WalkDir(assets, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, _ := assets.ReadFile(p)
			var v any
			if json.Unmarshal(b, &v) != nil {
				t.Errorf("%s is not valid JSON", p)
			}
			if m := secret.Find(b); m != nil {
				t.Errorf("%s looks like it holds a secret: %q", p, m)
			}
			n++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n == 0 {
		t.Fatal("no fixtures")
	}
}

func TestEveryRouteIsServedAndWritesNeedSameOrigin(t *testing.T) {
	h := NewStub().Handler() // panics if a route has no handler
	for _, p := range []string{Prefix, Prefix + "?view=history", Prefix + "/counts", Prefix + "/al_approval_01", Prefix + "/prefs",
		Prefix + "/rules", Prefix + "/sources", Prefix + "/openapi.yaml", StatePrefix} {
		if w := call(t, h, "GET", p, ""); w.Code != 200 || w.Header().Get("X-Tachyon-Alerts-Stub") != "1" {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	for _, c := range [][2]string{{"POST", Prefix + "/al_approval_01/ops"}, {"POST", Prefix + "/ops/bulk"}, {"PUT", Prefix + "/prefs"}, {"POST", Prefix + "/prefs/migrate"}, {"PUT", StatePrefix + "/al_approval_01"}} {
		r := httptest.NewRequest(c[0], "http://x"+c[1], strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("%s %s without Origin: %d", c[0], c[1], w.Code)
		}
		r = httptest.NewRequest(c[0], "http://x"+c[1], strings.NewReader(`{}`))
		r.Header.Set("Origin", "http://x")
		r.Header.Set("Authorization", "Bearer x")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("%s %s with Authorization: %d", c[0], c[1], w.Code)
		}
	}
}

func TestListFiltersAndErrors(t *testing.T) {
	h := NewStub().Handler()
	m := obj(t, call(t, h, "GET", Prefix+"?needs_you=1", ""))
	for _, a := range m["alerts"].([]any) {
		if a.(map[string]any)["needs_you"] != true || a.(map[string]any)["status"] != "active" {
			t.Errorf("needs_you filter leaked %v", a)
		}
	}
	first := obj(t, call(t, h, "GET", Prefix+"?limit=2", ""))
	if first["next"] == nil || len(first["alerts"].([]any)) != 2 {
		t.Fatalf("paging: %v", first)
	}
	second := obj(t, call(t, h, "GET", Prefix+"?limit=2&cursor="+first["next"].(string), ""))
	if second["alerts"].([]any)[0].(map[string]any)["alert_id"] == first["alerts"].([]any)[0].(map[string]any)["alert_id"] {
		t.Error("cursor did not advance")
	}
	for _, q := range []string{"?view=x", "?limit=0", "?min_severity=loud", "?bogus=1", "?cursor=!!"} {
		if w := call(t, h, "GET", Prefix+q, ""); w.Code != 400 {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
	if w := call(t, h, "GET", Prefix+"/al_nope", ""); w.Code != 404 {
		t.Errorf("unknown alert: %d", w.Code)
	}
	if w := call(t, h, "GET", Prefix+"/counts", "", "X-Stub-Status", "503"); w.Code != 503 {
		t.Errorf("forced status: %d", w.Code)
	}
}

func TestOpsCheckVersionAndMoveState(t *testing.T) {
	h := NewStub().Handler()
	id := Prefix + "/al_message_01"
	cur := obj(t, call(t, h, "GET", id, ""))["alert"].(map[string]any)
	v := int(cur["version"].(float64))
	if w := call(t, h, "POST", id+"/ops", `{"op":"dismiss"}`); w.Code != 400 {
		t.Errorf("missing expected_version: %d", w.Code)
	}
	w := call(t, h, "POST", id+"/ops", `{"op":"dismiss","expected_version":99}`)
	if w.Code != 409 || obj(t, w)["code"] != CodeVersionConflict || obj(t, w)["current"] == nil {
		t.Errorf("stale version: %d %s", w.Code, w.Body.String())
	}
	w = call(t, h, "POST", id+"/ops", `{"op":"snooze","preset":"1h","note":"later","expected_version":`+itoa(v)+`}`)
	a := obj(t, w)
	if w.Code != 200 || a["status"] != "snoozed" || a["wake_at_ms"] == nil || int(a["version"].(float64)) != v+1 {
		t.Fatalf("snooze: %d %s", w.Code, w.Body.String())
	}
	if c := obj(t, call(t, h, "GET", Prefix+"?view=history", "")); !contains(c, "al_message_01") {
		t.Error("snoozed alert is not in history")
	}
	for _, body := range []string{`{"op":"snooze","expected_version":%d}`, `{"op":"snooze","preset":"2h","expected_version":%d}`, `{"op":"defer","expected_version":%d}`, `{"op":"explode","expected_version":%d}`, `{"op":"snooze","until_ms":1,"expected_version":%d}`} {
		if w := call(t, h, "POST", id+"/ops", strings.Replace(body, "%d", itoa(v+1), 1)); w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	w = call(t, h, "POST", id+"/ops", `{"op":"reopen","expected_version":`+itoa(v+1)+`}`)
	if a := obj(t, w); a["status"] != "active" || a["read"] != false || a["wake_at_ms"] != nil {
		t.Errorf("reopen: %s", w.Body.String())
	}
}

func TestBulkAndSeqConflict(t *testing.T) {
	h := NewStub().Handler()
	if w := call(t, h, "POST", Prefix+"/ops/bulk", `{"op":"read","scope":"all_active","expected_seq":1}`); w.Code != 409 || obj(t, w)["code"] != CodeSeqConflict {
		t.Errorf("seq conflict: %d", w.Code)
	}
	w := call(t, h, "POST", Prefix+"/ops/bulk", `{"op":"read","scope":"all_active"}`)
	if w.Code != 200 || obj(t, w)["counts"].(map[string]any)["unread"].(float64) != 0 {
		t.Errorf("mark all read: %d %s", w.Code, w.Body.String())
	}
	for _, b := range []string{`{"op":"snooze","scope":"all_active"}`, `{"op":"read"}`, `{"op":"read","ids":["a"],"scope":"all_active"}`} {
		if w := call(t, h, "POST", Prefix+"/ops/bulk", b); w.Code != 400 {
			t.Errorf("%s: %d", b, w.Code)
		}
	}
}

func TestPrefsValidateAndMigrateOnce(t *testing.T) {
	h := NewStub().Handler()
	legacy, _ := assets.ReadFile("fixtures/prefs_legacy.json")
	m := obj(t, call(t, h, "POST", Prefix+"/prefs/migrate", string(legacy)))
	p := m["prefs"].(map[string]any)
	if m["applied"] != true || p["notify_level"] != "blockers" || p["quiet"] != true || p["coalesce_window_sec"].(float64) != 300 {
		t.Errorf("migrate: %v", m)
	}
	if m := obj(t, call(t, h, "POST", Prefix+"/prefs/migrate", `{"toastMinSeverity":"debug"}`)); m["applied"] != false || m["prefs"].(map[string]any)["notify_level"] != "blockers" {
		t.Errorf("second migrate must not apply: %v", m)
	}
	bad := `{"notify_level":"loud","toasts":true,"quiet":false,"quiet_hours":{"enabled":false,"start":"22:00","end":"07:00","tz":"UTC"},"muted_sources":[],"coalesce_window_sec":120}`
	if w := call(t, h, "PUT", Prefix+"/prefs", bad); w.Code != 400 {
		t.Errorf("bad notify_level: %d", w.Code)
	}
	if got := NotifyLevelFor("warning"); got != "warnings" {
		t.Errorf("warning maps to %s", got)
	}
	if got := NotifyLevelFor("notice"); got != "everything" {
		t.Errorf("notice maps to %s", got)
	}
}

func TestStateCompatibilityRoutes(t *testing.T) {
	h := NewStub().Handler()
	w := call(t, h, "PUT", StatePrefix+"/al_checkpoint_01", `{"type":"settle","resolution":"approved","note":"ok"}`)
	m := obj(t, w)
	if w.Code != 200 || m["status"] != "settled" || m["resolution"] != "approved" || m["key"] != "al_checkpoint_01" {
		t.Errorf("settle: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "PUT", StatePrefix+"/al_checkpoint_01", `{"type":"snooze"}`); w.Code != 400 {
		t.Errorf("snooze without until: %d", w.Code)
	}
}

// The console client keeps approval and bus event ids in the same state store, so any key is accepted and kept,
// with epoch-ms numbers; until is epoch ms or RFC 3339.
func TestStateKeepsAnyKeyAndAcceptsBothUntilForms(t *testing.T) {
	h := NewStub().Handler()
	w := call(t, h, "PUT", StatePrefix+"/approval:ap_1", `{"type":"dismiss","note":"later"}`)
	m := obj(t, w)
	if w.Code != 200 || m["key"] != "approval:ap_1" || m["status"] != "dismissed" || m["updatedAt"].(float64) <= 0 {
		t.Fatalf("any key: %d %s", w.Code, w.Body.String())
	}
	soon := time.Now().Add(time.Hour)
	for name, body := range map[string]string{
		"ms":      `{"type":"snooze","until":` + strconv.FormatInt(soon.UnixMilli(), 10) + `}`,
		"rfc3339": `{"type":"snooze","until":"` + soon.UTC().Format(time.RFC3339) + `"}`,
	} {
		w := call(t, h, "PUT", StatePrefix+"/bus:evt_9", body)
		m := obj(t, w)
		if w.Code != 200 || m["status"] != "snoozed" || m["snoozeUntil"].(float64) < float64(soon.UnixMilli()-1000) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := call(t, h, "PUT", StatePrefix+"/bus:evt_9", `{"type":"snooze","until":"soon"}`); w.Code != 400 {
		t.Errorf("garbled until: %d", w.Code)
	}
	if w := call(t, h, "PUT", StatePrefix+"/bus:evt_9", `{"type":"explode"}`); w.Code != 400 {
		t.Errorf("unknown type: %d", w.Code)
	}
	list := obj(t, call(t, h, "GET", StatePrefix+"?prefix=bus:", ""))["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["key"] != "bus:evt_9" {
		t.Errorf("list keeps non-alert keys and honours prefix: %v", list)
	}
	// An alert id still changes the alert itself.
	if w := call(t, h, "PUT", StatePrefix+"/al_checkpoint_01", `{"type":"read"}`); w.Code != 200 {
		t.Errorf("alert key: %d", w.Code)
	}
}

func TestStreamReplaysAndResumes(t *testing.T) {
	h := NewStub().Handler()
	all := call(t, h, "GET", Prefix+"/stream?once=1", "").Body.String()
	for _, ty := range []string{"alert.created", "alert.updated", "alert.woke", "alert.escalated", "alert.state", "counts", "prefs"} {
		if !strings.Contains(all, `"type":"`+ty+`"`) {
			t.Errorf("stream is missing %s", ty)
		}
	}
	first := strings.SplitN(all, "\n", 2)[0] // "id: N"
	id := strings.TrimPrefix(first, "id: ")
	rest := call(t, h, "GET", Prefix+"/stream?once=1", "", "Last-Event-ID", id).Body.String()
	if strings.Contains(rest, `"type":"alert.created"`) || !strings.Contains(rest, `"type":"alert.updated"`) {
		t.Errorf("resume after %s replayed wrongly:\n%s", id, rest)
	}
	for _, c := range [][]string{{"X-Stub-Scenario", "gap"}} {
		if g := call(t, h, "GET", Prefix+"/stream?once=1", "", c...).Body.String(); !strings.Contains(g, `"type":"resync"`) {
			t.Errorf("gap did not resync: %s", g)
		}
	}
	if g := call(t, h, "GET", Prefix+"/stream?once=1&after=99999999", "").Body.String(); !strings.Contains(g, `"type":"resync"`) {
		t.Error("cursor beyond the ring must resync")
	}
	if w := call(t, h, "GET", Prefix+"/stream?once=1", "", "Last-Event-ID", "x"); w.Code != 400 {
		t.Errorf("bad Last-Event-ID: %d", w.Code)
	}
}

func TestRulePackAndConformanceAgree(t *testing.T) {
	var pack struct {
		Rules []struct {
			ID string `json:"id"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(DefaultRulePack(), &pack); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range pack.Rules {
		ids[r.ID] = true
	}
	var conf struct {
		Cases []struct {
			Name   string `json:"name"`
			Expect struct {
				RuleID *string `json:"rule_id"`
			} `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(Conformance(), &conf); err != nil {
		t.Fatal(err)
	}
	for _, c := range conf.Cases {
		if c.Expect.RuleID != nil && !ids[*c.Expect.RuleID] {
			t.Errorf("case %q names unknown rule %q", c.Name, *c.Expect.RuleID)
		}
	}
}

func TestSafeHref(t *testing.T) {
	for href, want := range map[string]bool{"/tasks/1": true, "https://github.com/x": true, "//evil.test": false, "javascript:alert(1)": false, "/a\\b": false, "": false} {
		if SafeHref(href) != want {
			t.Errorf("SafeHref(%q) != %v", href, want)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func contains(list map[string]any, id string) bool {
	for _, a := range list["alerts"].([]any) {
		if a.(map[string]any)["alert_id"] == id {
			return true
		}
	}
	return false
}

func TestDecorate(t *testing.T) {
	a := Alert{Kind: "approval.requested", Title: "Deploy it", Source: "approvals",
		Subjects: []Subject{{Kind: "approval", Ref: "it1", Label: "Deploy it"}, {Kind: "task", Ref: "CW-1", URL: "/planner/CW-1"}, {Kind: "task", Ref: "CW-2", URL: "https://x.test/"}}}
	Decorate(&a)
	if a.SourceLabel != "Approvals" || a.DisplayTitle != "Needs your decision: Deploy it" || a.SubjectView == nil || a.SubjectView.Kind != "task" || a.SubjectView.Label != "CW-1" {
		t.Fatalf("%+v", a)
	}
	if len(a.Links) != 3 || a.Links[1].Href != "/planner/CW-1" || a.Links[2].Href != "" {
		t.Fatalf("links: %+v", a.Links)
	}
	b := Alert{Kind: "alert.raised", Title: "Disk full", Source: "build_farm"}
	Decorate(&b)
	if b.SourceLabel != "Build farm" || b.DisplayTitle != "" || b.SubjectView != nil || b.Links != nil {
		t.Fatalf("%+v", b)
	}
}

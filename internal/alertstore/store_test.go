package alertstore

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

var testPack = []byte(`{"version":1,"id":"t","rules":[
 {"id":"need","match":{"kinds":["need"]},"alert":{"title":"Needs {{kind}}","severity":"warning","needsYou":true}},
 {"id":"any","match":{"kinds":["info","other"]},"alert":{"title":"Heads up","severity":"info"}}]}`)

type fixture struct {
	s   *Store
	api http.Handler
	now *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pack, _ := alertsrules.ParsePack(testPack)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s := &Store{DB: db, Pack: func() alertsrules.Pack { return pack }, Now: func() time.Time { return now }}
	api := &API{Store: s, Prefs: SettingsPrefs{DB: db}, TZ: "UTC"}
	return &fixture{s: s, api: api.Handler(), now: &now}
}

func ev(kind, id string, at int64) alertsource.Event {
	return alertsource.Event{Source: "torque", SourceEventID: id, Kind: kind, AtMS: at,
		Subjects: []alertsapi.Subject{{Kind: "pr", Ref: "x", Label: "PR x", URL: "https://github.com/o/r/pull/1"}}}
}

func (f *fixture) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if method != "GET" {
		r.Header.Set("Origin", "http://"+r.Host)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	return w
}

func TestIngestDedupes(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		if err := f.s.Ingest(ev("need", "e1", 1000)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.Ingest(ev("unmatched", "e2", 1000)); err != nil {
		t.Fatal(err)
	}
	c, _ := f.s.Counts(context.Background())
	if c.Active != 1 || c.Unread != 1 || c.NeedsYou != 1 || c.Seq != 1 {
		t.Fatalf("%+v", c)
	}
	id := AlertID(alertsource.DedupeKey(ev("need", "e1", 1000)))
	a, err := f.s.Get(context.Background(), id)
	if err != nil || a.Title != "Needs need" || a.Primary == nil || a.Version != 1 {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestOpsVersionConflict(t *testing.T) {
	f := newFixture(t)
	f.s.Ingest(ev("need", "e1", 1000))
	id := AlertID(alertsource.DedupeKey(ev("need", "e1", 1000)))
	w := f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"read","expected_version":1}`, nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"dismiss","expected_version":1}`, nil)
	var body struct {
		Code    string
		Current alertsapi.Alert
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 409 || body.Code != alertsapi.CodeVersionConflict || body.Current.Version != 2 || !body.Current.Read {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"snooze","preset":"1h","expected_version":2}`, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"snoozed"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"settle","expected_version":3}`, nil); !strings.Contains(w.Body.String(), `"resolution":"Handled"`) {
		t.Fatalf("%s", w.Body)
	}
	if w = f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"reopen","expected_version":4}`, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"active"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = f.do("POST", alertsapi.Prefix+"/nope/ops", `{"op":"read","expected_version":1}`, nil); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
	if w = f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"read"}`, nil); w.Code != 400 {
		t.Fatalf("missing expected_version: %d", w.Code)
	}
	if w = f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"read","expected_version":5,"x":1}`, nil); w.Code != 400 {
		t.Fatalf("unknown field: %d", w.Code)
	}
}

func TestBulkSeqConflict(t *testing.T) {
	f := newFixture(t)
	f.s.Ingest(ev("need", "e1", 1000))
	f.s.Ingest(ev("info", "e2", 2000))
	w := f.do("POST", alertsapi.Prefix+"/ops/bulk", `{"op":"read","scope":"all_active","expected_seq":1}`, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), alertsapi.CodeSeqConflict) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = f.do("POST", alertsapi.Prefix+"/ops/bulk", `{"op":"read","scope":"all_active","expected_seq":2}`, nil)
	var out struct {
		Updated []string
		Counts  alertsapi.Counts
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Updated) != 2 || out.Counts.Unread != 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = f.do("POST", alertsapi.Prefix+"/ops/bulk", `{"op":"settle","scope":"all_active"}`, nil); w.Code != 400 {
		t.Fatalf("bad op %d", w.Code)
	}
	if w = f.do("POST", alertsapi.Prefix+"/ops/bulk", `{"op":"dismiss","scope":"all_active"}`, nil); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if c, _ := f.s.Counts(context.Background()); c.Active != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestListFiltersAndCursor(t *testing.T) {
	f := newFixture(t)
	for i, k := range []string{"need", "info", "other"} {
		f.s.Ingest(ev(k, string(rune('a'+i)), int64(1000*(i+1))))
	}
	get := func(q string) (alerts []alertsapi.Alert, next any, code int) {
		w := f.do("GET", alertsapi.Prefix+"?"+q, "", nil)
		var b struct {
			Alerts []alertsapi.Alert
			Next   any
		}
		json.Unmarshal(w.Body.Bytes(), &b)
		return b.Alerts, b.Next, w.Code
	}
	if a, _, _ := get(""); len(a) != 3 || a[0].LastAtMS != 3000 {
		t.Fatalf("%+v", a)
	}
	if a, _, _ := get("needs_you=1"); len(a) != 1 {
		t.Fatalf("needs_you %d", len(a))
	}
	if a, _, _ := get("min_severity=warning"); len(a) != 1 {
		t.Fatalf("min_severity %d", len(a))
	}
	if a, _, _ := get("q=Heads%25"); len(a) != 0 {
		t.Fatalf("LIKE wildcard must be escaped, got %d", len(a))
	}
	if a, _, _ := get("q=heads"); len(a) != 2 {
		t.Fatalf("q %d", len(a))
	}
	a, next, _ := get("limit=2")
	if len(a) != 2 || next == nil {
		t.Fatalf("page 1: %d %v", len(a), next)
	}
	b, next2, _ := get("limit=2&cursor=" + next.(string))
	if len(b) != 1 || next2 != nil || b[0].LastAtMS != 1000 {
		t.Fatalf("page 2: %+v %v", b, next2)
	}
	for _, bad := range []string{"bogus=1", "limit=0", "limit=201", "needs_you=2", "view=x", "min_severity=loud"} {
		if _, _, code := get(bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	id := a[0].AlertID
	f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"dismiss","expected_version":1}`, nil)
	if h, _, _ := get("view=history"); len(h) != 1 {
		t.Fatalf("history %d", len(h))
	}
	if act, _, _ := get(""); len(act) != 2 {
		t.Fatalf("active %d", len(act))
	}
}

func TestDetail(t *testing.T) {
	f := newFixture(t)
	f.s.Ingest(ev("need", "e1", 1000))
	id := AlertID(alertsource.DedupeKey(ev("need", "e1", 1000)))
	w := f.do("GET", alertsapi.Prefix+"/"+id, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"timeline"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = f.do("GET", alertsapi.Prefix+"/al_missing", "", nil); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

func TestPrefs(t *testing.T) {
	f := newFixture(t)
	w := f.do("GET", alertsapi.Prefix+"/prefs", "", nil)
	var p alertsapi.Prefs
	json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || p.QuietHours.TZ != "UTC" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	legacy := `{"quiet":true,"toastMinSeverity":"error","bellMinSeverity":"info","mutedSources":["x"]}`
	w = f.do("POST", alertsapi.Prefix+"/prefs/migrate", legacy, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"applied":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = f.do("POST", alertsapi.Prefix+"/prefs/migrate", `{"quiet":false}`, nil)
	if !strings.Contains(w.Body.String(), `"applied":false`) || !strings.Contains(w.Body.String(), `"quiet":true`) {
		t.Fatalf("second migrate must not overwrite: %s", w.Body)
	}
	p.Quiet = false
	b, _ := json.Marshal(p)
	if w = f.do("PUT", alertsapi.Prefix+"/prefs", string(b), nil); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = f.do("PUT", alertsapi.Prefix+"/prefs", `{"notify_level":"loud"}`, nil); w.Code != 400 {
		t.Fatalf("invalid prefs: %d", w.Code)
	}
}

func TestGuards(t *testing.T) {
	f := newFixture(t)
	f.s.Ingest(ev("need", "e1", 1000))
	id := AlertID(alertsource.DedupeKey(ev("need", "e1", 1000)))
	body := `{"op":"read","expected_version":1}`
	r := httptest.NewRequest("POST", alertsapi.Prefix+"/"+id+"/ops", strings.NewReader(body))
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("no Origin: %d", w.Code)
	}
	r = httptest.NewRequest("POST", alertsapi.Prefix+"/"+id+"/ops", strings.NewReader(body))
	r.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross origin: %d", w.Code)
	}
	if w := f.do("POST", alertsapi.Prefix+"/"+id+"/ops", body, map[string]string{"Authorization": "Bearer x"}); w.Code != 403 {
		t.Fatalf("authorization header: %d", w.Code)
	}
	if w := f.do("GET", alertsapi.Prefix+"", "", map[string]string{"Authorization": "Bearer x"}); w.Code != 403 {
		t.Fatalf("authorization on read: %d", w.Code)
	}
}

func TestCursorAndPrune(t *testing.T) {
	f := newFixture(t)
	if v, _ := f.s.Cursor("torque"); v != "" {
		t.Fatal(v)
	}
	f.s.SetCursor("torque", "42")
	f.s.SetCursor("torque", "43")
	if v, _ := f.s.Cursor("torque"); v != "43" {
		t.Fatal(v)
	}
	f.s.Ingest(ev("need", "e1", 1000))
	id := AlertID(alertsource.DedupeKey(ev("need", "e1", 1000)))
	f.do("POST", alertsapi.Prefix+"/"+id+"/ops", `{"op":"dismiss","expected_version":1}`, nil)
	*f.now = f.now.Add(AlertRetention + time.Hour)
	if err := f.s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Get(context.Background(), id); err != ErrNotFound {
		t.Fatalf("terminal alert past retention must go: %v", err)
	}
}

func TestSuppressedRaisesOnePerMinute(t *testing.T) {
	f := newFixture(t)
	f.s.Suppressed("torque", 5)
	f.s.Suppressed("torque", 7)
	c, _ := f.s.Counts(context.Background())
	if c.Active != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestStoredAlertsAreSecretFree(t *testing.T) {
	f := newFixture(t)
	e := ev("need", "e1", 1000)
	e.Subjects[0].URL = "javascript:alert(1)"
	e.Subjects[0].Label = "tok\x00en\nline"
	f.s.Ingest(e)
	w := f.do("GET", alertsapi.Prefix+"", "", nil)
	if strings.Contains(w.Body.String(), "javascript:") || strings.ContainsAny(w.Body.String(), "\x00") {
		t.Fatalf("unsafe content stored: %s", w.Body)
	}
}

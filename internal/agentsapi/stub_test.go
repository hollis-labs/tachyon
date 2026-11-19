package agentsapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func do(t *testing.T, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://x"+Prefix+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	return w
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
	n := 0
	err := fs.WalkDir(assets, "fixtures", func(p string, d fs.DirEntry, err error) error {
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
	if err != nil || n == 0 {
		t.Fatal("no fixtures", err)
	}
}

func TestReadRoutesServeFinalShapes(t *testing.T) {
	for _, p := range []string{"/sessions", "/sessions/sess_work", "/sessions/sess_work/events", "/sessions/sess_work/events?before_seq=1906000",
		"/sessions/sess_work/context-series", "/sessions/sess_work/tokens", "/sessions/sess_work/history", "/bus/events", "/bus/events?type=failures",
		"/collector", "/controls/req_restart_waiting", "/settings", "/openapi.yaml"} {
		w := do(t, "GET", p, "")
		if w.Code != 200 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/sessions/nope", "/controls/nope", "/sessions/nope/tokens"} {
		if w := do(t, "GET", p, ""); w.Code != 404 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	if do(t, "GET", "/sessions?state=bogus", "").Code != 400 || do(t, "GET", "/bus/events?type=bogus", "").Code != 400 {
		t.Error("bad filters must be 400")
	}
	if do(t, "GET", "/sessions", "", "X-Stub-Status", "503").Code != 503 {
		t.Error("X-Stub-Status not honoured")
	}
}

func TestSessionFixturesCoverTheStatesAndNeverFakeZero(t *testing.T) {
	var d sessionsDoc
	if !load("sessions", &d) {
		t.Fatal("sessions fixture")
	}
	states, reasons := map[string]bool{}, map[string]bool{}
	waitingStale, unmetered, nullCtx := false, false, false
	for _, s := range d.Sessions {
		states[s["state"].(string)] = true
		if r, _ := s["stale_reason"].(string); r != "" {
			reasons[r] = true
			if s["state"] != StateStale {
				t.Errorf("%v: stale_reason on a %v session", s["id"], s["state"])
			}
			if nu, _ := s["needs_you"].(map[string]any); nu["reason"] != nil {
				waitingStale = true
			}
		} else if s["state"] == StateStale {
			t.Errorf("%v: stale without a reason", s["id"])
		}
		if tk := s["tokens"].(map[string]any); tk["metered"] == false {
			unmetered = true
			if tk["input"] != nil || tk["output"] != nil {
				t.Errorf("%v: unmetered session reports tokens", s["id"])
			}
		}
		if c := s["context"].(map[string]any); c["used"] == nil {
			nullCtx = true
		}
		if s["context"].(map[string]any)["breakdown"] != nil {
			t.Errorf("%v: breakdown has no source and must be null", s["id"])
		}
	}
	for _, st := range []string{StateWorking, StateWaiting, StateIdle, StateStale, StateUnknown} {
		if !states[st] {
			t.Errorf("no %s session fixture", st)
		}
	}
	for _, r := range []string{StaleNoEvents, StaleNotAlive, StaleWorkerOffline} {
		if !reasons[r] {
			t.Errorf("no stale_reason %s fixture", r)
		}
	}
	if !waitingStale || !unmetered || !nullCtx {
		t.Errorf("missing fixtures: waiting-stale=%v unmetered=%v null-context=%v", waitingStale, unmetered, nullCtx)
	}
}

func TestEveryControlStateHasAFixture(t *testing.T) {
	have := map[string]bool{}
	for _, c := range controlFixtures() {
		have[c["kind"].(string)+"/"+c["state"].(string)] = true
	}
	for _, k := range []string{"nudge/queued", "nudge/delivered", "nudge/interrupted_then_delivered", "nudge/failed",
		"restart/requested", "restart/handoff_sent", "restart/waiting", "restart/stopping", "restart/stopped", "restart/resuming", "restart/done", "restart/failed"} {
		if !have[k] {
			t.Errorf("no control fixture %s", k)
		}
	}
}

func TestNudgeAndRestartValidate(t *testing.T) {
	ok := `{"request_id":"r1","text":"run the tests"}`
	if w := do(t, "POST", "/sessions/sess_work/nudge", ok); w.Code != 202 {
		t.Errorf("nudge: %d", w.Code)
	}
	if w := do(t, "POST", "/sessions/sess_work/nudge", `{"request_id":"r1","text":"x","interrupt":true}`); w.Code != 202 || !strings.Contains(w.Body.String(), "interrupted_then_delivered") {
		t.Errorf("interrupt nudge: %d %s", w.Code, w.Body)
	}
	for name, c := range map[string]struct {
		path, body string
		want       int
	}{
		"no request id":       {"/sessions/sess_work/nudge", `{"text":"x"}`, 400},
		"empty text":          {"/sessions/sess_work/nudge", `{"request_id":"r","text":"  "}`, 400},
		"too long":            {"/sessions/sess_work/nudge", `{"request_id":"r","text":"` + strings.Repeat("a", MaxNudgeText+1) + `"}`, 400},
		"not json":            {"/sessions/sess_work/nudge", `nope`, 400},
		"pty cannot nudge":    {"/sessions/sess_pty/nudge", ok, 409},
		"no silent downgrade": {"/sessions/sess_stale_dead/nudge", `{"request_id":"r","text":"x","interrupt":true}`, 409},
		"unknown session":     {"/sessions/nope/nudge", ok, 404},
		"restart wrong id":    {"/sessions/sess_work/restart", `{"request_id":"r","expected_session_id":"sess_other"}`, 409},
		"restart no id":       {"/sessions/sess_work/restart", `{"request_id":"r"}`, 400},
		"restart unsupported": {"/sessions/sess_pty/restart", `{"request_id":"r","expected_session_id":"sess_pty"}`, 409},
	} {
		if w := do(t, "POST", c.path, c.body); w.Code != c.want {
			t.Errorf("%s: %d want %d (%s)", name, w.Code, c.want, w.Body)
		}
	}
	if w := do(t, "POST", "/sessions/sess_work/restart", `{"request_id":"r","handoff":true,"expected_session_id":"sess_work"}`); w.Code != 202 {
		t.Errorf("restart: %d", w.Code)
	}
	if do(t, "POST", "/controls/req_restart_waiting/skip", "").Code != 200 || do(t, "POST", "/controls/req_nudge_queued/skip", "").Code != 409 {
		t.Error("skip applies to restarts only")
	}
}

func TestSettingsValidate(t *testing.T) {
	if w := do(t, "PUT", "/settings", `{"stale_after_s":900,"handoff_prompt":"p","handoff_timeout_s":300,"nudge_presets":["a"]}`); w.Code != 200 {
		t.Errorf("put: %d", w.Code)
	}
	if w := do(t, "PUT", "/settings", `{"stale_after_s":5,"handoff_prompt":"p","handoff_timeout_s":300}`); w.Code != 400 {
		t.Errorf("stale_after_s too small: %d", w.Code)
	}
}

func TestStreamReplaysAfterLastEventID(t *testing.T) {
	all := do(t, "GET", "/stream?once=1", "")
	if all.Code != 200 || !strings.HasPrefix(all.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", all.Code, all.Header().Get("Content-Type"))
	}
	for _, typ := range []string{"collector", "session", "summary", "bus", "control", "gap"} {
		if !strings.Contains(all.Body.String(), `"type":"`+typ+`"`) {
			t.Errorf("stream has no %s event", typ)
		}
	}
	resumed := do(t, "GET", "/stream?once=1", "", "Last-Event-ID", "103")
	body := resumed.Body.String()
	if strings.Contains(body, "id: 101") || strings.Contains(body, "id: 103") || !strings.Contains(body, "id: 104") {
		t.Errorf("resume after 103:\n%s", body)
	}
	if strings.Contains(body, `"type":"session"`) || strings.Contains(body, `"type":"summary"`) {
		t.Error("live snapshots must not replay on resume")
	}
	if do(t, "GET", "/stream?once=1", "", "Last-Event-ID", "x").Code != http.StatusBadRequest {
		t.Error("bad Last-Event-ID must be 400")
	}
	for _, typ := range []string{EventBus, EventControl, EventGap} {
		if !IDed(typ) {
			t.Errorf("%s must be replayable", typ)
		}
	}
	if IDed(EventSession) || IDed(EventSummary) || IDed(EventCollector) {
		t.Error("snapshots must not be replayable")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	if w := do(t, "GET", "/nope", ""); w.Code != 404 {
		t.Errorf("unknown: %d", w.Code)
	}
}

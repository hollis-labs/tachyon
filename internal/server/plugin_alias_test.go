package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestReviewerPreviewReadRouteGuard(t *testing.T) {
	checks := 0
	elevated := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		if elevated {
			w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
		} else {
			w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
		}
	}))
	defer upstream.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL})
	paths := []string{
		"/api/plugins/console/v1/tether/plans/cp_928c2c714c131ae1c23ff24b",
		"/api/plugins/console/v1/tether/agents/agt_abcde12345",
		"/api/plugins/console/v1/tether/plans%2fcp_928c2c714c131ae1c23ff24b",
		"/api/plugins/console/v1/tether/agents%2Fagt_abcde12345",
		"/api/plugins/console%2fv1/tether/plans/cp_928c2c714c131ae1c23ff24b",
		"/api/plugins/console/v1%2ftether/plans/cp_928c2c714c131ae1c23ff24b",
		"/api/plugins/console/v1/tether%2fplans/cp_928c2c714c131ae1c23ff24b",
	}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD"} {
			checks = 0
			r := httptest.NewRequest(method, "https://tachyon.test"+path, nil)
			r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
			w := httptest.NewRecorder()
			s.Routes().ServeHTTP(w, r)
			if w.Code != 403 || checks != 1 {
				t.Errorf("%s %s: status=%d authorityChecks=%d decoded=%s", method, path, w.Code, checks, r.URL.Path)
			}
		}
	}
	// An elevated operator is not refused by the guard under any spelling.
	elevated = true
	for _, path := range paths {
		r := httptest.NewRequest("GET", "https://tachyon.test"+path, nil)
		r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, r)
		if w.Code == 403 {
			t.Errorf("elevated GET %s refused by the guard", path)
		}
	}
}

// Encoded aliases of the other guarded console routes get the same checks as the literal spelling.
func TestEncodedAliasesOfGuardedMutationsAreGuarded(t *testing.T) {
	checks := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
	}))
	defer upstream.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL})
	for _, path := range []string{"/api/plugins/console/v1/tether%2fprojects", "/api/plugins/console%2Fv1/tether/projects", "/api/plugins%2fconsole/v1/tether/projects"} {
		for _, method := range []string{"POST", "PATCH", "DELETE"} {
			checks = 0
			r := httptest.NewRequest(method, "https://tachyon.test"+path, nil)
			r.Header.Set("Origin", "https://tachyon.test")
			r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
			w := httptest.NewRecorder()
			s.Routes().ServeHTTP(w, r)
			if w.Code != 403 || checks != 1 {
				t.Errorf("%s %s: status=%d checks=%d", method, path, w.Code, checks)
			}
		}
	}
}

// The cite overlay's Torque reads are operator-gated like the Tether plan/agent reads, in every spelling.
func TestTorquePreviewReadsNeedOperatorAssuranceInEverySpelling(t *testing.T) {
	checks := 0
	elevated := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		if elevated {
			w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
		} else {
			w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
		}
	}))
	defer upstream.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL})
	var paths []string
	for _, p := range []string{"epics/EP-20261023-0002", "projects/PRJ-20260917-0001", "sprints/SP-20261023-0003", "plans/PL-20261023-0004"} {
		paths = append(paths,
			"/api/plugins/console/v1/preview/"+p,
			"/api/plugins/console/v1/preview%2f"+p,
			"/api/plugins/console/v1/preview/"+strings.Replace(p, "/", "%2F", 1),
			"/api/plugins/console%2fv1/preview/"+p,
			"/api/plugins/console/v1%2fpreview/"+p,
			"/api/plugins%2fconsole/v1/preview/"+p,
		)
	}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD"} {
			checks = 0
			r := httptest.NewRequest(method, "https://tachyon.test"+path, nil)
			r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
			w := httptest.NewRecorder()
			s.Routes().ServeHTTP(w, r)
			if w.Code != 403 || checks != 1 {
				t.Errorf("%s %s: status=%d authorityChecks=%d", method, path, w.Code, checks)
			}
		}
		// No operator session at all is refused too, before anything is forwarded.
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+path, nil))
		if w.Code != 401 && w.Code != 403 {
			t.Errorf("no session %s: status=%d", path, w.Code)
		}
	}
	elevated = true
	for _, path := range paths {
		r := httptest.NewRequest("GET", "https://tachyon.test"+path, nil)
		r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, r)
		if w.Code == 401 || w.Code == 403 {
			t.Errorf("elevated GET %s refused by the guard: %d", path, w.Code)
		}
	}
}

func TestChatStubRoutesRequireOperator(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	on := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL, ChatStubs: true}).Routes()
	off := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	cases := []struct{ method, path string }{
		{"POST", "/api/plugins/console/v1/chat/threads"},
		{"PUT", "/api/plugins/console/v1/chat/threads/app:t_01/scratchpad"},
		{"DELETE", "/api/plugins/console/v1/chat/threads/app:t_01/topics/x"},
		{"GET", "/api/plugins/console/v1/chat/threads/app:t_01/workspace/x/content"},
		{"GET", "/api/plugins/console/v1/terminal/machines"},
		{"POST", "/api/plugins/console/v1/terminal/proposals/x/run"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "https://tachyon.test"+c.path, nil)
		r.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		on.ServeHTTP(w, r)
		if (w.Code != 401 && w.Code != 403) || w.Header().Get("X-Tachyon-Chat-Stub") != "" {
			t.Errorf("%s %s with stubs on and no operator session: %d", c.method, c.path, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest(c.method, "https://tachyon.test"+c.path, nil))
		if w.Header().Get("X-Tachyon-Chat-Stub") != "" {
			t.Errorf("%s %s served a stub with the flag off", c.method, c.path)
		}
	}
	// An open GET is served by the stub without a session.
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test/api/plugins/console/v1/chat/threads", nil))
	if w.Code != 200 || w.Header().Get("X-Tachyon-Chat-Stub") != "1" {
		t.Errorf("stub list: %d", w.Code)
	}
}

func TestAgentsStubRoutesRequireOperator(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	on := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL, AgentsStubs: true}).Routes()
	off := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	cases := []struct{ method, path string }{
		{"POST", "/api/plugins/console/v1/agents/sessions/sess_work/nudge"},
		{"POST", "/api/plugins/console/v1/agents/sessions/sess_work/restart"},
		{"POST", "/api/plugins/console/v1/agents/controls/req_restart_waiting/skip"},
		{"PUT", "/api/plugins/console/v1/agents/settings"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		on.ServeHTTP(w, r)
		if (w.Code != 401 && w.Code != 403) || w.Header().Get("X-Tachyon-Agents-Stub") != "" {
			t.Errorf("%s %s with stubs on and no operator session: %d", c.method, c.path, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`)))
		if w.Header().Get("X-Tachyon-Agents-Stub") != "" {
			t.Errorf("%s %s served a stub with the flag off", c.method, c.path)
		}
	}
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test/api/plugins/console/v1/agents/sessions", nil))
	if w.Code != 200 || w.Header().Get("X-Tachyon-Agents-Stub") != "1" {
		t.Errorf("stub list: %d", w.Code)
	}
}

func TestAgentsSessionRoutesAreRealAndWritesRequireOperator(t *testing.T) {
	store, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	// Stubs on as well: the real route must win over the stub prefix mount.
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL, AgentsStubs: true})
	s.store = store
	s.agentsCollector = &agentscollector.Collector{Store: &agentscollector.Store{DB: store}}
	h := s.Routes()
	const a = "/api/plugins/console/v1/agents"
	for _, p := range []string{a + "/sessions", a + "/settings", a + "/collector", a + "/bus/events"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+p, nil))
		if w.Code != 200 || w.Header().Get("X-Tachyon-Agents-Stub") != "" {
			t.Errorf("GET %s: %d stub=%q", p, w.Code, w.Header().Get("X-Tachyon-Agents-Stub"))
		}
	}
	req := httptest.NewRequest("PUT", "https://tachyon.test"+a+"/settings", strings.NewReader(`{"stale_after_s":900,"handoff_prompt":"p","handoff_timeout_s":120,"nudge_presets":[]}`))
	req.Header.Set("Origin", "https://tachyon.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 && w.Code != 403 {
		t.Errorf("PUT settings without an operator session: %d", w.Code)
	}
	var n int
	_ = store.Reader.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key='agents.settings'`).Scan(&n)
	if n != 0 {
		t.Error("a refused PUT stored settings")
	}
	// Controls are still the stub's: they answer from fixtures and refuse without a session.
	req = httptest.NewRequest("POST", "https://tachyon.test"+a+"/sessions/sess_work/nudge", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://tachyon.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 && w.Code != 403 {
		t.Errorf("nudge without an operator session: %d", w.Code)
	}
}

func TestAlertsStubIsMountedOnlyWhenEnabled(t *testing.T) {
	on := New(Config{PluginBinDir: t.TempDir(), AlertsStubs: true}).Routes()
	off := New(Config{PluginBinDir: t.TempDir()}).Routes()
	base := "https://tachyon.test/api/plugins/console/v1"
	for _, p := range []string{"/alerts", "/alerts/counts", "/alerts/al_approval_01", "/console/state"} {
		w := httptest.NewRecorder()
		on.ServeHTTP(w, httptest.NewRequest("GET", base+p, nil))
		if w.Code != 200 || w.Header().Get("X-Tachyon-Alerts-Stub") != "1" {
			t.Errorf("GET %s with the flag on: %d", p, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest("GET", base+p, nil))
		if w.Header().Get("X-Tachyon-Alerts-Stub") != "" {
			t.Errorf("GET %s served a stub with the flag off", p)
		}
	}
	for _, c := range []struct{ method, path string }{{"POST", "/alerts/al_approval_01/ops"}, {"POST", "/alerts/ops/bulk"}, {"PUT", "/alerts/prefs"}, {"PUT", "/console/state/al_approval_01"}} {
		w := httptest.NewRecorder()
		on.ServeHTTP(w, httptest.NewRequest(c.method, base+c.path, strings.NewReader(`{}`)))
		if w.Code != 403 {
			t.Errorf("%s %s without a same-origin Origin: %d", c.method, c.path, w.Code)
		}
	}
}

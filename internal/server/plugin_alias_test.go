package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
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

func TestBuilderStubRoutesRequireOperator(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	on := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL, BuilderStubs: true}).Routes()
	off := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	const b, r0 = "/api/plugins/console/v1/builder", "/api/plugins/console/v1/registry"
	cases := []struct{ method, path string }{
		{"POST", b + "/changes"},
		{"POST", b + "/changes/chg_draft/submit"},
		{"POST", b + "/changes/chg_draft/cancel"},
		{"POST", b + "/changes/chg_expired/rearm"},
		{"POST", b + "/launches/builder-reviewer-tachyon/launch"},
		{"PATCH", r0 + "/projects/tachyon"},
		{"PUT", r0 + "/projects/tachyon"},
		{"DELETE", r0 + "/projects/tachyon"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		on.ServeHTTP(w, req)
		if (w.Code != 401 && w.Code != 403) || w.Header().Get("X-Tachyon-Builder-Stub") != "" {
			t.Errorf("%s %s with stubs on and no operator session: %d", c.method, c.path, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`)))
		if w.Header().Get("X-Tachyon-Builder-Stub") != "" {
			t.Errorf("%s %s served a stub with the flag off", c.method, c.path)
		}
	}
	for _, p := range []string{b + "/summary", b + "/agents", r0 + "/projects", r0 + "/projects/tachyon"} {
		w := httptest.NewRecorder()
		on.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+p, nil))
		if w.Code != 200 || w.Header().Get("X-Tachyon-Builder-Stub") != "1" {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
}

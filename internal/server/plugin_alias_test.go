package server

import (
	"net/http"
	"net/http/httptest"
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

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
)

func TestTetherObjectMutationsRequireOperatorAssurance(t *testing.T) {
	checks := 0
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
	}))
	defer auth.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL})
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		req := httptest.NewRequest(method, "https://tachyon.test/api/plugins/console/v1/tether/projects", nil)
		req.Header.Set("Origin", "https://tachyon.test")
		req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s escaped guard: %d %s", method, w.Code, w.Body.String())
		}
	}
	if checks == 0 {
		t.Fatal("operator authority was never checked")
	}
}

func TestTetherPlanAndAgentReadsRequireOperatorAssurance(t *testing.T) {
	elevated := false
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if elevated {
			_, _ = w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
	}))
	defer auth.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL})
	paths := []string{"/plans/cp_928c2c714c131ae1c23ff24b", "/agents/agt_abcde12345"}
	get := func(method, p string, hdr map[string]string) int {
		req := httptest.NewRequest(method, "https://tachyon.test/api/plugins/console/v1/tether"+p, nil)
		req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "test"})
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, req)
		return w.Code
	}
	for _, p := range paths {
		for _, m := range []string{"GET", "HEAD"} {
			if code := get(m, p, nil); code != 403 {
				t.Fatalf("%s %s without operator assurance: %d", m, p, code)
			}
		}
		if code := get("GET", p, map[string]string{"Sec-Fetch-Site": "cross-site"}); code != 403 {
			t.Fatalf("cross-site read of %s: %d", p, code)
		}
	}
	elevated = true
	for _, p := range paths {
		if code := get("GET", p, nil); code == 403 {
			t.Fatalf("elevated read of %s refused by the guard", p)
		}
	}
}

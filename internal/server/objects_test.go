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

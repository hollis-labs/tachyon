package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
)

func TestTrustedAssuranceRoutesFailClosedWithoutConfiguration(t *testing.T) {
	s := New(Config{PluginBinDir: t.TempDir()})
	handler := s.Routes()
	for _, operation := range []string{"item", "complete", "outcome"} {
		r := httptest.NewRequest("POST", approvals.TrustedPrefix+"/"+operation, strings.NewReader(`{"item_id":"test"}`))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "assurance_unconfigured") {
			t.Fatalf("trusted route fell through: %d %s", w.Code, w.Body)
		}
	}
}

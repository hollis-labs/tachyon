package pluginallow

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOnlyListedPluginPathsPass(t *testing.T) {
	reached := ""
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r.Method + " " + r.URL.Path })
	h := Handler([]Entry{{"GET", "/api/plugins/console/v1/tasks/{id}"}}, next)
	cases := []struct {
		method, path string
		pass         bool
	}{
		{"GET", "/api/plugins/console/v1/tasks/T-1", true},
		{"POST", "/api/plugins/console/v1/tasks/T-1", false},
		{"GET", "/api/plugins/console/v1/tasks/T-1/comments", false},
		{"GET", "/api/plugins/console/v1/auth/step-up", false},
		{"DELETE", "/api/plugins/console/v1/anything", false},
		{"GET", "/api/plugins/", false},
		{"GET", "/api/identity/profile", true},
		{"GET", "/healthz", true},
		{"GET", "/api/plugins", true}, // not under the prefix: the host's own 404 or route applies
	}
	for _, c := range cases {
		reached = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if got := reached != ""; got != c.pass {
			t.Errorf("%s %s: reached=%v (status %d), want %v", c.method, c.path, got, rec.Code, c.pass)
		}
		if !c.pass && (rec.Code != http.StatusNotFound || rec.Body.String() != "{\"code\":\"not_found\",\"message\":\"no such route\"}\n") {
			t.Errorf("%s %s: %d %q", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

func TestAnEmptyListClosesThePrefix(t *testing.T) {
	h := Handler(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached the host") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/plugins/console/v1/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

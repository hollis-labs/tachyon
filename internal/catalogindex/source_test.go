package catalogindex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLastPlanToleratesMissingHistoryRoute(t *testing.T) {
	status := 404
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog/history" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		if status == 200 {
			_, _ = w.Write([]byte(`{"plans":[{"plan_id":"plan_1"}]}`))
		}
	}))
	defer srv.Close()
	src, err := NewHTTPSource(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := src.LastPlan(context.Background()); err != nil || id != "" {
		t.Errorf("404 is no history, not an error: %q %v", id, err)
	}
	status = 500
	if _, err := src.LastPlan(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing route must still fail: %v", err)
	}
	status = 200
	if id, err := src.LastPlan(context.Background()); err != nil || id != "plan_1" {
		t.Errorf("history present: %q %v", id, err)
	}
}

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlannerStubServesOnlyNewRoutesWhenEnabled(t *testing.T) {
	off := New(Config{PluginBinDir: t.TempDir()}).Routes()
	t.Setenv("TACHYON_PLANNER_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir()}).Routes()
	p := "https://tachyon.test/api/plugins/console/v1/planner/need-you"
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	if w.Code != 200 || w.Header().Get("X-Tachyon-Planner-Stub") != "1" {
		t.Errorf("stub need-you: %d", w.Code)
	}
	w = httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("POST", p, strings.NewReader(`{}`)))
	if w.Header().Get("X-Tachyon-Planner-Stub") == "1" {
		t.Error("stub answered a method it does not serve")
	}
	w = httptest.NewRecorder()
	off.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	if w.Header().Get("X-Tachyon-Planner-Stub") != "" {
		t.Error("stub served with the flag off")
	}
}

func TestPlannerStubOwnsAssignmentsWithoutADuplicatePattern(t *testing.T) {
	p := "https://tachyon.test/api/plugins/console/v1/planner/assignments"
	t.Setenv("TACHYON_PLANNER_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir()}).Routes() // would panic on a duplicate pattern
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	if w.Header().Get("X-Tachyon-Planner-Stub") != "1" {
		t.Error("stub did not own assignments")
	}
}

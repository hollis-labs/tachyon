package approvals

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevPostureElevatesWithoutKeyOrSession(t *testing.T) {
	b := &Bridge{Dev: true}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	r := httptest.NewRequest("POST", "http://tachyon.test/x", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://tachyon.test")
	if w := httptest.NewRecorder(); func() int { b.RequireOperator(ok).ServeHTTP(w, r); return w.Code }() != 204 {
		t.Fatal("dev posture should pass the operator gate with no session")
	}
	if _, st, err := b.lookup(httptest.NewRequest("GET", "http://tachyon.test/x", nil)); err != nil || !st.Elevated {
		t.Fatalf("lookup: %v %+v", err, st)
	}
	el := httptest.NewRequest("POST", Prefix+"/elevate", strings.NewReader("{}"))
	el.Header.Set("Origin", "http://"+el.Host)
	if w := httptest.NewRecorder(); func() int { b.ServeHTTP(w, el); return w.Code }() != 200 {
		t.Fatal("elevate should succeed with no key")
	}
}

func TestDevPostureOffByDefault(t *testing.T) {
	b := &Bridge{}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	r := httptest.NewRequest("POST", "http://tachyon.test/x", nil)
	r.Header.Set("Origin", "http://tachyon.test")
	w := httptest.NewRecorder()
	b.RequireOperator(ok).ServeHTTP(w, r)
	if w.Code == 204 {
		t.Fatal("must be gated when dev posture is off")
	}
}

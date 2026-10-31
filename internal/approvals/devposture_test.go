package approvals

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

func TestDevPostureIsElevatedWithoutSessionOrUpstream(t *testing.T) {
	t.Setenv(AssuranceEnv, "dev")
	b := New("") // no approval queue configured: dev must not need one
	if !b.Dev {
		t.Fatal("TACHYON_ASSURANCE=dev did not enable the dev posture")
	}
	p := &TangentProvider{Bridge: b}
	r := httptest.NewRequest(http.MethodGet, Prefix+"/inbox", nil)
	st, err := p.Require(r, assurance.ActionBatch)
	if err != nil || !st.Elevated() {
		t.Fatalf("want elevated, got %+v err=%v", st, err)
	}
	w := httptest.NewRecorder()
	wr := httptest.NewRequest(http.MethodPost, "/x", nil)
	wr.Host, wr.Header = "tachyon.test", http.Header{"Origin": {"https://tachyon.test"}}
	b.RequireOperator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, wr)
	if w.Code != 204 {
		t.Fatalf("write refused under dev posture: %d", w.Code)
	}
	el := httptest.NewRequest(http.MethodPost, Prefix+"/elevate", nil)
	el.Host, el.Header = "tachyon.test", http.Header{"Origin": {"https://tachyon.test"}}
	w = httptest.NewRecorder()
	b.ServeHTTP(w, el)
	if w.Code != 200 {
		t.Fatalf("elevate with no key: %d %s", w.Code, w.Body)
	}
}

func TestDevPostureOffByDefault(t *testing.T) {
	t.Setenv(AssuranceEnv, "")
	if New("").Dev {
		t.Fatal("dev posture must default off")
	}
}

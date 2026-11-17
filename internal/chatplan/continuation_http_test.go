package chatplan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

func TestContinuationPOSTRequiresServerProofAndCommitsExactPair(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const replacement = "22222222-2222-4222-8222-222222222222"
	s, _ := lineageSetup(t)
	old := writePlan(t, s, source, "Keep this plan")
	h := &Handler{Service: s}
	body := `{"source_session_id":"` + source + `","new_session_id":"` + replacement + `","logical_agent_id":"agent-a","idempotency_key":"continuation:` + source + `:` + replacement + `"}`
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, chatapi.Prefix+"/continuations", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.Mux().ServeHTTP(w, r)
		return w
	}
	if w := post(body); w.Code != 503 || !strings.Contains(w.Body.String(), "native_proof_unavailable") {
		t.Fatalf("missing proof seam: %d %s", w.Code, w.Body.String())
	}
	if _, err := planOf(s, source); err != nil {
		t.Fatal("unverified request moved source plan", err)
	}
	h.VerifyContinuation = func(context.Context, string, string, string) error { return errors.New("unverified") }
	if w := post(body); w.Code != 409 {
		t.Fatalf("denied proof: %d %s", w.Code, w.Body.String())
	}
	if _, err := planOf(s, source); err != nil {
		t.Fatal("denied request moved source plan", err)
	}
	var calls int
	h.VerifyContinuation = func(_ context.Context, gotSource, gotReplacement, gotAgent string) error {
		calls++
		if gotSource != source || gotReplacement != replacement || gotAgent != "agent-a" {
			t.Fatal("wrong verifier tuple")
		}
		return nil
	}
	if w := post(body); w.Code != 200 || !strings.Contains(w.Body.String(), `"plan_moved":true`) {
		t.Fatalf("first association: %d %s", w.Code, w.Body.String())
	}
	got, err := planOf(s, replacement)
	if err != nil || got.ID != old.ID || got.Revision != old.Revision+1 {
		t.Fatalf("immediate plan: %+v %v", got, err)
	}
	planGET := httptest.NewRecorder()
	h.Mux().ServeHTTP(planGET, httptest.NewRequest(http.MethodGet, chatapi.Prefix+"/threads/tether:"+replacement+"/plan", nil))
	if planGET.Code != 200 || !strings.Contains(planGET.Body.String(), old.ID) {
		t.Fatalf("GET plan before any proxy call: %d %s", planGET.Code, planGET.Body.String())
	}
	if w := post(body); w.Code != 200 || !strings.Contains(w.Body.String(), `"replayed":true`) {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	if calls != 2 {
		t.Fatalf("verification was skipped on replay: %d", calls)
	}
	if w := post(strings.Replace(body, "continuation:", "wrong:", 1)); w.Code != 400 {
		t.Fatalf("wrong key: %d", w.Code)
	}
}

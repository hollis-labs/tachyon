package chatbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestOutboxPersistsAndReplaysWithoutDuplicateSend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	db, err := localstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"s1","mode":"turn"}`))
	})
	handler := &Outbox{Store: db, Identity: func() (string, error) { return "principal", nil }, Next: next}
	send := func(text string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", ChatPrefix+"/agents/pm/send", strings.NewReader(`{"text":"`+text+`","idempotency_key":"message-key"}`))
		req.SetPathValue("launch", "pm")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	if r := send("hello"); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = localstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler.Store = db
	if r := send("hello"); r.Code != 200 || !strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatal(r.Body.String())
	}
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
	if r := send("different"); r.Code != 409 {
		t.Fatal("key reused for other text")
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", ChatPrefix+"/outbox?launch_id=pm", nil))
	if !strings.Contains(res.Body.String(), `"status":"sent"`) || !strings.Contains(res.Body.String(), `"body":"hello"`) {
		t.Fatal(res.Body.String())
	}
	handler.Identity = func() (string, error) { return "other-principal", nil }
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", ChatPrefix+"/outbox?launch_id=pm", nil))
	if strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatal("cross-principal history disclosed")
	}
}

func TestOutboxRetainsUpstreamScopeDenial(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := &Outbox{Store: db, Identity: func() (string, error) { return "principal", nil }, Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"required scope: operate"}}`))
	})}
	r := httptest.NewRequest("POST", ChatPrefix+"/agents/demo/send", strings.NewReader(`{"text":"hello","idempotency_key":"key"}`))
	r.SetPathValue("launch", "demo")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("scope failure concealed")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", ChatPrefix+"/outbox?launch_id=demo", nil))
	if !strings.Contains(w.Body.String(), `"error":"required scope: operate"`) || !strings.Contains(w.Body.String(), `"status":"failed"`) {
		t.Fatal("saved history lost the scope failure", w.Body.String())
	}
}

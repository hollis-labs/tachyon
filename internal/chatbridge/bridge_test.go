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

func TestTurnsKeepsBrowserSessionsSeparateAndRestrictsRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("browser authorization leaked")
		}
		if r.URL.Path == "/chat" {
			http.SetCookie(w, &http.Cookie{Name: "tangent_participant", Value: "new-session", HttpOnly: true})
			return
		}
		c, e := r.Cookie("tangent_participant")
		if e != nil || c.Value != "browser-one" {
			t.Errorf("wrong participant: %v %v", c, e)
		}
		if len(r.Cookies()) != 1 {
			t.Error("unrelated cookies forwarded")
		}
		if r.URL.Path != "/api/turns" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"pending":[],"history":[]}`))
	}))
	defer upstream.Close()
	h := NewTurns(upstream.URL)
	req := httptest.NewRequest("POST", Prefix+"/session", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	cookies := res.Result().Cookies()
	if res.Code != 200 || len(cookies) != 1 || cookies[0].Name != CookieName || cookies[0].Path != Prefix || !cookies[0].HttpOnly {
		t.Fatalf("bad session response: %d %v", res.Code, cookies)
	}
	req = httptest.NewRequest("GET", Prefix+"/inbox", nil)
	req.Header.Set("Authorization", "private")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "browser-one"})
	req.AddCookie(&http.Cookie{Name: "unrelated", Value: "private"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code)
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", Prefix+"/inbox", nil))
	if res.Code != 401 {
		t.Fatal("missing browser session was admitted")
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("POST", Prefix+"/items/one/dismiss", nil))
	if res.Code != 404 {
		t.Fatal("unreviewed route was admitted")
	}
}

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

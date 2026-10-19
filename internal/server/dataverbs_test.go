package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/dataverbs"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestLocalDataHostRoutesVerifyEveryWrite(t *testing.T) {
	checks := 0
	elevated := false
	authFailure := false
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		if r.URL.Path != "/api/session" || r.Method != "GET" {
			t.Error("unexpected assurance request")
		}
		cookie, err := r.Cookie("tangent_participant")
		if err != nil || cookie.Value != "session-test" || r.Header.Get("Authorization") != "" {
			t.Error("authority forwarding changed")
		}
		if authFailure {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"code":"unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"elevated": elevated, "assurance": "operator-token"})
	}))
	defer auth.Close()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL})
	s.objects = objects.New(db)
	handler := s.Routes()
	invoke := func(method, verb, input, cookie, origin string) *httptest.ResponseRecorder {
		t.Helper()
		path := dataverbs.Prefix + "/providers/tachyon/verbs/" + verb
		body := input
		if method == "GET" {
			path += "?input=" + url.QueryEscape(input)
			body = ""
		}
		req := httptest.NewRequest(method, "https://tachyon.test"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Actor", "forged")
		req.Header.Set("Authorization", "Bearer must-not-be-trusted")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	id := uuid.NewString()
	create := `{"id":"` + id + `","kind":"idea","definition":{"name":"Owned","status":"draft"}}`
	for _, test := range []struct {
		cookie, origin string
		status         int
	}{{"", "https://tachyon.test", 401}, {"session-test", "https://evil.test", 403}, {"session-test", "https://tachyon.test", 403}} {
		w := invoke("POST", "objects.create", create, test.cookie, test.origin)
		if w.Code != test.status {
			t.Fatalf("denied write: %d %s", w.Code, w.Body.String())
		}
	}
	rows, err := s.objects.List(context.Background(), objects.Filter{})
	if err != nil || len(rows) != 0 {
		t.Fatal("denied write persisted", err)
	}
	elevated = true
	w := invoke("POST", "objects.create", create, "session-test", "https://tachyon.test")
	if w.Code != 200 {
		t.Fatal("authorized write failed without plugin admission", w.Code, w.Body.String())
	}
	at := checks
	w = invoke("GET", "objects.get", `{"id":"`+id+`"}`, "", "")
	if w.Code != 200 || checks != at || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("read access/cache changed", w.Code)
	}
	history, err := s.objects.History(context.Background(), id, 0, 10)
	if err != nil || history[0].Actor != "operator:browser" {
		t.Fatal("forged audit actor", err)
	}
	// An earlier successful write must not cache elevation for the next one.
	elevated = false
	w = invoke("POST", "objects.patch", `{"id":"`+id+`","expected_revision":1,"definition":{"name":"Denied edit"}}`, "session-test", "https://tachyon.test")
	if w.Code != 403 || checks != at+1 {
		t.Fatal("cached assurance permitted write", w.Code)
	}
	authFailure = true
	w = invoke("POST", "objects.delete", `{"id":"`+id+`","expected_revision":1}`, "session-test", "https://tachyon.test")
	if w.Code != 503 {
		t.Fatal("unavailable assurance failed open", w.Code)
	}
	record, err := s.objects.Get(context.Background(), id)
	if err != nil || record.Name != "Owned" || record.Deleted() || record.Revision != 1 {
		t.Fatal("refused mutation reached store", err)
	}
}

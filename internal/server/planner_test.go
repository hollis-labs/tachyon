package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

func TestOwnedPlannerRoutesEnforceOperatorAndBypassPlugin(t *testing.T) {
	elevated := false
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	s.store = db
	s.objects = objects.New(db)
	handler := s.Routes()
	id := uuid.NewString()
	body := `{"id":"` + id + `","fields":{"title":"Authorized"}}`
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://tachyon.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "operator-session"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", planner.Prefix+"/ideas", body, "https://tachyon.test"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := s.objects.Get(context.Background(), id); err != objects.ErrNotFound {
		t.Fatal("unauthorized record created", err)
	}
	elevated = true
	if w := call("POST", planner.Prefix+"/ideas", body, "https://elsewhere.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("POST", planner.Prefix+"/ideas", body, "https://tachyon.test"); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, prefix := range []string{planner.Prefix, planner.LegacyPrefix} {
		if w := call("GET", prefix+"/ideas/"+id, "", ""); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	history, err := s.objects.History(context.Background(), id, 0, 10)
	if err != nil || history[0].Actor != "operator:browser" {
		t.Fatal(history, err)
	}
}

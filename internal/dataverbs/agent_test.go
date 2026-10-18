package dataverbs

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestAgentBoundaryAndKindRestrictions(t *testing.T) {
	local, s, db := setup(t)
	authStore := &agentaccess.Store{DB: db}
	planner := createRecord(t, local, objects.InboxItemKind, `{"name":"Private planning","status":"new"}`)
	other := createRecord(t, local, objects.ProjectKind, `{"name":"Other kind"}`)
	token, secret, err := authStore.Issue(ctx, "", "Planner", agentaccess.PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHTTP(s, authStore)
	call := func(method, path, input, credential string) *httptest.ResponseRecorder {
		t.Helper()
		if input != "" {
			path += "?input=" + url.QueryEscape(input)
		}
		req := httptest.NewRequest(method, AgentPrefix+path, nil)
		if credential != "" {
			req.Header.Add("Authorization", "Bearer "+credential)
		}
		req.Header.Set("Cookie", "tachyon_approvals=forged")
		req.Header.Set("X-Actor", "operator:browser")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable response")
		}
		return w
	}
	if w := call("GET", "/providers", "", ""); w.Code != 401 {
		t.Fatal("anonymous discovery", w.Code)
	}
	if w := call("GET", "/providers", "", secret); w.Code != 200 || strings.Contains(w.Body.String(), "objects.create") || strings.Contains(w.Body.String(), "collections.members") {
		t.Fatal("discovery", w.Code, w.Body.String())
	}
	for _, tt := range []struct {
		verb, input string
		status      int
	}{
		{"planner.list", `{}`, 200},
		{"planner.list", `{"kind":"agent"}`, 403},
		{"objects.list", `{}`, 403},
		{"objects.list", `{"kind":"inbox_item"}`, 200},
		{"objects.get", `{"id":"` + planner.ID + `"}`, 200},
		{"objects.history", `{"id":"` + planner.ID + `"}`, 200},
		{"objects.get", `{"id":"` + other.ID + `"}`, 403},
		{"objects.history", `{"id":"` + other.ID + `"}`, 403},
		{"objects.lookup_source", `{"provider":"tether","instance":"x","collection":"projects","kind":"project","external_id":"x"}`, 403},
		{"planner.list", `{"kind":"plan","kind":"agent"}`, 400},
		{"planner.list", `{"Kind":"agent"}`, 403},
		{"objects.links", `{"id":"` + planner.ID + `"}`, 404},
		{"objects.create", `{}`, 404},
	} {
		w := call("GET", "/providers/tachyon/verbs/"+tt.verb, tt.input, secret)
		if w.Code != tt.status {
			t.Fatalf("%s %s: %d %s", tt.verb, tt.input, w.Code, w.Body.String())
		}
	}
	if w := call("POST", "/providers/tachyon/verbs/planner.list", "", secret); w.Code != 405 {
		t.Fatal("read POST accepted")
	}
	_, narrow, err := authStore.Issue(ctx, "", "Narrow", agentaccess.Grants{Verbs: []string{"planner.list"}, Kinds: []string{"inbox_item"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/providers/tachyon/verbs/planner.list", `{}`, narrow); w.Code != 403 {
		t.Fatal("unbounded query accepted")
	}
	if w := call("GET", "/providers/tachyon/verbs/planner.list", `{"kind":"inbox_item"}`, narrow); w.Code != 200 {
		t.Fatal("bounded query refused")
	}
	if err := authStore.Revoke(ctx, token.ID, false); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/providers/tachyon/verbs/planner.list", `{}`, secret); w.Code != 401 {
		t.Fatal("revocation cached", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, AgentPrefix+"/providers", nil)
	req.Header.Add("Authorization", "Bearer "+narrow)
	req.Header.Add("Authorization", "Bearer "+narrow)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("duplicate credentials accepted")
	}
}

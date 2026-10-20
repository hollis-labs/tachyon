package dataverbs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestAgentPortfolioWritesEnforceGrantsAndRevisionHistory(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	writer, secret, err := access.Issue(ctx, "", "Portfolio Architect", agentaccess.PortfolioWrite(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := access.Issue(ctx, "", "Reader", agentaccess.PortfolioRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	narrowGrants := agentaccess.PortfolioWrite()
	narrowGrants.Kinds = []string{"idea"}
	_, narrow, err := access.Issue(ctx, "", "Ideas only", narrowGrants, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHTTP(s, access)
	call := func(method, verb, input, credential string, status int) map[string]any {
		t.Helper()
		path := AgentPrefix + "/providers/tachyon/verbs/" + verb
		var body io.Reader
		if method == "GET" {
			path += "?input=" + url.QueryEscape(input)
		} else {
			body = strings.NewReader(input)
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("X-Actor", "operator:browser")
		req.Header.Set("Cookie", "tachyon_approvals=forged")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s: got %d want %d: %s", verb, w.Code, status, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ids := map[string]string{}
	for _, database := range []string{"priorities", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"} {
		id := uuid.NewString()
		ids[database] = id
		input := fmt.Sprintf(`{"db":%q,"id":%q,"fields":{"title":"Agent authored"}}`, database, id)
		call("POST", "planner.portfolio.create", input, reader, 404)
		created := call("POST", "planner.portfolio.create", input, secret, 200)["data"].(map[string]any)
		if created["author"] != "service:"+writer.IdentityID {
			t.Fatal("forged actor", created)
		}
		// Retained UUID retry creates no duplicate and preserves the original revision.
		call("POST", "planner.portfolio.create", input, secret, 200)
	}
	id := ids["ideas"]
	patch := fmt.Sprintf(`{"db":"ideas","id":%q,"expected_revision":1,"patch":{"title":"Edited"}}`, id)
	call("POST", "planner.portfolio.patch", patch, secret, 200)
	call("POST", "planner.portfolio.patch", patch, secret, 409)
	call("POST", "planner.portfolio.patch", fmt.Sprintf(`{"db":"ideas","id":%q,"expected_revision":2,"patch":{"author":"forged"}}`, id), secret, 400)
	call("POST", "planner.portfolio.action", fmt.Sprintf(`{"db":"ideas","id":%q,"expected_revision":2,"action":"comments","arguments":{"text":"A note","author":"forged"}}`, id), secret, 200)
	got := call("GET", "planner.portfolio.get", fmt.Sprintf(`{"db":"ideas","id":%q}`, id), reader, 200)["data"].(map[string]any)
	comment := got["comments"].([]any)[0].(map[string]any)
	if comment["author"] != "service:"+writer.IdentityID {
		t.Fatal("spoofed comment", comment)
	}
	call("POST", "planner.portfolio.action", fmt.Sprintf(`{"db":"ideas","id":%q,"expected_revision":3,"action":"link-add","arguments":{"link":{"kind":"url","ref":"https://example.test"}}}`, id), secret, 200)
	relation := fmt.Sprintf(`{"from":{"db":"ideas","id":%q},"to":{"db":"risks","id":%q},"expected_revision":4,"add":true}`, id, ids["risks"])
	call("POST", "planner.portfolio.relate", relation, narrow, 403)
	call("POST", "planner.portfolio.relate", relation, secret, 200)
	call("POST", "planner.portfolio.reorder", fmt.Sprintf(`{"db":"ideas","items":[{"id":%q,"expected_revision":5}]}`, id), narrow, 200)
	call("POST", "planner.portfolio.action", fmt.Sprintf(`{"db":"ideas","id":%q,"expected_revision":6,"action":"delete"}`, id), secret, 403)
	call("POST", "objects.delete", fmt.Sprintf(`{"id":%q,"expected_revision":6}`, id), secret, 404)
	call("POST", "objects.patch", fmt.Sprintf(`{"id":%q,"expected_revision":6,"metadata":{}}`, id), secret, 404)
	call("GET", "planner.portfolio.list", `{"db":"risks"}`, narrow, 403)
	call("GET", "planner.portfolio.resolve", fmt.Sprintf(`{"id":%q}`, ids["risks"]), narrow, 404)
	call("GET", "planner.portfolio.resolve", fmt.Sprintf(`{"id":%q}`, id), narrow, 200)
	call("GET", "planner.portfolio.schema", `{"db":"ideas"}`, narrow, 200)
	call("POST", "planner.portfolio.create", fmt.Sprintf(`{"db":"risks","id":%q,"fields":{"title":"Forbidden"}}`, uuid.NewString()), narrow, 403)
	// A granted database name cannot be used to mutate an object of another kind.
	call("POST", "planner.portfolio.patch", fmt.Sprintf(`{"db":"ideas","id":%q,"expected_revision":1,"patch":{"title":"Forbidden"}}`, ids["risks"]), narrow, 404)
	call("GET", "planner.portfolio.patch", patch, secret, 405)
	call("POST", "planner.portfolio.get", fmt.Sprintf(`{"db":"ideas","id":%q}`, id), secret, 405)
	history := call("GET", "objects.history", fmt.Sprintf(`{"id":%q}`, id), reader, 200)["data"].(map[string]any)
	for _, value := range history["items"].([]any) {
		revision := value.(map[string]any)
		if revision["actor"] != "service:"+writer.IdentityID && revision["actor"] != "service:"+narrowIdentity(t, access, narrow) {
			t.Fatal("history not attributed to authenticated principal", revision)
		}
	}
	if err := access.Revoke(ctx, writer.ID, false); err != nil {
		t.Fatal(err)
	}
	call("POST", "planner.portfolio.patch", patch, secret, 401)
}

func narrowIdentity(t *testing.T, access *agentaccess.Store, secret string) string {
	t.Helper()
	principal, err := access.Verify(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	return principal.ID
}

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

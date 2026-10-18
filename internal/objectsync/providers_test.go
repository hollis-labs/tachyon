package objectsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

var ctx = context.Background()

func testStore(t *testing.T) (*localstore.Store, *Coordinator) {
	t.Helper()
	s, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, New(s)
}
func mustRun(t *testing.T, c *Coordinator, s Source) State {
	t.Helper()
	state, err := c.Run(ctx, s, "")
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func get(t *testing.T, c *Coordinator, s Source, id string) objects.Record {
	t.Helper()
	r, err := c.objects.LookupSource(ctx, objects.SourceKey{SourceScope: s.Scope(), ExternalID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func project(name, status string) map[string]any {
	return map[string]any{"urn": "msg://agent/agent-mux/prj_example", "kind": "project", "display_name": name, "status": status, "description": "upstream description", "props": map[string]string{"language": "go"}, "guidelines": "use local libs", "entry_points": []string{"README.md"}, "external_ids": []map[string]string{{"substrate": "cerberus", "external_id": "project-a"}}, "kind_meta": map[string]string{"private": "do-not-import"}}
}

func TestTetherImportOverlaysDeletionRestorationAndFailure(t *testing.T) {
	_, c := testStore(t)
	payload := any(map[string]any{"projects": []any{project("Control plane", "active")}})
	status := 200
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/registry/projects" || r.URL.Query().Get("status") != "*" || r.URL.Query().Get("include") != "external_ids" {
			t.Error("wrong inventory request")
		}
		if r.Header.Get("Tether-Protocol") != "1" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing provider authentication")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()
	s, err := NewTetherProjects(server.URL, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	state := mustRun(t, c, s)
	if !state.Authoritative || state.Items != 1 || state.Pages != 1 || state.LastSuccessAt == "" {
		t.Fatalf("state: %+v", state)
	}
	first := get(t, c, s, "msg://agent/agent-mux/prj_example")
	if strings.Contains(string(first.Definition), "do-not-import") || !strings.Contains(string(first.Definition), "cerberus") {
		t.Fatal("wrong projection")
	}
	edited, err := c.objects.Patch(ctx, first.ID, first.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"My project"}`), Metadata: json.RawMessage(`{"favorite":true}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, c, s)
	same := get(t, c, s, "msg://agent/agent-mux/prj_example")
	if same.ID != first.ID || same.Revision != edited.Revision {
		t.Fatal("identical poll created a new record or revision")
	}
	payload = map[string]any{"projects": []any{project("Renamed upstream", "deprecated")}}
	mustRun(t, c, s)
	deleted := get(t, c, s, "msg://agent/agent-mux/prj_example")
	if !deleted.Deleted() || deleted.Name != "My project" || !strings.Contains(string(deleted.Metadata), "favorite") {
		t.Fatal("deprecation lost local ownership")
	}
	payload = map[string]any{"projects": []any{project("Renamed upstream", "active")}}
	good := mustRun(t, c, s)
	restored := get(t, c, s, "msg://agent/agent-mux/prj_example")
	if restored.Deleted() || restored.ID != first.ID {
		t.Fatal("restoration lost identity")
	}
	for _, code := range []int{401, 403, 429, 500, 302} {
		status = code
		payload = map[string]string{"error": "test-token must never be persisted"}
		state, err = c.Run(ctx, s, "")
		if err == nil || state.Phase != "failed" || state.LastSuccessAt != good.LastSuccessAt {
			t.Fatalf("failed scan state: %+v %v", state, err)
		}
		if strings.Contains(state.Problem, "test-token") || strings.Contains(err.Error(), "test-token") {
			t.Fatal("persisted remote error body")
		}
		if get(t, c, s, "msg://agent/agent-mux/prj_example").Revision != restored.Revision {
			t.Fatal("failed inventory changed records")
		}
	}
	status = 200
	for _, bad := range []any{map[string]any{}, map[string]any{"projects": nil}, map[string]any{"projects": []any{}, "next_cursor": "more"}, map[string]any{"projects": []any{project("x", "active"), project("x", "active")}}} {
		payload = bad
		if _, err := c.Run(ctx, s, ""); err == nil {
			t.Fatal("accepted incomplete inventory")
		}
		if get(t, c, s, "msg://agent/agent-mux/prj_example").Revision != restored.Revision {
			t.Fatal("invalid inventory changed records")
		}
	}
	payload = map[string]any{"projects": []any{}}
	mustRun(t, c, s)
	if !get(t, c, s, "msg://agent/agent-mux/prj_example").Deleted() {
		t.Fatal("complete inventory did not tombstone absent project")
	}
}

func repo(id int, name string) map[string]any {
	return map[string]any{"id": id, "name": name, "owner": map[string]any{"login": "hollis-labs", "id": 209122427}, "html_url": "https://github.com/hollis-labs/" + name, "description": "source", "visibility": "private", "default_branch": "main", "archived": false, "topics": []string{"go"}}
}

func TestGitHubPaginationAtomicFailureRenameAndVisibility(t *testing.T) {
	_, c := testStore(t)
	name := "tachyon"
	fail := false
	missing := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Error("wrong GitHub request")
		}
		if missing {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		page := r.URL.Query().Get("page")
		if page == "1" {
			q := r.URL.Query()
			q.Set("page", "2")
			w.Header().Set("Link", "<"+server.URL+"/organizations/209122427/repos?"+q.Encode()+">; rel=\"next\"")
			json.NewEncoder(w).Encode([]any{repo(1, name)})
		} else if page == "2" {
			if fail {
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":"secret"}`)
				return
			}
			json.NewEncoder(w).Encode([]any{repo(2, "libs")})
		} else {
			t.Error("unexpected page")
		}
	}))
	defer server.Close()
	s, err := NewGitHubRepositories(server.URL, "Hollis-Labs", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	good := mustRun(t, c, s)
	first := get(t, c, s, "1")
	if good.Authoritative || good.Pages != 2 || good.Items != 2 || good.Checkpoint != "visible_inventory_complete" {
		t.Fatalf("bad scan state: %+v", good)
	}
	_, err = c.objects.Patch(ctx, first.ID, first.Revision, objects.Patch{Metadata: json.RawMessage(`{"group":"fabric"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	before := get(t, c, s, "1")
	name = "renamed"
	fail = true
	state, err := c.Run(ctx, s, "")
	if err == nil || state.Pages != 1 || state.Checkpoint != "page:1" || state.LastSuccessAt != good.LastSuccessAt {
		t.Fatalf("partial scan: %+v %v", state, err)
	}
	if get(t, c, s, "1").Name != before.Name {
		t.Fatal("applied first page before scan succeeded")
	}
	fail = false
	mustRun(t, c, s)
	after := get(t, c, s, "1")
	if after.Name != "renamed" || after.ID != first.ID || string(after.Metadata) != `{"group":"fabric"}` {
		t.Fatal("rename lost identity or local metadata")
	}
	missing = true
	mustRun(t, c, s)
	if get(t, c, s, "1").Deleted() || get(t, c, s, "2").Deleted() {
		t.Fatal("visibility loss inferred deletion")
	}
}

func TestGitHubRejectsUnsafeContinuationAndMalformedInventories(t *testing.T) {
	for _, scenario := range []string{"foreign-link", "wrong-page", "filter-change", "null", "duplicate", "wrong-owner", "too-many", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				q := r.URL.Query()
				q.Set("page", "2")
				link := server.URL + r.URL.Path + "?" + q.Encode()
				list := []any{repo(1, "one")}
				switch scenario {
				case "foreign-link":
					link = "https://elsewhere.invalid/repos?page=2"
				case "wrong-page":
					q.Set("page", "3")
					link = server.URL + r.URL.Path + "?" + q.Encode()
				case "filter-change":
					q.Set("type", "public")
					link = server.URL + r.URL.Path + "?" + q.Encode()
				case "null":
					fmt.Fprint(w, "null")
					return
				case "duplicate":
					list = append(list, repo(1, "again"))
					link = ""
				case "wrong-owner":
					p := repo(1, "one")
					p["owner"] = map[string]string{"login": "other"}
					list = []any{p}
					link = ""
				case "too-many":
					list = nil
					for i := 0; i < 101; i++ {
						list = append(list, repo(i+1, strconv.Itoa(i)))
					}
					link = ""
				case "redirect":
					w.Header().Set("Location", server.URL+"/redirected")
					w.WriteHeader(302)
					return
				}
				if link != "" {
					w.Header().Set("Link", "<"+link+">; rel=\"next\"")
				}
				json.NewEncoder(w).Encode(list)
			}))
			defer server.Close()
			s, err := NewGitHubRepositories(server.URL, "hollis-labs", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Scan(ctx, time.Now(), func(Progress) error { return nil }); err == nil {
				t.Fatal("accepted malformed scan")
			}
			if requests != 1 {
				t.Fatal("followed unsafe request")
			}
		})
	}
}

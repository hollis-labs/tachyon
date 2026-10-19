package planner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestOwnedChangeStreamObservesServiceWrites(t *testing.T) {
	s, store := ownedFixture(t)
	up := httptest.NewServer(NewHTTP(store, nil))
	defer up.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(up.URL + Prefix + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	readFrame := func() string {
		t.Helper()
		frame := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			frame += line
			if line == "\n" {
				return frame
			}
		}
	}
	initial := readFrame()
	if _, err := s.Create(context.Background(), "inbox", uuid.NewString(), Item{"title": "Written outside HTTP"}, "local-agent"); err != nil {
		t.Fatal(err)
	}
	changed := readFrame()
	if initial == changed || !strings.Contains(changed, "event: planner.changed") {
		t.Fatal("service write did not invalidate stream", changed)
	}
}

func ownedFixture(t *testing.T) (*Owned, *localstore.Store) {
	t.Helper()
	store, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &Owned{Objects: objects.New(store)}, store
}
func revision(row Item) int64 { return int64(number(row["rev"])) }

func TestOwnedPortfolioAuthorshipCommentsAndConflict(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"id":"DEC-legacy","title":"Original","status":"decided","rev":4,"created":"2026-01-01","updated":"2026-01-02","notes":"remove me","comments":[{"id":"c-1","author":"original","text":"history"}],"options":[{"id":"a","label":"First"}],"future":9007199254740993}`)
	obs, err := PortfolioObservation("https://torque.test", "decisions", raw, 2, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Objects.RefreshImported(ctx, obs, "import")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(ctx, "decisions", "DEC-legacy")
	if err != nil || row["id"] != original.ID || row["updated"] != "2026-01-02" {
		t.Fatal(row, err)
	}
	row, err = s.Patch(ctx, "decisions", original.ID, revision(row), Item{"title": "Authored", "notes": nil}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := row["notes"]; ok {
		t.Fatal("deleted field resurfaced")
	}
	row, err = s.Action(ctx, "decisions", original.ID, revision(row), "comments", Item{"text": "New evidence", "author": "forged"}, "verified-operator")
	if err != nil {
		t.Fatal(err)
	}
	comments := array(row["comments"])
	if len(comments) != 2 || textField(object(comments[1]), "author") != "verified-operator" {
		t.Fatal(comments)
	}
	before := revision(row)
	row, err = s.Action(ctx, "decisions", original.ID, before, "decide", Item{"option": "a", "comment": "Chosen"}, "operator")
	if err != nil || row["status"] != "accepted" {
		t.Fatal(row, err)
	}
	if _, err = s.Patch(ctx, "decisions", original.ID, before, Item{"title": "Stale"}, "operator"); !errors.Is(err, objects.ErrConflict) {
		t.Fatal(err)
	}
	got, err := s.Objects.Get(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Source.Definition) != string(original.Source.Definition) || !strings.Contains(string(got.Definition), "9007199254740993") {
		t.Fatal("provenance or number lost")
	}
	if _, err = s.Objects.Patch(ctx, got.ID, got.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"CLI edit"}`)}, "local-agent"); err != nil {
		t.Fatal(err)
	}
	row, err = s.Get(ctx, "decisions", original.ID)
	if err != nil || row["title"] != "CLI edit" {
		t.Fatal("native service edits hidden", row, err)
	}
}

func TestPortfolioReorderRollsBackAndPromotionReplays(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	a, err := s.Create(ctx, "ideas", uuid.NewString(), Item{"title": "A"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "ideas", uuid.NewString(), Item{"title": "B"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Reorder(ctx, "ideas", []OrderChange{{textField(a, "id"), revision(a)}, {textField(b, "id"), revision(b) + 1}}, "operator")
	if !errors.Is(err, objects.ErrConflict) {
		t.Fatal(err)
	}
	check, err := s.Get(ctx, "ideas", textField(a, "id"))
	if err != nil || revision(check) != revision(a) {
		t.Fatal("partial reorder committed", err)
	}
	for _, db := range portfolioDatabases {
		if db == "inbox" {
			continue
		}
		t.Run(db, func(t *testing.T) {
			in, err := s.Create(ctx, "inbox", uuid.NewString(), Item{"title": "Capture"}, "operator")
			if err != nil {
				t.Fatal(err)
			}
			target := uuid.NewString()
			out, err := s.Promote(ctx, textField(in, "id"), revision(in), db, target, Item{"title": "Promoted"}, "operator")
			if err != nil {
				t.Fatal(err)
			}
			if object(out["inbox"])["status"] != "triaged" || object(out["item"])["id"] != target {
				t.Fatal(out)
			}
			if _, err = s.Promote(ctx, textField(in, "id"), revision(in), db, target, Item{"title": "Promoted"}, "operator"); err != nil {
				t.Fatal("retry failed", err)
			}
			if _, err = s.Promote(ctx, textField(in, "id"), revision(in), db, target, Item{"title": "Different request"}, "operator"); !errors.Is(err, objects.ErrConflict) {
				t.Fatal("changed retry accepted", err)
			}
		})
	}
}

func TestTaskReadsUsePrimaryAPIsAndPagination(t *testing.T) {
	requests := []string{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if strings.Contains(r.URL.Path, "planner") {
			t.Error("extra Planner hop")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/api/v1/tasks/CW-1" {
			_, _ = w.Write([]byte(`{"id":"CW-1","title":"Task","status":"doing"}`))
			return
		}
		if r.URL.Path != "/api/v1/tasks" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("project_id") != "PRJ-1" || r.URL.Query().Get("tags") != "scope" {
			t.Error("scope lost")
		}
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"CW-1","title":"Task","status":"doing"}],"meta":{"has_more":true,"next_cursor":"next"}}`))
		} else {
			_, _ = w.Write([]byte(`{"items":[{"id":"CW-2","title":"Done","status":"done"}],"meta":{"has_more":false,"next_cursor":null}}`))
		}
	}))
	defer up.Close()
	tasks := NewTasks(up.URL, "")
	ctx := context.Background()
	titles, err := tasks.Titles(ctx, []string{"CW-1"})
	if err != nil || titles["CW-1"].Title != "Task" {
		t.Fatal(titles, err)
	}
	out, err := tasks.ForWorkstream(ctx, Item{"id": "ws", "torque_project_ids": []any{"PRJ-1", "PRJ-1"}, "torque_tag": "scope"})
	if err != nil || out.Total != 2 || out.OpenTotal != 1 || out.Counts["done"] != 1 {
		t.Fatal(out, err)
	}
	if len(requests) < 3 {
		t.Fatal("pagination was not followed")
	}
}

func TestPlannerHTTPReadsWithTorqueUnavailableAndRevisionRequired(t *testing.T) {
	s, store := ownedFixture(t)
	ctx := context.Background()
	row, err := s.Create(ctx, "ideas", uuid.NewString(), Item{"title": "Owned"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(store, NewTasks("http://127.0.0.1:1", ""))
	ws, err := s.Create(ctx, "workstreams", uuid.NewString(), Item{"title": "Local workstream", "status": "active", "tags": []any{"board"}, "torque_project_ids": []any{"PRJ-1"}}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/databases", "/ideas", "/ideas/" + textField(row, "id"), "/schema/ideas", "/search?q=Owned", "/board"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", Prefix+path, nil))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if path == "/board" {
			var b struct {
				Sections map[string][]Item `json:"sections"`
				Notices  []string          `json:"notices"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || len(b.Notices) == 0 || len(b.Sections["in_flight"]) == 0 || b.Sections["in_flight"][0]["id"] != ws["id"] {
				t.Fatal("upstream failure hid owned board", w.Body.String(), err)
			}
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PATCH", Prefix+"/ideas/"+textField(row, "id"), strings.NewReader(`{"patch":{"title":"No revision"}}`)))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, err := s.Get(ctx, "ideas", textField(row, "id"))
	if err != nil || got["title"] != "Owned" {
		t.Fatal(got, err)
	}
}

func TestPortfolioRelationUsesLocalIdentityAndRevision(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	from, err := s.Create(ctx, "ideas", uuid.NewString(), Item{"title": "Related idea"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	to, err := s.Create(ctx, "workstreams", uuid.NewString(), Item{"title": "Workstream"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	q := RelationRequest{From: PortfolioRef{"ideas", textField(from, "id")}, To: PortfolioRef{"workstreams", textField(to, "id")}, ExpectedRevision: revision(from), Field: "workstream_ids", Add: true}
	row, err := s.Relate(ctx, q, "operator")
	if err != nil || !contains(row["workstream_ids"], to["id"].(string)) {
		t.Fatal(row, err)
	}
	if _, err := s.Relate(ctx, q, "operator"); !errors.Is(err, objects.ErrConflict) {
		t.Fatal("stale relation accepted", err)
	}
	resolved, err := s.ResolveReference(ctx, textField(to, "id"))
	if err != nil || resolved["db"] != "workstreams" {
		t.Fatal(resolved, err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// This server deliberately rejects unknown parameters, as the legacy provider
// does: permissive fixtures would conceal a broken deployment handshake.
func listFixture(t *testing.T, paged bool, total int, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		allowed := map[string]bool{"limit": true, "offset": true, "status": true, "project_id": true, "tags": true, "q": true}
		if paged {
			allowed["include_total"] = true
		}
		for key := range q {
			if !allowed[key] {
				http.Error(w, "unknown parameter "+key, 400)
				return
			}
		}
		if !q.Has("offset") {
			t.Error("missing explicit offset")
			http.Error(w, "offset required", 400)
			return
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 200 {
			t.Errorf("invalid requested limit %d", limit)
			http.Error(w, "limit", 400)
			return
		}
		end := offset + limit
		if end > total {
			end = total
		}
		items := make([]WorkItem, 0, end-offset)
		for i := offset; i < end; i++ {
			items = append(items, WorkItem{ID: fmt.Sprintf("CW-%d", i)})
		}
		more := end < total
		meta := map[string]any{"returned": len(items), "limit": limit, "offset": offset, "has_more": more, "next_offset": nil}
		if more {
			meta["next_offset"] = end
		}
		var body map[string]any
		if paged {
			if q.Get("include_total") == "true" {
				meta["total"] = total
			}
			body = map[string]any{"items": items, "meta": meta}
		} else {
			meta["tasks"] = items
			meta["total"] = total
			body = meta
		}
		json.NewEncoder(w).Encode(body)
	}))
}

func TestListFullOffsetTraversal(t *testing.T) {
	for _, paged := range []bool{false, true} {
		t.Run(fmt.Sprint(paged), func(t *testing.T) {
			var calls atomic.Int32
			server := listFixture(t, paged, 4300, &calls)
			defer server.Close()
			adapter := NewTorqueAdapter(server.URL)
			offset := 0
			for {
				list, err := adapter.ListWorkItems(context.Background(), WorkFilters{Limit: 200, Offset: offset})
				if err != nil {
					t.Fatal(err)
				}
				if list.Total != 4300 || len(list.Tasks) > 200 {
					t.Fatalf("bad page: %+v", list)
				}
				for i, item := range list.Tasks {
					if item.ID != fmt.Sprintf("CW-%d", offset+i) {
						t.Fatalf("skipped/repeated item %s at %d", item.ID, offset+i)
					}
				}
				offset += len(list.Tasks)
				if !list.HasMore {
					if list.NextOffset != nil {
						t.Fatal("final continuation")
					}
					break
				}
				if list.NextOffset == nil || *list.NextOffset != offset {
					t.Fatal("broken continuation")
				}
			}
			want := int32(22)
			if paged {
				want++
			}
			if offset != 4300 || calls.Load() != want {
				t.Fatalf("walk: %d items / %d requests", offset, calls.Load())
			}
		})
	}
}

func TestBoardTotalsBothShapes(t *testing.T) {
	for _, paged := range []bool{false, true} {
		for _, limit := range []int{1, 50} {
			t.Run(fmt.Sprintf("paged=%v/limit=%d", paged, limit), func(t *testing.T) {
				var calls atomic.Int32
				base := listFixture(t, paged, 73, &calls)
				defer base.Close()
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("status") != "doing" || r.URL.Query().Get("project_id") != "project-A" || r.URL.Query().Get("tags") != "team" || r.URL.Query().Get("offset") != "0" {
						t.Errorf("lost board filters: %v", r.URL.Query())
					}
					resp, err := http.Get(base.URL + r.URL.String())
					if err != nil {
						t.Error(err)
						return
					}
					defer resp.Body.Close()
					var raw json.RawMessage
					json.NewDecoder(resp.Body).Decode(&raw)
					w.WriteHeader(resp.StatusCode)
					w.Write(raw)
				}))
				defer proxy.Close()
				list, err := NewTorqueAdapter(proxy.URL).ListWorkItems(context.Background(), WorkFilters{Limit: limit, Status: "doing", ProjectID: "project-A", Tags: "team"})
				if err != nil {
					t.Fatal(err)
				}
				if list.Total != 73 || len(list.Tasks) != limit || !list.HasMore {
					t.Fatalf("board total: %+v", list)
				}
			})
		}
	}
}

func TestShapeNegotiationAndRollback(t *testing.T) {
	var shape atomic.Bool
	var calls atomic.Int32
	var totals []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		totals = append(totals, q.Has("include_total"))
		if q.Get("offset") != "0" {
			t.Errorf("offset: %v", q)
		}
		if !shape.Load() {
			for k := range q {
				if k != "limit" && k != "offset" {
					http.Error(w, "unknown parameter "+k, 400)
					return
				}
			}
			w.Write([]byte(`{"tasks":[],"total":0,"has_more":false}`))
			return
		}
		meta := map[string]any{"returned": 0, "limit": 50, "offset": 0, "has_more": false, "next_offset": nil}
		if q.Has("include_total") {
			meta["total"] = 0
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []WorkItem{}, "meta": meta})
	}))
	defer server.Close()
	adapter := NewTorqueAdapter(server.URL)
	read := func() {
		t.Helper()
		if _, err := adapter.ListWorkItems(context.Background(), WorkFilters{Limit: 50}); err != nil {
			t.Fatal(err)
		}
	}
	read()
	shape.Store(true)
	read()
	read()
	shape.Store(false)
	read()
	read()
	want := []bool{false, false, true, true, true, false, false}
	if fmt.Sprint(totals) != fmt.Sprint(want) {
		t.Fatalf("negotiation %v want %v", totals, want)
	}
	// A new adapter must not inherit another instance's observed shape.
	if _, err := NewTorqueAdapter(server.URL).ListWorkItems(context.Background(), WorkFilters{Limit: 50}); err != nil {
		t.Fatal(err)
	}
	if totals[len(totals)-1] {
		t.Fatal("instance cache leaked")
	}
}

func TestSearchBothShapes(t *testing.T) {
	for _, paged := range []bool{false, true} {
		t.Run(fmt.Sprint(paged), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if r.URL.Path != "/api/v1/tasks/search" || q.Get("q") != "hello & world" || q.Get("limit") != "200" || q.Get("offset") != "0" {
					t.Errorf("search request: %s", r.URL)
				}
				for k := range q {
					if k != "q" && k != "limit" && k != "offset" && !(paged && k == "include_total") {
						http.Error(w, "unknown parameter "+k, 400)
						return
					}
				}
				n := 250
				if paged {
					n = 200
				}
				items := make([]WorkItem, n)
				if !paged {
					json.NewEncoder(w).Encode(map[string]any{"tasks": items})
					return
				}
				meta := map[string]any{"returned": n, "limit": 200, "offset": 0, "has_more": true, "next_offset": 200}
				if q.Has("include_total") {
					meta["total"] = 250
				}
				json.NewEncoder(w).Encode(map[string]any{"items": items, "meta": meta})
			}))
			defer server.Close()
			list, err := NewTorqueAdapter(server.URL).SearchWorkItems(context.Background(), "hello & world")
			if err != nil {
				t.Fatal(err)
			}
			wantTotal, wantCalls := 250, 1
			if paged {
				wantTotal, wantCalls = 250, 2
			}
			if len(list.Tasks) != 200 || !list.HasMore || list.Total != wantTotal || calls != wantCalls {
				t.Fatalf("search: %+v / calls %d", list, calls)
			}
		})
	}
}

func TestListFailuresDoNotLoop(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		cached     bool
		wantCalls  int
	}{
		{"other400", `invalid limit`, 400, true, 1},
		{"missing-total", `{"items":[],"meta":{"returned":0,"limit":50,"offset":0,"has_more":false}}`, 200, false, 2},
		{"double-flip", `unknown parameter include_total`, 400, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, tc.body, tc.status) }))
			defer server.Close()
			adapter := NewTorqueAdapter(server.URL)
			adapter.pagedLists.Store(tc.cached)
			list, err := adapter.ListWorkItems(context.Background(), WorkFilters{Limit: 50})
			if err == nil || list != nil || calls != tc.wantCalls {
				t.Fatalf("error=%v list=%+v calls=%d", err, list, calls)
			}
		})
	}
}

func TestMalformedListMetadata(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"items":[],"tasks":[]}`, `{"items":[],"meta":null}`, `{"items":null,"meta":{}}`,
		`{"items":[],"meta":{"returned":0,"limit":50,"has_more":false,"next_cursor":null}}`,
		`{"items":[{}],"meta":{"returned":1,"limit":50,"has_more":true,"next_cursor":"abc","total":2}}`,
		`{"items":[],"meta":{"returned":1,"limit":50,"offset":0,"has_more":false,"total":1}}`,
		`{"items":[],"meta":{"returned":0,"limit":50,"offset":0,"has_more":true,"total":1}}`,
		`{"items":[{}],"meta":{"returned":1,"limit":50,"offset":0,"has_more":true,"total":2}}`,
		`{"items":[{}],"meta":{"returned":1,"limit":50,"offset":0,"has_more":true,"next_offset":0,"total":2}}`,
		`{"items":[{}],"meta":{"returned":1,"limit":50,"offset":0,"has_more":true,"next_offset":2,"total":2}}`,
		`{"items":[{}],"meta":{"returned":1,"limit":50,"offset":0,"has_more":true,"next_offset":1.5,"total":2}}`,
		`{"items":[],"meta":{"returned":0,"limit":50,"offset":1,"has_more":false,"total":0}}`,
		`{"items":[],"meta":{"returned":0,"limit":50,"offset":0,"has_more":"false","total":0}}`,
		`{"items":[],"meta":{"returned":0,"limit":50,"offset":0,"has_more":false,"next_offset":1,"total":0}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			list, _, _, err := decodeTorqueList(json.RawMessage(raw), 0, false)
			if err == nil || list != nil {
				t.Fatalf("accepted malformed response: %+v", list)
			}
		})
	}
}

func TestConcurrentListShapeCache(t *testing.T) {
	var calls atomic.Int32
	server := listFixture(t, true, 0, &calls)
	defer server.Close()
	adapter := NewTorqueAdapter(server.URL)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := adapter.ListWorkItems(context.Background(), WorkFilters{Limit: 50}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestLegacyFixtureRejectsUnknownParameters(t *testing.T) {
	var calls atomic.Int32
	server := listFixture(t, false, 0, &calls)
	defer server.Close()
	for _, key := range []string{"include_total", "surprise"} {
		q := url.Values{"limit": {"50"}, "offset": {"0"}, key: {"true"}}
		response, err := http.Get(server.URL + "/api/v1/tasks?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("legacy accepted %s", key)
		}
	}
}

func TestEmptyListBothShapes(t *testing.T) {
	for _, paged := range []bool{false, true} {
		t.Run(fmt.Sprint(paged), func(t *testing.T) {
			var calls atomic.Int32
			server := listFixture(t, paged, 0, &calls)
			defer server.Close()
			list, err := NewTorqueAdapter(server.URL).ListWorkItems(context.Background(), WorkFilters{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if list.Tasks == nil || len(list.Tasks) != 0 || list.Total != 0 || list.HasMore || list.NextOffset != nil {
				t.Fatalf("empty: %+v", list)
			}
		})
	}
}

func TestSearchEmptyAndFinalBothShapes(t *testing.T) {
	for _, paged := range []bool{false, true} {
		for _, total := range []int{0, 1} {
			t.Run(fmt.Sprintf("paged=%v/total=%d", paged, total), func(t *testing.T) {
				var calls atomic.Int32
				server := listFixture(t, paged, total, &calls)
				defer server.Close()
				list, err := NewTorqueAdapter(server.URL).SearchWorkItems(context.Background(), "specific match")
				if err != nil {
					t.Fatal(err)
				}
				if len(list.Tasks) != total || list.Total != total || list.HasMore || list.NextOffset != nil {
					t.Fatalf("final search: %+v", list)
				}
			})
		}
	}
}

func readTorqueGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".golden")
	if err != nil {
		t.Fatal(err)
	}
	header, body, ok := strings.Cut(string(raw), "\n")
	if !ok || !strings.HasPrefix(header, "// Torque ") {
		t.Fatal("missing capture provenance")
	}
	return []byte(body)
}

// These are real HTTP handler captures from two disposable Torque builds, not
// synthesized wire records. Assert all WorkItem fields survive both envelopes.
func TestRealTorqueGoldenRecords(t *testing.T) {
	legacy, _, _, err := decodeTorqueList(readTorqueGolden(t, "legacy-list"), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Tasks) != 4 {
		t.Fatal("fixture cohort")
	}
	for i, item := range legacy.Tasks {
		var project *string
		if i < 3 {
			id := "PRJ-20261001-0001"
			project = &id
		}
		want := WorkItem{ID: fmt.Sprintf("CW-20261001-%04d", i+1), Title: fmt.Sprintf("golden-match-%d", i), Description: fmt.Sprintf("fixture description %d", i), Status: "todo", Priority: i + 1, ProjectID: project, Manual: true, Metadata: map[string]any{"owner": "fixture", "nested": map[string]any{"flag": true}, "count": float64(i)}}
		if !reflect.DeepEqual(item, want) {
			t.Fatalf("fixture field mapping: got %+v want %+v", item, want)
		}
	}
	for _, name := range []string{"legacy-list", "legacy-search", "current-list", "current-search", "current-list-total", "current-search-total"} {
		t.Run(name, func(t *testing.T) {
			raw := readTorqueGolden(t, name)
			list, _, _, err := decodeTorqueList(raw, 0, strings.Contains(name, "search"))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(list.Tasks, legacy.Tasks) {
				t.Fatalf("task fields changed across real handler envelopes: %+v", list.Tasks)
			}
			// Independently compare the eight consumed JSON fields before decoding,
			// ensuring an unexpected type/key cannot silently become a Go zero value.
			var body map[string]json.RawMessage
			json.Unmarshal(raw, &body)
			records := body["tasks"]
			if records == nil {
				records = body["items"]
			}
			var items []map[string]json.RawMessage
			json.Unmarshal(records, &items)
			for i, record := range items {
				projected := map[string]json.RawMessage{}
				for _, key := range []string{"id", "title", "description", "status", "priority", "project_id", "manual", "metadata"} {
					value, ok := record[key]
					if !ok {
						t.Fatalf("missing field %s", key)
					}
					projected[key] = value
				}
				expected, _ := json.Marshal(list.Tasks[i])
				actual, _ := json.Marshal(projected)
				var got, want any
				json.Unmarshal(actual, &got)
				json.Unmarshal(expected, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("lossy field projection: %s vs %s", actual, expected)
				}
			}
		})
	}
}

func TestRealTorqueGoldenHandshake(t *testing.T) {
	for _, shape := range []string{"legacy", "current"} {
		for _, operation := range []string{"list", "search"} {
			t.Run(shape+"-"+operation, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					name := shape + "-" + operation
					if r.URL.Query().Get("offset") != "0" {
						t.Error("missing explicit zero")
					}
					if r.URL.Query().Has("include_total") {
						if shape == "legacy" {
							http.Error(w, "unsupported query parameter include_total", 400)
							return
						}
						name += "-total"
					}
					w.Write(readTorqueGolden(t, name))
				}))
				defer server.Close()
				adapter := NewTorqueAdapter(server.URL)
				var list *WorkList
				var err error
				if operation == "list" {
					list, err = adapter.ListWorkItems(context.Background(), WorkFilters{Limit: 50})
				} else {
					list, err = adapter.SearchWorkItems(context.Background(), "golden-match")
				}
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := 1
				if shape == "current" {
					wantCalls = 2
				}
				if list.Total != 4 || len(list.Tasks) != 4 || list.HasMore || calls != wantCalls {
					t.Fatalf("golden handshake: %+v, requests=%d", list, calls)
				}
			})
		}
	}
}

func TestLegacySearchCapBoundaries(t *testing.T) {
	for _, count := range []int{200, 201, 350} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"tasks": make([]WorkItem, count)})
			}))
			defer server.Close()
			list, err := NewTorqueAdapter(server.URL).SearchWorkItems(context.Background(), "matches")
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tasks) != 200 || list.Total != count || list.HasMore != (count > 200) {
				t.Fatalf("legacy exact count before truncation: %+v", list)
			}
		})
	}
}

func TestMalformedListElementsAndTotals(t *testing.T) {
	for _, raw := range []string{
		`{"items":[null],"meta":{"returned":1,"limit":50,"offset":0,"has_more":false,"total":1}}`,
		`{"items":[123],"meta":{"returned":1,"limit":50,"offset":0,"has_more":false,"total":1}}`,
		`{"items":[[]],"meta":{"returned":1,"limit":50,"offset":0,"has_more":false,"total":1}}`,
		`{"items":[{"id":"one"}],"meta":{"returned":1,"limit":50,"offset":0,"has_more":false,"total":0}}`,
		`{"tasks":[{"id":"one"}],"has_more":true,"next_offset":1}`,
	} {
		t.Run(raw, func(t *testing.T) {
			list, _, _, err := decodeTorqueList(json.RawMessage(raw), 0, true)
			if err == nil || list != nil {
				t.Fatalf("accepted malformed list: %+v", list)
			}
		})
	}
	list, _, _, err := decodeTorqueList(json.RawMessage(`{"items":[],"meta":{"returned":0,"limit":50,"offset":100,"has_more":false,"total":1}}`), 100, false)
	if err != nil || list.HasMore || list.Total != 1 {
		t.Fatalf("past-end empty page rejected: %v %+v", err, list)
	}
	list, _, _, err = decodeTorqueList(json.RawMessage(`{"items":[{"id":"one","future_field":true}],"meta":{"returned":1,"limit":50,"offset":0,"has_more":false,"total":1}}`), 0, false)
	if err != nil || list.Tasks[0].ID != "one" {
		t.Fatalf("unknown object field rejected: %v %+v", err, list)
	}
}

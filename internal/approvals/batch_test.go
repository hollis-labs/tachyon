package approvals

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type batchFix struct {
	in       *Inbox
	elevated *bool
}

func newBatchFix(t *testing.T, elevated bool) batchFix {
	t.Helper()
	calls := 0
	up := fakeTangent(t, &elevated, "", &calls)
	return batchFix{in: NewInbox(nativeStore(t), New(up.URL)), elevated: &elevated}
}

func (f batchFix) rev(t *testing.T, id string) int64 {
	t.Helper()
	w := get(f.in, Prefix+"/items/"+id, "s1")
	var m struct {
		Revision int64  `json:"revision"`
		State    string `json:"state"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatalf("item %s: %d %s", id, w.Code, w.Body)
	}
	return m.Revision
}

func (f batchFix) state(t *testing.T, id string) string {
	t.Helper()
	var m struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(get(f.in, Prefix+"/items/"+id, "s1").Body.Bytes(), &m)
	return m.State
}

type bi struct {
	ID        string `json:"item_id"`
	Expected  int64  `json:"expected_revision"`
	Presented int64  `json:"presented_projection_revision"`
}

func (f batchFix) post(batchID, decision, note string, items ...bi) *httptest.ResponseRecorder {
	body := map[string]any{"batch_id": batchID, "decision": decision, "items": items}
	if note != "" {
		body["note"] = note
	}
	return serveJSON(f.in, "POST", Prefix+"/batch/resolve", body, "", "s1")
}

type batchOut struct {
	BatchID string `json:"batch_id"`
	Results []struct {
		ItemID   string   `json:"item_id"`
		Status   string   `json:"status"`
		Decision string   `json:"decision"`
		Revision int64    `json:"revision"`
		Blockers []string `json:"blockers"`
		Error    *struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"results"`
	Counts map[string]int `json:"counts"`
}

func decode(t *testing.T, w *httptest.ResponseRecorder) batchOut {
	t.Helper()
	var o batchOut
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
		t.Fatalf("batch: %d %s", w.Code, w.Body)
	}
	return o
}

func TestBatchResolveMixedOutcomes(t *testing.T) {
	f := newBatchFix(t, true)
	ok1 := enqueue(t, f.in, seed{key: "ok1", class: "low", agent: "builder", extra: map[string]any{"effects": fx("update")}})
	ok2 := enqueue(t, f.in, seed{key: "ok2", class: "low", agent: "builder", extra: map[string]any{"effects": fx("create")}})
	none := enqueue(t, f.in, seed{key: "none", class: "low", agent: "builder"})
	del := enqueue(t, f.in, seed{key: "del", class: "medium", agent: "builder", extra: map[string]any{"effects": fx("delete")}})
	stale := enqueue(t, f.in, seed{key: "stale", class: "low", agent: "builder", extra: map[string]any{"effects": fx("restart")}})
	gone := enqueue(t, f.in, seed{key: "gone", class: "low", agent: "builder", extra: map[string]any{"effects": fx("update")}})
	short := enqueue(t, f.in, seed{key: "short", class: "low", agent: "builder", expires: 40 * time.Millisecond, extra: map[string]any{"effects": fx("update")}})
	last := enqueue(t, f.in, seed{key: "last", class: "low", agent: "builder", extra: map[string]any{"effects": fx("update")}})

	// decide `gone` first through a one-item batch so the big batch finds it terminal
	r := f.rev(t, gone)
	if o := decode(t, f.post("pre", "denied", "", bi{gone, r, r})); o.Results[0].Status != "resolved" {
		t.Fatalf("pre-decide: %+v", o)
	}
	time.Sleep(80 * time.Millisecond)

	items := []bi{}
	for _, id := range []string{ok1, none, del, stale, gone, short, "does-not-exist", ok2, last} {
		rv := int64(1)
		if id != "does-not-exist" {
			rv = f.rev(t, id)
		}
		if id == stale {
			rv += 7
		}
		items = append(items, bi{id, rv, rv})
	}
	o := decode(t, f.post("b1", "approved", "", items...))
	want := []struct{ id, status string }{{ok1, "resolved"}, {none, "ineligible"}, {del, "ineligible"}, {stale, "stale"}, {gone, "conflict"},
		{short, "expired"}, {"does-not-exist", "error"}, {ok2, "resolved"}, {last, "resolved"}}
	if len(o.Results) != len(want) {
		t.Fatalf("results: %+v", o.Results)
	}
	for k, w := range want {
		g := o.Results[k]
		if g.ItemID != w.id || g.Status != w.status {
			t.Errorf("#%d %s: got %s want %s (%+v)", k, w.id, g.Status, w.status, g)
		}
	}
	if o.Counts["resolved"] != 3 || o.Counts["ineligible"] != 2 || o.Counts["stale"] != 1 || o.Counts["conflict"] != 1 || o.Counts["expired"] != 1 || o.Counts["error"] != 1 {
		t.Errorf("counts: %v", o.Counts)
	}
	if b := o.Results[1].Blockers; len(b) != 1 || b[0] != "no_effects_declared" {
		t.Errorf("blockers: %v", b)
	}
	if o.Results[4].Decision != "denied" || o.Results[3].Error == nil || o.Results[3].Error.Code != "stale_revision" || o.Results[6].Error.Code != "not_found" {
		t.Errorf("details: %+v", o.Results)
	}
	if o.Results[0].Decision != "approved" || o.Results[0].Revision < 2 {
		t.Errorf("resolved result: %+v", o.Results[0])
	}
	for _, id := range []string{ok1, ok2, last} {
		if s := f.state(t, id); s != "resolved" {
			t.Errorf("%s should be resolved, is %s", id, s)
		}
	}
	for _, id := range []string{none, del, stale} {
		if s := f.state(t, id); s == "resolved" {
			t.Errorf("%s must stay pending", id)
		}
	}
}

func TestBatchResolveNeverTakesHighSecretOrCommandItems(t *testing.T) {
	f := newBatchFix(t, true)
	ids := map[string]string{
		"high":     enqueue(t, f.in, seed{key: "high", class: "high", agent: "b", extra: map[string]any{"effects": fx("update")}}),
		"command":  enqueue(t, f.in, seed{key: "cmd", class: "low", agent: "b", extra: map[string]any{"action": "deploy", "argv": []string{"x"}, "effects": fx("update")}}),
		"archive":  enqueue(t, f.in, seed{key: "arc", class: "low", agent: "b", extra: map[string]any{"effects": fx("archive")}}),
		"question": enqueue(t, f.in, seed{key: "q", kind: "card.question", class: "low", agent: "b", extra: map[string]any{"effects": fx("update")}}),
	}
	var items []bi
	for _, id := range ids {
		rv := f.rev(t, id)
		items = append(items, bi{id, rv, rv})
	}
	o := decode(t, f.post("b2", "approved", "", items...))
	for _, r := range o.Results {
		if r.Status != "ineligible" || len(r.Blockers) == 0 {
			t.Errorf("%+v must be ineligible", r)
		}
		if s := f.state(t, r.ItemID); s == "resolved" {
			t.Errorf("%s was resolved", r.ItemID)
		}
	}
}

func TestBatchResolveReplayAndReuse(t *testing.T) {
	f := newBatchFix(t, true)
	a := enqueue(t, f.in, seed{key: "a", class: "low", agent: "b", extra: map[string]any{"effects": fx("update")}})
	b := enqueue(t, f.in, seed{key: "b", class: "low", agent: "b", extra: map[string]any{"effects": fx("update")}})
	ra, rb := f.rev(t, a), f.rev(t, b)
	first := f.post("replay-1", "approved", "", bi{a, ra, ra})
	if first.Code != 200 || first.Header().Get("X-Tachyon-Batch-Replay") != "" {
		t.Fatalf("first: %d %v", first.Code, first.Header())
	}
	again := f.post("replay-1", "approved", "", bi{a, ra, ra})
	if again.Code != 200 || again.Header().Get("X-Tachyon-Batch-Replay") != "1" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay must return the stored answer: %d %s vs %s", again.Code, again.Body, first.Body)
	}
	if w := f.post("replay-1", "approved", "", bi{a, ra, ra}, bi{b, rb, rb}); w.Code != 409 || !strings.Contains(w.Body.String(), "batch_id_reuse") {
		t.Fatalf("same id, different request: %d %s", w.Code, w.Body)
	}
	if f.state(t, b) == "resolved" {
		t.Fatal("a refused reuse must decide nothing")
	}
	var n int
	if err := f.in.Store.DB.Reader.QueryRow(`SELECT COUNT(*) FROM approval_batches`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("stored batches: %d %v", n, err)
	}
}

func TestBatchResolveNeedsElevatedAccess(t *testing.T) {
	f := newBatchFix(t, false)
	a := enqueue(t, f.in, seed{key: "a", class: "low", agent: "b", extra: map[string]any{"effects": fx("update")}})
	ra := f.rev(t, a)
	w := f.post("e1", "approved", "", bi{a, ra, ra})
	if w.Code != 403 || !strings.Contains(w.Body.String(), "assurance_insufficient") {
		t.Fatalf("not elevated: %d %s", w.Code, w.Body)
	}
	if f.state(t, a) == "resolved" {
		t.Fatal("nothing may be decided without elevation")
	}
	*f.elevated = true
	if w = f.post("e1", "approved", "", bi{a, ra, ra}); w.Code != 200 {
		t.Fatalf("elevated: %d %s", w.Code, w.Body)
	}
}

func TestBatchResolveValidation(t *testing.T) {
	f := newBatchFix(t, true)
	a := enqueue(t, f.in, seed{key: "a", class: "low", agent: "b", extra: map[string]any{"effects": fx("update")}})
	ra := f.rev(t, a)
	var many []bi
	for k := 0; k < 26; k++ {
		many = append(many, bi{a + string(rune('a'+k)), 1, 1})
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"no items":        f.post("v1", "approved", ""),
		"26 items":        f.post("v2", "approved", "", many...),
		"duplicate":       f.post("v3", "approved", "", bi{a, ra, ra}, bi{a, ra, ra}),
		"bad decision":    f.post("v4", "maybe", "", bi{a, ra, ra}),
		"bad batch id":    f.post("has space", "approved", "", bi{a, ra, ra}),
		"empty batch id":  f.post("", "approved", "", bi{a, ra, ra}),
		"zero revision":   f.post("v5", "approved", "", bi{a, 0, ra}),
		"missing present": f.post("v6", "approved", "", bi{a, ra, 0}),
		"long note":       f.post("v7", "approved", strings.Repeat("x", 2001), bi{a, ra, ra}),
		"unknown field":   serveJSON(f.in, "POST", Prefix+"/batch/resolve", map[string]any{"batch_id": "v8", "decision": "approved", "items": []bi{{a, ra, ra}}, "x": 1}, "", "s1"),
	} {
		if w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if f.state(t, a) == "resolved" {
		t.Fatal("invalid requests must decide nothing")
	}
	// a cross-origin POST is refused like any other write
	r := httptest.NewRequest("POST", "http://tachyon.test"+Prefix+"/batch/resolve", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	f.in.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("origin: %d", rec.Code)
	}
}

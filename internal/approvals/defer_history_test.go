package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func jsonOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return m
}

func TestDeferOverlayOrderingClampAndErrors(t *testing.T) {
	in := newInbox(t)
	ctx := context.Background()
	start := Changes(in.Store.DB).Head()
	a := enqueue(t, in, seed{key: "a", class: "low", agent: "x", expires: 2 * time.Hour, extra: map[string]any{"effects": fx("update")}})
	b := enqueue(t, in, seed{key: "b", class: "low", agent: "x", expires: 3 * time.Hour, extra: map[string]any{"effects": fx("update")}})
	short := enqueue(t, in, seed{key: "short", class: "low", agent: "x", expires: 5 * time.Minute, extra: map[string]any{"effects": fx("update")}})
	path := func(id string) string { return Prefix + "/items/" + id + "/defer" }

	w := serveJSON(in, "POST", path(a), map[string]any{"expected_revision": 1}, "", "s1")
	res := jsonOf(t, w.Body.String())
	until, _ := time.Parse(time.RFC3339, res["deferred_until"].(string))
	if w.Code != 200 || res["expires_before_snooze_ends"] != false || until.Sub(time.Now()) < 55*time.Minute || until.Sub(time.Now()) > 61*time.Minute {
		t.Fatalf("default 1h: %d %s", w.Code, w.Body)
	}
	w = serveJSON(in, "POST", path(short), map[string]any{"expected_revision": 1, "duration_s": 3600, "reason": "later"}, "", "s1")
	res = jsonOf(t, w.Body.String())
	if w.Code != 200 || res["expires_before_snooze_ends"] != true {
		t.Fatalf("clamped to expiry: %d %s", w.Code, w.Body)
	}
	if u, _ := time.Parse(time.RFC3339, res["deferred_until"].(string)); u.After(time.Now().Add(5*time.Minute + 2*time.Second)) {
		t.Errorf("a snooze must never extend expiry: %v", u)
	}
	l, _ := list(t, in, "")
	got := ids(l.Pending)
	if got[0] != b || l.Counts["deferred"].(float64) != 2 || l.Counts["batchable"].(float64) != 1 {
		t.Errorf("deferred items sort last and leave the batchable count: %v %v", got, l.Counts)
	}
	for _, it := range l.Pending {
		if it["item_id"] == a {
			if it["deferred_until"] == nil || it["batchable"] != false || !strings.Contains(fmt.Sprint(it["batch_blockers"]), "deferred") {
				t.Errorf("deferred item projection: %v", it)
			}
		}
		if it["item_id"] == b && it["deferred_until"] != nil {
			t.Error("undeferred item has deferred_until")
		}
	}

	for name, body := range map[string]map[string]any{
		"stale":    {"expected_revision": 9},
		"short":    {"expected_revision": 1, "duration_s": 10},
		"long":     {"expected_revision": 1, "duration_s": 700000},
		"both":     {"expected_revision": 1, "duration_s": 60, "until_ms": 1},
		"norev":    {"duration_s": 60},
		"unknown":  {"expected_revision": 1, "wat": 1},
		"longnote": {"expected_revision": 1, "reason": strings.Repeat("x", 281)},
	} {
		w := serveJSON(in, "POST", path(b), body, "", "s1")
		want := 400
		if name == "stale" {
			want = 409
		}
		if w.Code != want {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := serveJSON(in, "POST", path(b), map[string]any{"expected_revision": 1}, "", ""); w.Code != 401 {
		t.Errorf("no session: %d", w.Code)
	}
	if w := serveJSON(in, "DELETE", Prefix+"/items/"+b, nil, "", "s1"); w.Code != 405 {
		t.Errorf("DELETE is only for /defer: %d", w.Code)
	}
	if w := serveJSON(in, "DELETE", path(a), nil, "", "s1"); w.Code != 200 || jsonOf(t, w.Body.String())["deferred_until"] != nil {
		t.Errorf("undefer: %d %s", w.Code, w.Body)
	}
	if w := serveJSON(in, "DELETE", path(a), nil, "", "s1"); w.Code != 200 {
		t.Errorf("undefer twice is fine: %d", w.Code)
	}
	// Resolving a deferred item clears the overlay and it cannot be deferred again.
	serveJSON(in, "POST", path(b), map[string]any{"expected_revision": 1}, "", "s1")
	cmd := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "approved"}}
	if w := serveJSON(in, "POST", Prefix+"/items/"+b+"/resolve", cmd, "", "s1"); w.Code != 200 {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	if in.Store.DeferredUntil(ctx, b, time.Now()) != nil {
		t.Error("the overlay must be gone after resolve")
	}
	if w := serveJSON(in, "POST", path(b), map[string]any{"expected_revision": 2}, "", "s1"); w.Code != 409 {
		t.Errorf("defer a decided item: %d %s", w.Code, w.Body)
	}
	ev := sseEvents(get(in, Prefix+"/events?once=1&after="+fmt.Sprint(start), "s1").Body.String())
	types := map[string]bool{}
	for _, e := range ev {
		types[e.Data["type"].(string)] = true
	}
	if !types["item.deferred"] || !types["item.undeferred"] {
		t.Errorf("stream lacks defer events: %v", types)
	}
}

func TestHistoryTimelineAuditAndAppendOnlyLog(t *testing.T) {
	in := newInbox(t)
	ctx := context.Background()
	a := enqueue(t, in, seed{key: "a", class: "low", agent: "alice", extra: map[string]any{"effects": fx("update")}})
	b := enqueue(t, in, seed{key: "b", class: "low", agent: "bob", extra: map[string]any{"effects": fx("update")}})
	c := enqueue(t, in, seed{key: "c", class: "low", agent: "bob", expires: 300 * time.Millisecond, extra: map[string]any{"effects": fx("update")}})
	post := func(id, op string, body any) {
		if w := serveJSON(in, "POST", Prefix+"/items/"+id+"/"+op, body, "", "s1"); w.Code != 200 {
			t.Fatalf("%s %s: %d %s", op, id, w.Code, w.Body)
		}
	}
	post(a, "present", map[string]any{"expected_revision": 1, "presented_projection_revision": 1})
	post(a, "present", map[string]any{"expected_revision": 1, "presented_projection_revision": 1})
	post(a, "defer", map[string]any{"expected_revision": 1, "reason": "after lunch"})
	post(a, "resolve", map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "approved", "note": "Looks right"}})
	post(b, "resolve", map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "denied", "note": "Wrong target"}})
	time.Sleep(450 * time.Millisecond)
	if n, err := in.Service.ExpireDue(ctx); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}

	w := get(in, Prefix+"/history", "s1")
	h := jsonOf(t, w.Body.String())
	evs := h["events"].([]any)
	if w.Code != 200 || len(evs) != 3 {
		t.Fatalf("history: %d %s", w.Code, w.Body)
	}
	byItem := map[string]map[string]any{}
	for _, e := range evs {
		m := e.(map[string]any)
		byItem[m["item_id"].(string)] = m
	}
	ra, rb, rc := byItem[a], byItem[b], byItem[c]
	if ra["kind"] != "resolved" || ra["decision"] != "approved" || ra["note"] != "Looks right" || ra["actor"].(map[string]any)["label"] != "You" || ra["mini"].(map[string]any)["title"] != "Title a" {
		t.Errorf("approved row: %v", ra)
	}
	if rb["status"] != "denied" || rb["reason"] != "denied" || rb["note"] != "Wrong target" {
		t.Errorf("denied row: %v", rb)
	}
	if rc["kind"] != "expired" || rc["reason"] != "deadline" || rc["actor"].(map[string]any)["label"] != "System" {
		t.Errorf("expired row: %v", rc)
	}
	other := jsonOf(t, get(in, Prefix+"/history", "another-session").Body.String())["events"].([]any)
	for _, e := range other {
		if e.(map[string]any)["item_id"] == a && e.(map[string]any)["actor"].(map[string]any)["label"] != "Operator session" {
			t.Error("another session sees the decider as an operator session")
		}
	}
	for q, want := range map[string]int{"?status=denied": 1, "?status=expired": 1, "?requester=bob": 2, "?requester=alice": 1, "?q=wrong": 1, "?q=title+a": 1, "?limit=2": 2} {
		if n := len(jsonOf(t, get(in, Prefix+"/history"+q, "s1").Body.String())["events"].([]any)); n != want {
			t.Errorf("%s: %d want %d", q, n, want)
		}
	}
	p1 := jsonOf(t, get(in, Prefix+"/history?limit=2", "s1").Body.String())
	p2 := jsonOf(t, get(in, Prefix+"/history?limit=2&cursor="+p1["next_cursor"].(string), "s1").Body.String())
	if len(p2["events"].([]any)) != 1 || p2["next_cursor"] != nil {
		t.Errorf("page 2: %v", p2)
	}
	if n := len(jsonOf(t, get(in, Prefix+"/history?kind=deferred", "s1").Body.String())["events"].([]any)); n != 1 {
		t.Errorf("kind=deferred: %d", n)
	}
	for _, bad := range []string{"?status=x", "?kind=x", "?from=yesterday", "?limit=0", "?cursor=x"} {
		if get(in, Prefix+"/history"+bad, "s1").Code != 400 {
			t.Errorf("%s accepted", bad)
		}
	}
	// Audit: presented once (idempotent per session), deferred with its reason, then resolved.
	au := jsonOf(t, get(in, Prefix+"/items/"+a+"/audit", "s1").Body.String())["events"].([]any)
	var kinds []string
	for _, e := range au {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	if strings.Join(kinds, ",") != "presented,deferred,resolved" {
		t.Errorf("audit kinds: %v", kinds)
	}
	if au[1].(map[string]any)["note"] != "after lunch" || au[1].(map[string]any)["until"] == nil {
		t.Errorf("deferred event: %v", au[1])
	}
	if get(in, Prefix+"/items/item_nope/audit", "s1").Code != 404 {
		t.Error("audit of unknown item")
	}
	if get(in, Prefix+"/history", "").Code != 401 {
		t.Error("history needs a session")
	}

	// Append-only, and repairable from the records.
	err := in.Store.DB.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE approval_decision_log SET note='x'`)
		return err
	})
	if err == nil {
		t.Error("the log must refuse updates")
	}
	err = in.Store.DB.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_decision_log`)
		return err
	})
	if err == nil {
		t.Error("the log must refuse deletes")
	}
	err = in.Store.DB.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{`DROP TRIGGER approval_decision_log_no_delete`, `DELETE FROM approval_decision_log WHERE kind IN ('resolved','expired')`} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := in.Store.BackfillLog(ctx); err != nil || n != 3 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	if n, _ := in.Store.BackfillLog(ctx); n != 0 {
		t.Errorf("second backfill: %d", n)
	}
	if n := len(jsonOf(t, get(in, Prefix+"/history", "s1").Body.String())["events"].([]any)); n != 3 {
		t.Errorf("history after repair: %d", n)
	}
}

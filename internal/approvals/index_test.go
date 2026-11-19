package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

type seed struct {
	key, kind, class, agent string
	extra                   map[string]any
	expires                 time.Duration
}

func enqueue(t *testing.T, in *Inbox, s seed) string {
	t.Helper()
	req := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: s.kind, IdempotencyKey: s.key,
		Source: hitl.SourceAssertion{ApplicationID: "test-producer", AgentID: s.agent}, Extra: map[string]json.RawMessage{}}
	if req.Kind == "" {
		req.Kind = "approval"
	}
	put := func(k string, v any) { req.Extra[k], _ = json.Marshal(v) }
	put("title", "Title "+s.key)
	put("summary", "Summary for "+s.key)
	put("request", "do it")
	put("class", s.class)
	put("op_digest", "sha256:"+strings.Repeat("a", 64))
	for k, v := range s.extra {
		put(k, v)
	}
	if s.expires != 0 {
		at := time.Now().Add(s.expires)
		req.ExpiresAt = &at
	}
	h, err := in.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", s.key, err)
	}
	return h.ItemID
}

func fx(op string) []map[string]string { return []map[string]string{{"target": "x:" + op, "op": op}} }

type listed struct {
	Pending []map[string]any `json:"pending"`
	History []map[string]any `json:"history"`
	NextP   string           `json:"next_pending"`
	NextH   string           `json:"next_history"`
	Now     int64            `json:"server_now_ms"`
	Counts  map[string]any   `json:"counts"`
}

func list(t *testing.T, in *Inbox, query string) (listed, int) {
	t.Helper()
	w := get(in, Prefix+"/inbox"+query, "s1")
	var l listed
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", w.Body, err)
		}
	}
	return l, w.Code
}

func ids(items []map[string]any) (out []string) {
	for _, it := range items {
		out = append(out, it["item_id"].(string))
	}
	return
}

func newInbox(t *testing.T) *Inbox {
	elevated, calls := false, 0
	up := fakeTangent(t, &elevated, "", &calls)
	return NewInbox(nativeStore(t), New(up.URL))
}

func TestProjectionGroupsRiskAndBatchEligibility(t *testing.T) {
	in := newInbox(t)
	ok := enqueue(t, in, seed{key: "ok", class: "low", agent: "builder", extra: map[string]any{"effects": fx("update")}})
	none := enqueue(t, in, seed{key: "none", class: "low", agent: "builder"})
	del := enqueue(t, in, seed{key: "del", class: "medium", agent: "builder", extra: map[string]any{"effects": fx("delete")}})
	cmd := enqueue(t, in, seed{key: "cmd", class: "medium", agent: "runner", extra: map[string]any{"action": "deploy", "argv": []string{"deploy"}, "effects": fx("run")}})
	sec := enqueue(t, in, seed{key: "sec", kind: "card.form", class: "medium", agent: "builder", extra: map[string]any{"data": map[string]any{"fields": []map[string]string{{"type": "secret"}}}}})
	q := enqueue(t, in, seed{key: "q", kind: "card.question", class: "low", agent: "builder"})
	note := enqueue(t, in, seed{key: "note", kind: "attention", class: "low", agent: "builder"})
	l, code := list(t, in, "")
	if code != 200 || len(l.Pending) != 7 {
		t.Fatalf("list: %d %d", code, len(l.Pending))
	}
	by := map[string]map[string]any{}
	for _, it := range l.Pending {
		by[it["item_id"].(string)] = it
	}
	check := func(id, group, risk string, batchable bool, blockers ...string) {
		it := by[id]
		got := []string{}
		for _, b := range it["batch_blockers"].([]any) {
			got = append(got, b.(string))
		}
		if it["group"] != group || it["risk"] != risk || it["batchable"] != batchable || strings.Join(got, ",") != strings.Join(blockers, ",") {
			t.Errorf("%s: group=%v risk=%v batchable=%v blockers=%v", id, it["group"], it["risk"], it["batchable"], got)
		}
		if it["server_now_ms"] == nil || it["asked_at"] == nil || it["presented_projection_revision"] == nil {
			t.Errorf("%s: missing base fields", id)
		}
	}
	check(ok, "change", "low", true)
	check(none, "change", "low", false, "no_effects_declared")
	check(del, "destructive", "medium", false, "risk_not_low", "destructive", "irreversible_effect")
	check(cmd, "command", "medium", false, "risk_not_low", "command_exec", "irreversible_effect")
	check(sec, "secret", "medium", false, "kind_not_batchable", "risk_not_low", "interactive", "has_secret", "no_effects_declared")
	check(q, "question", "low", false, "kind_not_batchable", "interactive", "no_effects_declared")
	check(note, "notice", "low", false, "kind_not_batchable", "no_effects_declared")
	if by[ok]["requester"].(map[string]any)["id"] != "builder" || by[ok]["effect_classes"].([]any)[0] != "update" {
		t.Errorf("requester/effect_classes: %v", by[ok])
	}
	if l.Counts["pending"].(float64) != 7 || l.Counts["batchable"].(float64) != 1 || l.Counts["by_group"].(map[string]any)["change"].(float64) != 2 {
		t.Errorf("counts: %v", l.Counts)
	}
	if by[ok]["destructive"] != nil || by[ok]["affected_total"] != float64(0) {
		t.Error("an ordinary item is not destructive and affects nothing")
	}
	if ev, _ := by[ok]["evidence"].([]any); ev == nil || len(ev) != 0 || by[ok]["run_state"] != "none" {
		t.Errorf("an item without evidence lists none and has no run: %v %v", by[ok]["evidence"], by[ok]["run_state"])
	}
}

func TestFiltersSearchOrderingAndPaging(t *testing.T) {
	in := newInbox(t)
	late := enqueue(t, in, seed{key: "late", class: "low", agent: "a", expires: 2 * time.Hour, extra: map[string]any{"effects": fx("update")}})
	soon := enqueue(t, in, seed{key: "soon", class: "low", agent: "b", expires: time.Hour, extra: map[string]any{"effects": fx("update")}})
	never := enqueue(t, in, seed{key: "never", class: "high", agent: "a", extra: map[string]any{"effects": fx("update")}})
	soonHigh := enqueue(t, in, seed{key: "soonhigh", class: "high", agent: "a", expires: time.Hour, extra: map[string]any{"effects": fx("restart")}})
	l, _ := list(t, in, "")
	got := ids(l.Pending)
	// soonest expiry first; among equal-ish expiry the later-created one expires later; no expiry last
	if got[len(got)-1] != never || got[0] != soon && got[0] != soonHigh {
		t.Errorf("urgency order: %v", got)
	}
	if idx := func(id string) int {
		for i, g := range got {
			if g == id {
				return i
			}
		}
		return -1
	}; idx(soon) > idx(late) || idx(soonHigh) > idx(late) {
		t.Errorf("both hour-expiry items come before the two-hour one: %v", got)
	}
	l, _ = list(t, in, "?order=created")
	if ids(l.Pending)[0] != soonHigh {
		t.Errorf("created order is newest first: %v", ids(l.Pending))
	}
	if l, _ = list(t, in, "?risk=high"); len(l.Pending) != 2 {
		t.Errorf("risk filter: %v", ids(l.Pending))
	}
	if l, _ = list(t, in, "?batchable=1"); len(l.Pending) != 2 {
		t.Errorf("batchable filter: %d", len(l.Pending))
	}
	if l, _ = list(t, in, "?requester=b"); len(l.Pending) != 1 || l.Pending[0]["item_id"] != soon {
		t.Errorf("requester filter: %v", ids(l.Pending))
	}
	if l, _ = list(t, in, "?q=TITLE+SOONHIGH"); len(l.Pending) != 1 || l.Pending[0]["item_id"] != soonHigh {
		t.Errorf("search is case-insensitive over title: %v", ids(l.Pending))
	}
	if l, _ = list(t, in, "?q=%25"); len(l.Pending) != 0 {
		t.Errorf("wildcards are literal: %d", len(l.Pending))
	}
	if l, _ = list(t, in, "?status=pending&group=change"); len(l.History) != 0 || len(l.Pending) != 4 || l.Counts["pending"].(float64) != 4 {
		t.Errorf("status pending: %+v", l.Counts)
	}
	// paging in urgency order by item-id cursor
	p1, _ := list(t, in, "?limit=3")
	if len(p1.Pending) != 3 || p1.NextP == "" {
		t.Fatalf("page 1: %v next=%q", ids(p1.Pending), p1.NextP)
	}
	p2, _ := list(t, in, "?limit=3&pending_before="+p1.NextP)
	if len(p2.Pending) != 1 || p2.NextP != "" || p2.Pending[0]["item_id"] != got[3] {
		t.Errorf("page 2: %v", ids(p2.Pending))
	}
	p2b, _ := list(t, in, "?limit=3&cursor="+p1.NextP)
	if strings.Join(ids(p2b.Pending), ",") != strings.Join(ids(p2.Pending), ",") {
		t.Error("cursor is pending_before for the pending side")
	}
	for _, bad := range []string{"?order=x", "?group=nope", "?risk=x", "?batchable=0", "?status=x", "?limit=0", "?limit=101", "?pending_before=missing", "?history_status=bogus"} {
		if _, code := list(t, in, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

func TestIndexFollowsRecordsHistoryFiltersAndBackfill(t *testing.T) {
	in := newInbox(t)
	a := enqueue(t, in, seed{key: "a", class: "low", agent: "x", extra: map[string]any{"effects": fx("update")}})
	b := enqueue(t, in, seed{key: "b", class: "low", agent: "y", extra: map[string]any{"effects": fx("update")}})
	enqueue(t, in, seed{key: "c", class: "low", agent: "x", extra: map[string]any{"effects": fx("update")}})
	resolve := func(id, decision string) {
		cmd := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": decision}}
		if w := serveJSON(in, "POST", Prefix+"/items/"+id+"/resolve", cmd, "", "s1"); w.Code != 200 {
			t.Fatalf("resolve %s: %d %s", id, w.Code, w.Body)
		}
	}
	resolve(a, "approved")
	resolve(b, "denied")
	l, _ := list(t, in, "")
	if len(l.Pending) != 1 || len(l.History) != 2 {
		t.Fatalf("after resolve: %d pending %d history", len(l.Pending), len(l.History))
	}
	if l, _ = list(t, in, "?status=history&history_status=denied"); len(l.History) != 1 || l.History[0]["item_id"] != b {
		t.Errorf("denied: %v", ids(l.History))
	}
	if l, _ = list(t, in, "?status=history&requester=x"); len(l.History) != 1 || l.History[0]["item_id"] != a {
		t.Errorf("requester: %v", ids(l.History))
	}
	if l, _ = list(t, in, "?status=history&history_status=approved&limit=1"); len(l.History) != 1 || l.NextH != "" {
		t.Errorf("approved: %v", l.NextH)
	}
	if h1, _ := list(t, in, "?status=history&limit=1&group=change"); len(h1.History) != 1 || h1.NextH == "" {
		t.Errorf("filtered history paging: %q", h1.NextH)
	} else if h2, _ := list(t, in, "?status=history&limit=1&group=change&history_before="+h1.NextH); len(h2.History) != 1 || h2.History[0]["item_id"] == h1.History[0]["item_id"] {
		t.Errorf("filtered history page 2: %v", ids(h2.History))
	}
	// Wipe the index (an upgrade from before it existed): Reindex restores it.
	if _, err := in.Store.DB.Reader.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if err := in.Store.DB.Writer.Submit(context.Background(), "t", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_item_index`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if l, _ = list(t, in, ""); len(l.Pending) != 0 {
		t.Fatal("expected an empty queue without the index")
	}
	n, err := in.Store.Reindex(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("reindex: %d %v", n, err)
	}
	if n, _ = in.Store.Reindex(context.Background()); n != 0 {
		t.Errorf("second reindex should find nothing: %d", n)
	}
	if l, _ = list(t, in, ""); len(l.Pending) != 1 || len(l.History) != 2 {
		t.Errorf("restored: %d/%d", len(l.Pending), len(l.History))
	}
}

func TestSweeperMaterializesExpiryAndPublishes(t *testing.T) {
	in := newInbox(t)
	id := enqueue(t, in, seed{key: "short", class: "low", agent: "x", expires: 400 * time.Millisecond, extra: map[string]any{"effects": fx("update")}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		in.Maintain(ctx, 50*time.Millisecond, func(string, ...any) {})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec, err := in.Store.Get(ctx, id)
		if err == nil && rec.State == hitl.StateExpired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not expired: %v %v", rec.State, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if l, _ := list(t, in, "?status=history&history_status=expired"); len(l.History) != 1 || l.History[0]["state"] != "expired" {
		t.Errorf("history: %v", l.History)
	}
	ev := sseEvents(get(in, Prefix+"/events?once=1&after="+fmt.Sprint(Changes(in.Store.DB).Head()-2), "s1").Body.String())
	found := false
	for _, e := range ev {
		found = found || e.Data["type"] == "item.expired" && e.Data["item_id"] == id
	}
	if !found {
		t.Errorf("no item.expired event: %v", ev)
	}
}

package agentscollector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func busFrame(id int, kind, app, payload string) string {
	return fmt.Sprintf(`{"id":"e:%d","kind":%q,"app":%q,"time":"2026-10-30T01:00:%02dZ","payload":%s}`, id, kind, app, id%60, payload)
}

func normalize(t *testing.T, raw string) BusEvent {
	t.Helper()
	f, ok := ParseFrame([]byte(raw))
	if !ok {
		t.Fatal("frame did not parse")
	}
	b, ok := NormalizeBus(f)
	if !ok {
		t.Fatalf("%s is not a feed event", raw)
	}
	return b
}

func TestNormalizeBusMapsTheCatalogKinds(t *testing.T) {
	for name, c := range map[string]struct {
		raw            string
		typ, subj, sev string
		card, review   bool
		refKinds       string
	}{
		"deploy ok":     {busFrame(1, "deploy.state_changed", "cerberus", `{"resource":"tachyon","state":"succeeded","version":"v0.31.2","pr":"hollis-labs/tachyon#166"}`), "deploy", "tachyon deployed (v0.31.2)", "info", true, false, "pr"},
		"deploy failed": {busFrame(2, "deploy.state_changed", "cerberus", `{"resource":"nanite","state":"failed"}`), "deploy", "nanite deploy failed", "error", true, false, ""},
		"rolled back":   {busFrame(3, "deploy.state_changed", "cerberus", `{"resource":"nanite","state":"rolled_back"}`), "deploy", "nanite rolled back", "warn", true, false, ""},
		"restart":       {busFrame(4, "resource.state_changed", "cerberus", `{"name":"hub-agent","reason":"restart"}`), "restart", "hub-agent restarted", "warn", false, false, ""},
		"alert":         {busFrame(5, "alert.raised", "cerberus", `{"name":"nanite health","title":"3 consecutive failures"}`), "failure", "nanite health alert raised", "error", true, false, ""},
		"alert cleared": {busFrame(6, "alert.cleared", "cerberus", `{"name":"nanite health"}`), "failure", "nanite health recovered", "info", false, false, ""},
		"task closed":   {busFrame(7, "task.state_changed", "torque", `{"task_id":"CW-20261030-0002","state":"done","title":"Threads","pr":"hollis-labs/tachyon#171"}`), "task", "CW-20261030-0002 done", "info", true, true, "task,pr"},
		"host":          {busFrame(8, "fleet.node.down", "cerberus", `{"name":"hub"}`), "host", "hub down", "warn", false, false, ""},
		"workstream":    {busFrame(9, "workstream.created", "tether", `{"name":"agents-gui"}`), "workstream", "workstream agents-gui created", "info", false, false, ""},
		"card by id":    {busFrame(11, "card.resolved", "tachyon", `{"card_id":"card_8f3a9c21d0","card_type":"form"}`), "card", "card resolved: form card_8f3", "info", false, false, ""},
		"card":          {busFrame(10, "card.answered", "tachyon", `{"name":"Which project?"}`), "card", "card answered: Which project?", "info", false, false, ""},
	} {
		b := normalize(t, c.raw)
		kinds := []string{}
		for _, r := range b.Refs {
			kinds = append(kinds, r.Kind)
		}
		if b.Type != c.typ || b.Subject != c.subj || b.Severity != c.sev || (b.Card != nil) != c.card || strings.Join(kinds, ",") != c.refKinds {
			t.Errorf("%s: %+v card=%v refs=%v", name, b, b.Card != nil, kinds)
		}
		if c.review && (b.Card == nil || b.Card.ReviewRef == nil || *b.Card.ReviewRef != "hollis-labs/tachyon#171") {
			t.Errorf("%s: a closed task with a PR names it as review_ref: %+v", name, b.Card)
		}
	}
	f, _ := ParseFrame([]byte(`{"id":"e:11","kind":"session.ended","session_id":"abcdef0123456789","time":"2026-10-30T01:00:11Z","payload":{"reason":"restart"}}`))
	if b, ok := NormalizeBus(f); !ok || b.Type != "lifecycle" || b.Subject != "abcdef0123456789 ended (restart)" || b.Refs[0].Kind != "session" {
		t.Errorf("lifecycle: %+v", b)
	}
	for _, kind := range []string{"session.output", "session.work_state_changed", "tool.call.finished", "resource.state_changed"} {
		f, _ := ParseFrame([]byte(busFrame(12, kind, "x", `{"name":"x"}`)))
		if _, ok := NormalizeBus(f); ok {
			t.Errorf("%s must not reach the feed", kind)
		}
	}
}

func TestNormalizeBusNeverEchoesPayloadText(t *testing.T) {
	secret := "ghp_" + strings.Repeat("Zq1X", 8)
	b := normalize(t, busFrame(1, "alert.raised", "cerberus", `{"name":"disk","title":"token `+secret+` leaked","body":"`+secret+`","detail":"`+secret+`"}`))
	blob, _ := json.Marshal(b)
	if strings.Contains(string(blob), secret) {
		t.Fatalf("secret in the event: %s", blob)
	}
	bad := normalize(t, busFrame(2, "deploy.state_changed", "cerberus", `{"resource":"x; rm -rf /\n<script>","state":"Succeeded!","pr":"not a pr","version":"v1\u0000"}`))
	if strings.ContainsAny(bad.Subject, ";<>") || len(bad.Refs) != 0 {
		t.Fatalf("an invalid name, state or pr must be dropped: %+v", bad)
	}
}

func TestBusFeedIsStoredIdempotentlyAndServed(t *testing.T) {
	src := &fakeSource{snap: Snapshot{Env: "e", HighWater: 1}, streamFrame: []string{
		busFrame(2, "deploy.state_changed", "cerberus", `{"resource":"tachyon","state":"succeeded","version":"v1"}`),
		busFrame(2, "deploy.state_changed", "cerberus", `{"resource":"tachyon","state":"succeeded","version":"v1"}`), // replay
		busFrame(3, "alert.raised", "cerberus", `{"name":"nanite health"}`),
		busFrame(4, "task.state_changed", "torque", `{"task_id":"CW-20261030-0002","state":"done"}`),
		busFrame(5, "deploy.state_changed", "cerberus", `{"resource":"nanite","state":"failed"}`),
	}}
	c := newCollector(t, src)
	runUntil(t, c, func() bool { return count(t, c, `SELECT COUNT(*) FROM bus_events`) == 4 })
	if count(t, c, `SELECT COUNT(*) FROM agents_events`) != 0 || count(t, c, `SELECT COUNT(*) FROM agents_sessions`) != 0 {
		t.Fatal("an app event must not become a session")
	}
	get := func(q string) (int, map[string]any) {
		w := httptest.NewRecorder()
		c.BusHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, BusPath+q, nil))
		var m map[string]any
		_ = json.NewDecoder(w.Body).Decode(&m)
		return w.Code, m
	}
	code, m := get("")
	evs := m["events"].([]any)
	if code != 200 || len(evs) != 4 || evs[0].(map[string]any)["subject"] != "nanite deploy failed" || m["next_before"] != nil {
		t.Fatalf("%d %v", code, m)
	}
	first := evs[0].(map[string]any)
	for _, k := range []string{"id", "seq", "at", "type", "subject", "actor", "app", "severity", "refs", "card"} {
		if _, ok := first[k]; !ok {
			t.Errorf("event lacks %s", k)
		}
	}
	_, m = get("?limit=2")
	if len(m["events"].([]any)) != 2 || m["next_before"] != 4.0 {
		t.Fatalf("page 1: %v", m)
	}
	_, m = get("?limit=2&before_seq=4")
	if len(m["events"].([]any)) != 2 || m["next_before"] != nil {
		t.Fatalf("page 2: %v", m)
	}
	_, m = get("?type=deploys")
	if len(m["events"].([]any)) != 2 {
		t.Fatalf("deploys: %v", m)
	}
	_, m = get("?type=failures&app=cerberus&q=NANITE")
	if len(m["events"].([]any)) != 1 {
		t.Fatalf("failures: %v", m)
	}
	_, m = get("?q=%25")
	if len(m["events"].([]any)) != 0 {
		t.Fatal("a LIKE wildcard must match literally")
	}
	for _, q := range []string{"?type=bogus", "?limit=0", "?limit=101", "?before_seq=x", "?app=a%3Bb"} {
		if code, _ := get(q); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
	s, _ := c.Status(context.Background())
	if s.BusTopics["deploy"] != "live" || s.BusTopics["failure"] != "live" || s.BusTopics["task"] != "live" || s.BusTopics["restart"] != "off" || s.BusTopics["host"] != "off" || s.BusTopics["workstream"] != "off" {
		t.Fatalf("topics: %v", s.BusTopics)
	}
}

func TestBusRetention(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	old := time.Now().Add(-8 * 24 * time.Hour).UnixNano()
	if e := c.Store.DB.Writer.Submit(context.Background(), "seed", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject) VALUES(1,'a',?,'deploy','old'),(2,'b',?,'deploy','new')`, old, time.Now().UnixNano())
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Store.Prune(context.Background()); e != nil {
		t.Fatal(e)
	}
	if count(t, c, `SELECT COUNT(*) FROM bus_events`) != 1 {
		t.Fatal("the 8-day-old event survived")
	}
}

func TestTaskEventNamesItsTaskFromEntityOrIsDropped(t *testing.T) {
	f, _ := ParseFrame([]byte(`{"kind":"task.state_changed","id":"e:9","app":"torque","payload":{"entity":{"type":"task","id":"CW-20261031-0013"},"from":"doing","to":"done"}}`))
	b, ok := NormalizeBus(f)
	if !ok || b.Subject != "CW-20261031-0013 done" || len(b.Refs) != 1 {
		t.Fatalf("%v %+v", ok, b)
	}
	f, _ = ParseFrame([]byte(`{"kind":"task.state_changed","id":"e:10","app":"torque","payload":{"to":"done"}}`))
	if _, ok := NormalizeBus(f); ok {
		t.Fatal("a task event naming no task is not published")
	}
}

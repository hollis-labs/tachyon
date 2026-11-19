package agentscollector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Tether event vocabulary (CW-20261114-0019): a top-level one-line summary on every event, and the
// acting seat's cell as scope.cell on the live stream or cell on GET /events (whose scope is a string).
func TestParseFrameReadsTetherSummaryAndCell(t *testing.T) {
	live, ok := ParseFrame([]byte(`{"id":"env:7","kind":"message.read","time":"2026-11-14T09:00:00Z","scope":{"cell":"photon","project":"agent-os"},"summary":"@photon-tether read request from @archie","payload":{}}`))
	if !ok || live.TetherSummary != "@photon-tether read request from @archie" || live.Cell != "photon" {
		t.Fatalf("live shape: %+v", live)
	}
	hist, ok := ParseFrame([]byte(`{"seq":8,"kind":"session.compacted","at":"2026-11-14T09:00:00Z","scope":"session","cell":"photon","session_id":"s1","summary":"context compacted (auto), 367003 tokens before","payload_json":"{}"}`))
	if !ok || hist.TetherSummary != "context compacted (auto), 367003 tokens before" || hist.Cell != "photon" {
		t.Fatalf("history shape: %+v", hist)
	}
	old, ok := ParseFrame([]byte(`{"seq":9,"kind":"message.read","at":"2026-11-14T09:00:00Z","scope":"daemon","payload_json":"{}"}`))
	if !ok || old.TetherSummary != "" || old.Cell != "" {
		t.Fatalf("an event from before the vocabulary has neither: %+v", old)
	}
	bad, _ := ParseFrame([]byte(`{"seq":10,"kind":"message.read","cell":"Photon; drop","summary":"x"}`))
	if bad.Cell != "" {
		t.Fatalf("a cell that is not a slug is dropped: %q", bad.Cell)
	}
}

func TestEventSummaryIsOneRedactedBoundedLine(t *testing.T) {
	if got := EventSummary("  sent\nrequest\t to  @archie \r\n"); got != "sent request to @archie" {
		t.Fatalf("one line: %q", got)
	}
	if got := EventSummary("token " + secret + " leaked"); strings.Contains(got, secret) {
		t.Fatalf("a credential survived: %q", got)
	}
	if got := EventSummary(strings.Repeat("é", 500)); len([]rune(got)) != SummaryMax {
		t.Fatalf("bounded to %d characters: %d", SummaryMax, len([]rune(got)))
	}
}

func TestTetherSummaryIsStoredOnSessionAndBusEventsAndServed(t *testing.T) {
	src := &fakeSource{snap: Snapshot{Env: "e", HighWater: 1}, streamFrame: []string{
		`{"id":"e:2","kind":"session.compacted","session_id":"s1","time":"2026-11-14T09:00:00Z","scope":{"cell":"photon"},"summary":"context compacted (auto), 367003 tokens before","payload":{}}`,
		`{"id":"e:3","kind":"tool.call.finished","session_id":"s1","time":"2026-11-14T09:00:01Z","summary":"called Bash with ` + secret + `","payload":{"tool":"Bash"}}`,
		`{"id":"e:4","kind":"deploy.state_changed","app":"cerberus","time":"2026-11-14T09:00:02Z","scope":{"cell":"ops"},"summary":"tachyon deployed v2 on agent-os","payload":{"resource":"tachyon","state":"succeeded"}}`,
	}}
	c := newCollector(t, src)
	runUntil(t, c, func() bool {
		return count(t, c, `SELECT COUNT(*) FROM agents_events`) == 2 && count(t, c, `SELECT COUNT(*) FROM bus_events`) == 1
	})
	if n := count(t, c, `SELECT COUNT(*) FROM agents_events WHERE tether_summary='context compacted (auto), 367003 tokens before' AND cell='photon' AND summary='session.compacted'`); n != 1 {
		t.Fatal("the session event keeps Tachyon's derived summary and stores Tether's beside it")
	}
	if n := count(t, c, `SELECT COUNT(*) FROM agents_events WHERE tether_summary LIKE '%`+secret+`%'`); n != 0 {
		t.Fatal("a credential in a wire summary reached the store")
	}
	w := httptest.NewRecorder()
	c.BusHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, BusPath+"?q=agent-os", nil))
	var m struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil || len(m.Events) != 1 {
		t.Fatalf("search matches the Tether summary: %v %s", err, w.Body)
	}
	if e := m.Events[0]; e["summary"] != "tachyon deployed v2 on agent-os" || e["cell"] != "ops" || e["subject"] == e["summary"] {
		t.Fatalf("the feed serves Tether's summary and cell beside its own subject: %v", e)
	}
}

// Mail and seat events carry no session, so before 055 they were dropped. Now message.sent and seat.* are feed
// rows whose line is Tether's summary, the only safe text they have; without one they are not published.
func TestMailAndSeatEventsBecomeFeedRowsFromTetherSummary(t *testing.T) {
	sent := normalize(t, `{"id":"e:1","kind":"message.sent","time":"2026-11-14T09:00:00Z","scope":{"cell":"photon"},"summary":"@archie sent request to @photon-tachyon","payload":{"from":"msg://agent/agent-mux/agt_lc7m97f5fp","to":["msg://agent/agent-mux/agt_4jfp86a7kp"],"body_ref":"x"}}`)
	if sent.Type != "message" || sent.Subject != "@archie sent request to @photon-tachyon" || sent.Summary != sent.Subject || sent.Cell != "photon" || sent.Severity != "info" || sent.Card != nil {
		t.Fatalf("mail row: %+v", sent)
	}
	waiting := normalize(t, `{"id":"e:2","kind":"seat.waiting","time":"2026-11-14T09:00:00Z","summary":"@photon-tachyon is waiting on @chrispian","payload":{"waiting_for":"@chrispian","request_id":"r1"}}`)
	if waiting.Type != "seat" || waiting.Severity != "warn" || waiting.Subject != "@photon-tachyon is waiting on @chrispian" {
		t.Fatalf("seat row: %+v", waiting)
	}
	for _, raw := range []string{
		`{"id":"e:3","kind":"message.sent","time":"2026-11-14T09:00:00Z","payload":{}}`,                           // from before summaries
		`{"id":"e:4","kind":"message.read","time":"2026-11-14T09:00:00Z","summary":"@b read request from @a"}`,    // folds under (b)
		`{"id":"e:5","kind":"message.sent","app":"forge","time":"2026-11-14T09:00:00Z","summary":"forge sent x"}`, // only Tether's own mail
	} {
		f, ok := ParseFrame([]byte(raw))
		if !ok {
			t.Fatal(raw)
		}
		if b, ok := NormalizeBus(f); ok {
			t.Errorf("not a feed row: %s -> %+v", raw, b)
		}
	}
}

package alertsource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

type memSink struct {
	mu         sync.Mutex
	events     []Event
	suppressed int
}

func (s *memSink) Ingest(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}
func (s *memSink) Suppressed(_ string, n int) { s.mu.Lock(); s.suppressed += n; s.mu.Unlock() }

type memCursor map[string]string

func (m memCursor) Cursor(s string) (string, error) { return m[s], nil }
func (m memCursor) SetCursor(s, c string) error     { m[s] = c; return nil }

func TestDedupeKey(t *testing.T) {
	if k := DedupeKey(Event{Source: "activity", SourceEventID: "e1"}); k != "activity|e1" {
		t.Errorf("stable id: %s", k)
	}
	a := Event{Source: "torque", Kind: "task.state_changed", Subjects: []alertsapi.Subject{{Kind: "task", Ref: "CW-1"}}, Payload: map[string]any{"revision": 4.0}, AtMS: 1000}
	b := a
	b.AtMS = 999999999 // a replay at another time with the same revision is the same event
	if DedupeKey(a) != DedupeKey(b) {
		t.Error("same revision must dedupe")
	}
	b.Payload = map[string]any{"revision": 5.0}
	if DedupeKey(a) == DedupeKey(b) {
		t.Error("new revision must not dedupe")
	}
	c, d := Event{Source: "torque", Kind: "x", AtMS: 60000}, Event{Source: "torque", Kind: "x", AtMS: 60000 + 59999}
	if DedupeKey(c) != DedupeKey(d) {
		t.Error("same-minute fallback must dedupe")
	}
}

func TestParseActivityFrame(t *testing.T) {
	e, err := ParseActivityFrame([]byte(`{"id":"a1","ts":1793332800000,"kind":"approval.requested","source":{"id":"hitl"},"severity":"warning",
	 "subjects":[{"type":"approval","id":"apr_1","label":"Restart\u0007 it","url":"javascript:alert(1)"},{"type":"","id":"x"}],
	 "payload":{"title":"T\nline","n":3,"list":[1],"obj":{"k":"v","deep":{"z":1}}}}`), "sse9")
	if err != nil {
		t.Fatal(err)
	}
	if e.SourceEventID != "a1" || e.Kind != "approval.requested" || e.SeverityHint != "warning" || len(e.Subjects) != 1 {
		t.Fatalf("%+v", e)
	}
	if e.Subjects[0].Label != "Restart it" || e.Subjects[0].URL != "" {
		t.Errorf("subject not cleaned: %+v", e.Subjects[0])
	}
	if e.Payload["title"] != "T line" || e.Payload["list"] != nil || e.Payload["obj"].(map[string]any)["deep"] != nil {
		t.Errorf("payload: %v", e.Payload)
	}
	if e2, _ := ParseActivityFrame([]byte(`{"kind":"k"}`), "sse9"); e2.SourceEventID != "sse9" {
		t.Error("SSE id is the fallback id")
	}
	if _, err := ParseActivityFrame([]byte(`{"ts":1}`), ""); err == nil {
		t.Error("a frame without kind must fail")
	}
	if e3, _ := ParseActivityFrame([]byte(`{"kind":"k","severity":"loud"}`), "i"); e3.SeverityHint != "" {
		t.Error("unknown severity must be dropped")
	}
	r := e.RuleEvent()
	if r.Subjects[0].Type != "approval" || r.Subjects[0].ID != "apr_1" || r.Severity != "warning" {
		t.Errorf("rule event: %+v", r)
	}
}

func TestReadSSE(t *testing.T) {
	var got []Frame
	err := ReadSSE(strings.NewReader(": hi\n\nid: 4\ndata: {\"a\":1}\n\ndata: x\ndata: y\n\nid: 5\n\n"), func(f Frame) error { got = append(got, f); return nil })
	if err != nil || len(got) != 2 || got[0].ID != "4" || got[1].Data != "x\ny" {
		t.Errorf("%v %+v", err, got)
	}
}

func TestActivityResumesFromCursorAndSavesIt(t *testing.T) {
	var lastID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastID = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("id: 8\ndata: {\"kind\":\"pr.merged\",\"severity\":\"info\"}\n\nid: 9\ndata: not json\n\nid: 10\ndata: {\"kind\":\"agent.state\"}\n\n"))
	}))
	defer srv.Close()
	sink, cur, h := &memSink{}, memCursor{"activity": "7"}, NewHealth()
	a := &Activity{URL: srv.URL, Sink: sink, Cursor: cur, Health: h}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lastID != "7" || len(sink.events) != 2 || sink.events[0].SourceEventID != "8" || cur["activity"] != "10" {
		t.Errorf("resume %q events %d cursor %q", lastID, len(sink.events), cur["activity"])
	}
	if s := h.Snapshot(); len(s) != 1 || s[0].State != "ok" || s[0].LastEventMS == 0 {
		t.Errorf("health %+v", s)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }))
	defer bad.Close()
	if err := (&Activity{URL: bad.URL, Sink: sink, Cursor: cur}).Run(context.Background()); err == nil {
		t.Error("HTTP 502 must fail the run")
	}
}

func TestLimiterDropsAndCounts(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(2)
	l.now = func() time.Time { return now }
	if !l.Allow("a") || !l.Allow("a") || l.Allow("a") || !l.Allow("b") {
		t.Fatal("burst of 2 per source")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("a") {
		t.Error("window must slide")
	}
	sink, cur := &memSink{}, memCursor{}
	lim := NewLimiter(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("id: 1\ndata: {\"kind\":\"a\"}\n\nid: 2\ndata: {\"kind\":\"b\"}\n\n"))
	}))
	defer srv.Close()
	if err := (&Activity{URL: srv.URL, Sink: sink, Cursor: cur, Limit: lim}).Run(context.Background()); err != nil || len(sink.events) != 1 || sink.suppressed != 1 || cur["activity"] != "2" {
		t.Errorf("%v events %d suppressed %d cursor %s", err, len(sink.events), sink.suppressed, cur["activity"])
	}
}

func TestSuperviseRestartsAfterErrorAndPanic(t *testing.T) {
	h := NewHealth()
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	done := make(chan struct{})
	go func() {
		Supervise(ctx, "x", h, time.Millisecond, func(context.Context) error {
			n++
			switch n {
			case 1:
				panic("boom")
			case 2:
				return context.DeadlineExceeded
			}
			cancel()
			return nil
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not restart")
	}
	if n != 3 {
		t.Errorf("runs: %d", n)
	}
	if s := h.Snapshot(); len(s) != 1 || s[0].State != "down" || strings.Contains(s[0].Note, "boom") {
		t.Errorf("health %+v (a panic value must not leak)", s)
	}
}

type fakeBus []agentscollector.BusEvent

func (f fakeBus) Events(_ context.Context, after int64) ([]agentscollector.BusEvent, error) {
	var out []agentscollector.BusEvent
	for _, e := range f {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestBusPollResumesAndNormalizes(t *testing.T) {
	src := fakeBus{
		{Seq: 3, At: time.UnixMilli(1000), Type: "deploy", Severity: "warning", Subject: "station", Refs: []agentscollector.BusRef{{Kind: "task", Ref: "CW-1", Label: "T"}}, Card: &agentscollector.BusCard{Title: "Deploy", Summary: "s"}},
		{Seq: 4, At: time.UnixMilli(2000), Type: "host"},
	}
	sink, cur := &memSink{}, memCursor{"agents": "3"}
	b := &Bus{Src: src, Sink: sink, Cursor: cur}
	if err := b.Poll(context.Background()); err != nil || len(sink.events) != 1 || sink.events[0].Kind != "bus.host" || cur["agents"] != "4" {
		t.Errorf("%v %+v %v", err, sink.events, cur)
	}
	e := FromBus(src[0])
	if e.SourceEventID != "3" || e.Kind != "bus.deploy" || e.Subjects[0].Ref != "CW-1" || e.Payload["title"] != "Deploy" {
		t.Errorf("%+v", e)
	}
}

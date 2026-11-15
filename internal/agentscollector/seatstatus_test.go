package agentscollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

func applyRaw(t *testing.T, c *Collector, raws ...string) {
	t.Helper()
	var fs []Frame
	for _, raw := range raws {
		f, ok := ParseFrame([]byte(raw))
		if !ok {
			t.Fatalf("frame did not parse: %s", raw)
		}
		fs = append(fs, f)
	}
	if err := c.Store.Apply(context.Background(), fs); err != nil {
		t.Fatal(err)
	}
}

// Each seat keeps its newest declared status (Tether seat.status); a replayed older event never overwrites it, an
// expired status is not listed, and only fields that pass validation are kept.
func TestSeatStatusKeepsTheNewestValidStatusPerSeat(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	applyRaw(t, c,
		`{"id":"e:10","kind":"seat.status","time":"`+at(-time.Hour)+`","payload":{"agent_urn":"msg://agent/agent-mux/agt_a","handle":"@builder-1","state":"working","set_by":"msg://agent/agent-mux/agt_a"}}`,
		`{"id":"e:12","kind":"seat.status","time":"`+at(-time.Minute)+`","payload":{"agent_urn":"msg://agent/agent-mux/agt_a","handle":"@builder-1","state":"blocked","owner":"@chrispian","reason":"needs the deploy token `+secret+`\nnow","ref":"task:CW-20261114-0099","expires_at":"`+at(time.Hour)+`"}}`,
		`{"id":"e:11","kind":"seat.status","time":"`+at(-30*time.Minute)+`","payload":{"agent_urn":"msg://agent/agent-mux/agt_a","state":"available"}}`, // older: ignored
		`{"id":"e:13","kind":"seat.status","time":"`+at(-time.Minute)+`","payload":{"agent_urn":"msg://agent/agent-mux/agt_b","handle":"@b","state":"waiting-decision","owner":"chrispian","ref":"javascript:alert(1)","expires_at":"`+at(-time.Second)+`"}}`,
		`{"id":"e:14","kind":"seat.status","time":"`+at(0)+`","payload":{"agent_urn":"msg://agent/agent-mux/agt_c","state":"asleep"}}`, // unknown state: dropped
		`{"id":"e:15","kind":"seat.status","time":"`+at(0)+`","payload":{"agent_urn":"msg://agent/agent-mux/agt_d","handle":"@d","state":"waiting-decision","owner":"chrispian; drop","ref":"javascript:alert(1)"}}`,
	)
	got, err := c.Store.SeatStatuses(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SeatStatus{}
	for _, s := range got {
		by[s.AgentURN] = s
	}
	if len(got) != 2 {
		t.Fatalf("an expired status (agt_b) and an unknown state (agt_c) are not listed: %+v", got)
	}
	a := by["msg://agent/agent-mux/agt_a"]
	if a.State != "blocked" || a.Owner != "@chrispian" || a.Ref != "task:CW-20261114-0099" || a.Handle != "@builder-1" || a.ExpiresAt == nil {
		t.Fatalf("newest status: %+v", a)
	}
	if strings.Contains(a.Reason, secret) || strings.Contains(a.Reason, "\n") || !strings.HasPrefix(a.Reason, "needs the deploy token") {
		t.Fatalf("reason is one redacted line: %q", a.Reason)
	}
	if d := by["msg://agent/agent-mux/agt_d"]; d.Owner != "" || d.Ref != "" || d.State != "waiting-decision" {
		t.Fatalf("an invalid owner and ref are dropped, the status kept: %+v", d)
	}
}

// The backfill fills a seat with no row and refreshes a backfilled row with a newer declaration, but never
// replaces a row the live stream wrote.
func TestSeatStatusBackfillNeverOverridesTheLiveStream(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	now := time.Now().UTC()
	const a, b = "msg://agent/agent-mux/agt_a", "msg://agent/agent-mux/agt_b"
	applyRaw(t, c, `{"id":"e:50","kind":"seat.status","time":"`+now.Add(-time.Hour).Format(time.RFC3339Nano)+`","payload":{"agent_urn":"`+b+`","state":"blocked"}}`)
	ctx := context.Background()
	for _, st := range []*SeatStatus{
		{AgentURN: a, Handle: "@a", State: "available", At: now.Add(-2 * time.Hour)},
		{AgentURN: a, Handle: "@a", State: "working", At: now.Add(-time.Minute)},  // newer: refreshes the backfilled row
		{AgentURN: a, Handle: "@a", State: "paused", At: now.Add(-3 * time.Hour)}, // older: ignored
		{AgentURN: b, Handle: "@b", State: "available", At: now},                  // the live row stays
	} {
		if err := c.Store.BackfillSeatStatus(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := c.Store.SeatStatuses(ctx, now)
	by := map[string]string{}
	for _, s := range got {
		by[s.AgentURN] = s.State
	}
	if by[a] != "working" || by[b] != "blocked" || len(got) != 2 {
		t.Fatalf("%v", by)
	}
	// A live event after a backfill replaces it.
	applyRaw(t, c, `{"id":"e:51","kind":"seat.status","time":"`+now.Format(time.RFC3339Nano)+`","payload":{"agent_urn":"`+a+`","state":"waiting-agent"}}`)
	got, _ = c.Store.SeatStatuses(ctx, now)
	for _, s := range got {
		if s.AgentURN == a && s.State != "waiting-agent" {
			t.Fatalf("live after backfill: %+v", s)
		}
	}
}

// A card read is one keyed GET /status/{@handle}, validated like a live seat.status; an expired or undeclared card
// gives nothing.
func TestSeatCardReadsOneStatus(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/status/@gui":
			fmt.Fprint(w, `{"handle":"@gui","agent_urn":"msg://agent/agent-mux/agt_gui","declared":{"agent_urn":"msg://agent/agent-mux/agt_gui","state":"waiting-agent","reason":"Re-review of #78\nnow","ref":"task:CW-20261114-0109","owner":"@gui-review","since":"2026-11-14T22:18:49Z","set_by":"msg://agent/agent-mux/agt_gui","updated_at":"2026-11-14T22:18:49Z"},"effective":"waiting-agent","live":{"online":true}}`)
		case "/status/@old":
			fmt.Fprint(w, `{"handle":"@old","agent_urn":"msg://agent/agent-mux/agt_old","expired":true,"declared":{"agent_urn":"msg://agent/agent-mux/agt_old","state":"working","since":"2026-11-01T00:00:00Z"},"effective":"unknown"}`)
		case "/status/@none":
			fmt.Fprint(w, `{"handle":"@none","agent_urn":"msg://agent/agent-mux/agt_none","effective":"undeclared"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	src, err := NewHTTPSource(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	cards := src.(SeatCards)
	st, err := cards.SeatCard(context.Background(), "@gui")
	if err != nil || st == nil || st.AgentURN != "msg://agent/agent-mux/agt_gui" || st.State != "waiting-agent" || st.Owner != "@gui-review" || st.Ref != "task:CW-20261114-0109" ||
		st.Reason != "Re-review of #78 now" || st.Seq != 0 || st.At.IsZero() {
		t.Fatalf("%+v %v", st, err)
	}
	for _, h := range []string{"@old", "@none"} {
		if st, err := cards.SeatCard(context.Background(), h); err != nil || st != nil {
			t.Fatalf("%s: %+v %v", h, st, err)
		}
	}
	if _, err := cards.SeatCard(context.Background(), "@missing"); err == nil {
		t.Fatal("a missing seat is an error, skipped by the backfill")
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/status/@") {
			t.Fatalf("only status cards are read: %v", paths)
		}
	}
}

type cardSource struct {
	*fakeSource
	mu    sync.Mutex
	asked []string
	cards map[string]*SeatStatus
}

func (s *cardSource) SeatCard(_ context.Context, h string) (*SeatStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, h)
	if st, ok := s.cards[h]; ok {
		return st, nil
	}
	return nil, errors.New("tether answered 404")
}

// On start the collector reads each synced catalog seat's card once, in the background, and stores what it finds.
func TestCollectorBackfillsSeatStatusesFromCardsOnStart(t *testing.T) {
	now := time.Now().UTC()
	src := &cardSource{fakeSource: &fakeSource{snap: Snapshot{Env: "e", HighWater: 1}},
		cards: map[string]*SeatStatus{"@gui": {AgentURN: "msg://agent/agent-mux/agt_gui", Handle: "@gui", State: "waiting-agent", At: now.Add(-time.Minute)}}}
	c := newCollector(t, src.fakeSource)
	c.Source = src
	for _, id := range []string{"gui", "lead", "bad id"} {
		raw, _ := json.Marshal(objects.Agent{Common: objects.Common{Name: id}})
		if _, err := objects.New(c.Store.DB).UpdateOrCreate(context.Background(), objects.Observation{Key: objects.SourceKey{SourceScope: objects.SourceScope{Provider: "tether",
			Instance: "https://tether.test", Collection: "catalog/agents", Kind: objects.AgentKind}, ExternalID: id}, ObservedAt: now, Definition: raw}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	runUntil(t, c, func() bool {
		got, _ := c.Store.SeatStatuses(context.Background(), now)
		return len(got) == 1 && got[0].State == "waiting-agent"
	})
	src.mu.Lock()
	defer src.mu.Unlock()
	if strings.Join(src.asked, ",") != "@gui,@lead" {
		t.Fatalf("each valid catalog handle is read once, in order: %v", src.asked)
	}
}

package agentscollector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
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

func seatFrame(t *testing.T, seq int, at time.Time, urn, state string) Frame {
	t.Helper()
	raw := fmt.Sprintf(`{"seq":%d,"kind":"seat.status","at":%q,"payload_json":"{\"agent_urn\":\"%s\",\"handle\":\"@h\",\"state\":\"%s\"}"}`, seq, at.Format(time.RFC3339Nano), urn, state)
	f, ok := ParseFrame([]byte(raw))
	if !ok || f.Seat == nil {
		t.Fatalf("seat frame did not parse: %s", raw)
	}
	return f
}

// The backfill keeps each seat's newest status whatever order Tether lists them in, and never replaces a newer
// row the live stream already stored.
func TestSeatStatusBackfillKeepsTheNewest(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	now := time.Now().UTC()
	const a, b = "msg://agent/agent-mux/agt_a", "msg://agent/agent-mux/agt_b"
	applyRaw(t, c, `{"id":"e:50","kind":"seat.status","time":"`+now.Format(time.RFC3339Nano)+`","payload":{"agent_urn":"`+b+`","state":"blocked"}}`)
	n, err := c.Store.BackfillSeatStatuses(context.Background(), []Frame{
		seatFrame(t, 40, now.Add(-time.Minute), a, "working"), // newest first, as Tether lists them
		seatFrame(t, 30, now.Add(-time.Hour), a, "available"),
		seatFrame(t, 20, now.Add(-time.Hour), b, "working"), // older than the live row: ignored
	})
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	got, _ := c.Store.SeatStatuses(context.Background(), now)
	by := map[string]string{}
	for _, s := range got {
		by[s.AgentURN] = s.State
	}
	if by[a] != "working" || by[b] != "blocked" || len(got) != 2 {
		t.Fatalf("%v", by)
	}
}

type seatHistorySource struct {
	*fakeSource
	mu     sync.Mutex
	calls  int
	frames []Frame
}

func (s *seatHistorySource) SeatStatusHistory(context.Context, int) ([]Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return nil, errors.New("tether answered 503")
	}
	return s.frames, nil
}

// On start the collector backfills seat statuses from Tether's history, in the background, retrying a failure.
func TestCollectorBackfillsSeatStatusesOnStart(t *testing.T) {
	old := seatBackfillRetry
	seatBackfillRetry = 10 * time.Millisecond
	defer func() { seatBackfillRetry = old }()
	now := time.Now().UTC()
	src := &seatHistorySource{fakeSource: &fakeSource{snap: Snapshot{Env: "e", HighWater: 1}},
		frames: []Frame{seatFrame(t, 9, now.Add(-time.Minute), "msg://agent/agent-mux/agt_gui", "waiting-agent")}}
	c := newCollector(t, src.fakeSource)
	c.Source = src
	runUntil(t, c, func() bool {
		got, _ := c.Store.SeatStatuses(context.Background(), now)
		return len(got) == 1 && got[0].State == "waiting-agent"
	})
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.calls != 2 {
		t.Fatalf("one failed attempt, then one that stored: %d calls", src.calls)
	}
}

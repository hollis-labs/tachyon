package agentscollector

import (
	"context"
	"strings"
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

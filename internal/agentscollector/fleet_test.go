package agentscollector

import (
	"context"
	"testing"
	"time"
)

type fakeFleet struct {
	workers []FleetWorker
	streams []FleetWorkstream
}

func (f *fakeFleet) Fleet(context.Context) ([]FleetWorker, []FleetWorkstream, error) {
	return f.workers, f.streams, nil
}

func TestFleetEmitterHostAndWorkstreamEvents(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	src := &fakeFleet{
		workers: []FleetWorker{{ID: "w1", Label: "agent-os", Connected: true}},
		streams: []FleetWorkstream{
			{ID: "ws-new", Name: "Nil v2 — capture & data", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
			{ID: "ws-old", Name: "ancient", CreatedAt: now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)},
		},
	}
	e := &FleetEmitter{Source: src, Store: c.Store, Now: func() time.Time { return now }}
	ctx := context.Background()
	if err := e.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	ev, _, err := c.Store.BusEvents(ctx, BusQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 || ev[0]["type"] != "workstream" || ev[0]["subject"] != "workstream Nil v2 capture data created" {
		t.Fatalf("seed pass: want one recent workstream and no host event, got %v", ev)
	}
	// A re-poll and a restart (new emitter, same data) add nothing.
	_ = e.Poll(ctx)
	e2 := &FleetEmitter{Source: src, Store: c.Store, Now: func() time.Time { return now }}
	_ = e2.Poll(ctx)
	if ev, _, _ = c.Store.BusEvents(ctx, BusQuery{Limit: 50}); len(ev) != 1 {
		t.Fatalf("replay must not duplicate: %v", ev)
	}
	// The worker drops, then a new workstream appears.
	src.workers[0].Connected = false
	src.streams = append(src.streams, FleetWorkstream{ID: "ws-2", Name: "second", CreatedAt: now.Format(time.RFC3339)})
	now = now.Add(time.Minute)
	if err := e.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	ev, _, _ = c.Store.BusEvents(ctx, BusQuery{Limit: 50})
	got := map[string]string{}
	for _, x := range ev {
		got[x["type"].(string)+"/"+x["subject"].(string)] = x["severity"].(string)
	}
	if got["host/agent-os offline"] != "warn" || got["workstream/workstream second created"] != "info" || len(ev) != 3 {
		t.Fatalf("after change: %v", ev)
	}
}

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestPollingFilterExecution(t *testing.T) {
	local := NewLocalAdapter(100)
	local.RecordActivity(ActivityEntry{Source: "fixture", Kind: "test", Summary: "match"})
	local.RecordActivity(ActivityEntry{Source: "other", Kind: "test"})
	local.RecordEvent(Event{Source: "fixture", Kind: "test"})
	local.RecordEvent(Event{Source: "other", Kind: "test"})
	local.RecordLog(LogEntry{Source: "fixture", Level: "info", Message: "match", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	local.RecordLog(LogEntry{Source: "fixture", Level: "error", Message: "other"})
	p := &plugin{adapter: local}
	for _, tc := range []struct{ channel, filter string }{
		{"activity", `{"source":"fixture","kind":"test","limit":1}`},
		{"events", `{"source":"fixture","kind":"test","limit":1}`},
		{"logs", `{"source":"fixture","level":"info","search":"match","since":"2025-12-31T00:00:00Z","until":"2026-01-02T00:00:00Z","limit":1}`},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			env, err := p.handleVerb(context.Background(), "observe_subscribe", mustMarshal(t, SubscribeRequest{Channel: tc.channel, Filter: tc.filter}))
			if err != nil || env.Status != contract.StatusOK {
				t.Fatalf("subscribe: %+v %v", env, err)
			}
			var d SubscriptionHandle
			if err := json.Unmarshal(env.Data, &d); err != nil {
				t.Fatal(err)
			}
			env, err = p.handleVerb(context.Background(), "observe_"+tc.channel, d.Payload)
			var rows []map[string]any
			if err != nil || env.Status != contract.StatusOK || json.Unmarshal(env.Data, &rows) != nil || len(rows) != 1 || rows[0]["source"] != "fixture" {
				t.Fatalf("poll: %+v %v", env, err)
			}
		})
	}
}

func TestPollingInvalidAndUnsupported(t *testing.T) {
	for _, tc := range []struct{ channel, filter, code string }{
		{"", "", "validation"}, {"bogus", "", "validation"}, {"../status", "", "validation"},
		{"metrics", "", "unsupported"}, {"status", "", "unsupported"},
		{"activity", `{"since_id":"obs-1"}`, "unsupported"},
		{"events", `{"since_id":"obs-1"}`, "unsupported"},
		{"logs", `{"kind":"test"}`, "validation"},
		{"activity", `{"source kind":"test"}`, "validation"}, {"activity", `{"source":3}`, "validation"},
		{"activity", `{"limit":201}`, "validation"}, {"logs", `{"limit":0}`, "validation"},
		{"logs", `{"limit":1.5}`, "validation"}, {"logs", `{"since":"yesterday"}`, "validation"},
		{"events", `null`, "validation"}, {"activity", `[]`, "validation"}, {"logs", `{`, "validation"},
	} {
		env, err := newTestPlugin().handleVerb(context.Background(), "observe_subscribe", mustMarshal(t, SubscribeRequest{Channel: tc.channel, Filter: tc.filter}))
		if err != nil || env.Status != contract.StatusError || env.Error == nil || env.Error.Code != tc.code || len(env.Data) > 0 {
			t.Errorf("%+v: %+v %v", tc, env, err)
		}
	}
}

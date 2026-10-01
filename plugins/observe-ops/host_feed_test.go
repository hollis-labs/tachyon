package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

func TestPrivateHostIngestionRequiresMarkerAndSafeShape(t *testing.T) {
	p := &plugin{local: NewLocalAdapter(3), hostToken: "HOST-CREDENTIAL"}
	p.adapter = p.local
	batch := observefeed.Batch{Epoch: "epoch", Records: []observefeed.Record{{ID: "record-1", Sequence: 1, Timestamp: time.Now(), Kind: "operation_result", Plugin: "work-ops", Generation: "gen", Operation: "operation", Module: "work", Verb: "work_write", Effect: "writes", Status: "ask", DurationMS: 3}}, Counters: observefeed.Counters{Overflow: 7}}
	args := func(token string) string {
		raw, _ := json.Marshal(observefeed.Ingest{Token: token, Batch: batch})
		return string(raw)
	}
	for _, raw := range []string{args(""), args("forged"), strings.Replace(args(p.hostToken), `"status":"ask"`, `"status":"ask","payload":"INJECTED-SECRET"`, 1), args(p.hostToken) + `{}`, strings.Repeat("x", observefeed.MaxBytes+1)} {
		if _, err := p.Command(context.Background(), subprocess.CommandRequest{Name: observefeed.Command, Args: raw}); err == nil {
			t.Fatal("forged/invalid ingestion accepted")
		}
	}
	entries, _ := p.local.ListActivity(context.Background(), ActivityFilter{})
	if len(entries) != 0 {
		t.Fatal("forgery recorded")
	}
	ack, err := p.Command(context.Background(), subprocess.CommandRequest{Name: observefeed.Command, Args: args(p.hostToken)})
	if err != nil || ack.Content != `{"accepted":true}` {
		t.Fatal(ack, err)
	}
	for _, verb := range []string{"observe_activity", "observe_events", "observe_logs", "observe_metrics", "observe_status"} {
		env, err := p.HandleVerb(context.Background(), verb, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(env.Data), p.hostToken) || strings.Contains(string(env.Data), "INJECTED-SECRET") {
			t.Fatal("credential/forgery exposed", verb)
		}
	}
	status, _ := p.local.Status(context.Background())
	if status.HostFeed == nil || status.HostFeed.Counters.Overflow != 7 || status.HostFeed.LastSequence != 1 {
		t.Fatal(status)
	}
	entries, _ = p.local.ListActivity(context.Background(), ActivityFilter{})
	if len(entries) != 1 {
		t.Fatal("reads/delivery recursed", len(entries))
	}
	for n := 0; n < 5; n++ {
		if _, err := p.ingestHost(args(p.hostToken)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ = p.local.ListActivity(context.Background(), ActivityFilter{Limit: 100})
	events, _ := p.local.ListEvents(context.Background(), EventFilter{Limit: 100})
	metrics, _ := p.local.ListMetrics(context.Background(), MetricFilter{Limit: 100})
	if len(entries) != 3 || len(events) != 3 || len(metrics) != 3 {
		t.Fatal("local storage exceeded ring", len(entries), len(events), len(metrics))
	}
	if _, declared := p.Capabilities().Verbs[observefeed.Command]; declared {
		t.Fatal("private command advertised")
	}
}

func TestMissingHostMarkerCannotEnableIngestion(t *testing.T) {
	p := &plugin{local: NewLocalAdapter(3)}
	if _, err := p.ingestHost(`{"token":"","batch":{"epoch":"epoch","records":[]}}`); err == nil {
		t.Fatal("empty marker enabled ingestion")
	}
}

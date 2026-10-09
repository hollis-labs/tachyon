package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

// The marker is per-process, injected into Init by the host, and never stored
// in recorded entries. This command is absent from all public declarations.
func (p *plugin) ingestHost(args string) (subprocess.CommandResult, error) {
	if len(args) > observefeed.MaxBytes {
		return subprocess.CommandResult{}, fmt.Errorf("invalid host ingestion")
	}
	var in observefeed.Ingest
	dec := json.NewDecoder(strings.NewReader(args))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || p.hostToken == "" || subtle.ConstantTimeCompare([]byte(in.Token), []byte(p.hostToken)) != 1 || in.Batch.Validate() != nil || p.local == nil {
		return subprocess.CommandResult{}, fmt.Errorf("invalid host ingestion")
	}
	for _, r := range in.Batch.Records {
		kind := "lifecycle"
		if strings.HasPrefix(r.Kind, "operation_") {
			kind = "verb"
		}
		p.local.RecordActivity(ActivityEntry{ID: r.ID, Timestamp: r.Timestamp, Kind: kind, Source: r.Plugin, Actor: "tachyon", Summary: r.Kind + " " + r.Status, Detail: r})
		p.local.RecordEvent(Event{ID: r.ID, Timestamp: r.Timestamp, Kind: r.Kind, Source: r.Plugin, Payload: map[string]any{
			"sequence": r.Sequence, "generation": r.Generation, "operation_id": r.Operation, "module": r.Module, "verb": r.Verb, "method": r.Method, "status": r.Status, "effect": r.Effect, "duration_ms": r.DurationMS, "reason_class": r.Reason,
		}})
		if r.Kind == "operation_result" {
			p.local.RecordMetric(MetricPoint{Name: "latency_ms", Value: float64(r.DurationMS), Unit: "ms", Timestamp: r.Timestamp, Tags: map[string]string{"plugin": r.Plugin, "verb": r.Verb, "effect": r.Effect, "status": r.Status}})
		}
		if r.Status == "error" {
			p.local.RecordLog(LogEntry{ID: r.ID, Timestamp: r.Timestamp, Level: "error", Source: r.Plugin, Message: r.Kind + " failed", Fields: map[string]any{"operation_id": r.Operation, "reason_class": r.Reason}})
		}
	}
	p.local.mu.Lock()
	last := uint64(0)
	if p.local.hostFeed != nil && p.local.hostFeed.Epoch == in.Batch.Epoch {
		last = p.local.hostFeed.LastSequence
	}
	for _, r := range in.Batch.Records {
		last = max(last, r.Sequence)
	}
	p.local.hostFeed = &HostFeedStatus{Epoch: in.Batch.Epoch, Counters: in.Batch.Counters, LastSequence: last}
	p.local.mu.Unlock()
	return subprocess.CommandResult{Action: "message", Content: `{"accepted":true}`}, nil
}

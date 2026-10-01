package observefeed

import (
	"strings"
	"testing"
	"time"
)

func validRecord() Record {
	return Record{ID: "epoch-1", Sequence: 1, Timestamp: time.Now(), Kind: "operation_result", Plugin: "work-ops", Generation: "generation", Operation: "operation", Module: "work", Verb: "work_write", Effect: "writes", Status: "ask", Method: "command/execute", DurationMS: 5}
}

func TestBatchClassificationsAndBounds(t *testing.T) {
	for _, kind := range []string{"operation_start", "operation_result", "plugin_load", "plugin_restart", "plugin_unload", "plugin_failure", "plugin_retire"} {
		for _, status := range []string{"started", "ok", "ask", "error", "unknown"} {
			r := validRecord()
			r.Kind = kind
			r.Status = status
			if err := (Batch{Epoch: "epoch", Records: []Record{r}}).Validate(); err != nil {
				t.Fatal(kind, status, err)
			}
		}
	}
	for _, reason := range []string{"spawn", "settings", "handshake", "load", "discovery", "declaration", "validation", "collision", "timeout", "transport_error", "canceled", "invalid_response"} {
		r := validRecord()
		r.Reason = reason
		if err := (Batch{Epoch: "epoch", Records: []Record{r}}).Validate(); err != nil {
			t.Fatal(reason, err)
		}
	}
	for _, mutate := range []func(*Record){func(r *Record) { r.ID = "" }, func(r *Record) { r.Plugin = "" }, func(r *Record) { r.Generation = "credential?secret" }, func(r *Record) { r.Module = strings.Repeat("x", 129) }, func(r *Record) { r.Sequence = 0 }, func(r *Record) { r.Timestamp = time.Time{} }, func(r *Record) { r.DurationMS = -1 }, func(r *Record) { r.Kind = "arbitrary-event" }, func(r *Record) { r.Status = "approved" }, func(r *Record) { r.Effect = "unknown-effect" }, func(r *Record) { r.Reason = "UPSTREAM-SECRET" }, func(r *Record) { r.Method = "user/input" }} {
		r := validRecord()
		mutate(&r)
		if (Batch{Epoch: "epoch", Records: []Record{r}}).Validate() == nil {
			t.Fatal("unsafe record accepted", r)
		}
	}
	batch := Batch{Epoch: "epoch", Records: make([]Record, MaxBatch)}
	for n := range batch.Records {
		batch.Records[n] = validRecord()
	}
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	batch.Records = append(batch.Records, validRecord())
	if batch.Validate() == nil {
		t.Fatal("oversized batch accepted")
	}
	if (Batch{Epoch: "bad epoch"}).Validate() == nil {
		t.Fatal("unsafe epoch")
	}
}

func TestIdentityAllowlist(t *testing.T) {
	for _, id := range []string{"work-ops", "host:opaque/id", strings.Repeat("a", 128)} {
		if !Identifier(id) {
			t.Fatal("safe identity rejected", id)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 129), "value\nsecret", "credential?value", "🗝️"} {
		if Identifier(id) {
			t.Fatal("unsafe identity accepted", id)
		}
	}
}

package hitl

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func requestSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	raw, e := os.ReadFile("testdata/tangent-hitl-request.schema.json")
	if e != nil {
		t.Fatal(e)
	}
	var schema jsonschema.Schema
	if e = json.Unmarshal(raw, &schema); e != nil {
		t.Fatal(e)
	}
	resolved, e := schema.Resolve(nil)
	if e != nil {
		t.Fatal(e)
	}
	return resolved
}
func validateRequestBytes(schema *jsonschema.Resolved, raw []byte) error {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return e
	}
	return schema.Validate(v)
}
func TestExactBuiltRequestMatchesTangentSchema(t *testing.T) {
	schema := requestSchema(t)
	for _, kind := range []string{"approval", "attention"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeClient{}
			r, _ := NewRuntime(f, "")
			ask := contract.AskDetail{Prompt: strings.Repeat("界", 4000), Kind: kind, Correlations: map[string]any{"plugin_spoof": "ignored", "additional": make([]any, 16)}}
			if kind == "approval" {
				ask.Impact = &contract.HitlImpact{Approve: "Yes", Deny: "No"}
			}
			raw, _ := json.Marshal(contract.ResultEnvelope{Status: contract.StatusAsk, Ask: &ask})
			out := r.Attach(context.Background(), strings.Repeat("v", 200), []byte(`{"n":9007199254740993}`), "gen", func() bool { return true }, raw)
			if len(f.requests) != 1 {
				t.Fatal(string(out))
			}
			r.mu.Lock()
			var frozen []byte
			for _, p := range r.entries {
				frozen = append([]byte(nil), p.snapshot...)
			}
			r.mu.Unlock()
			if e := validateRequestBytes(schema, frozen); e != nil {
				t.Fatal(e)
			}
			if utf8.RuneCountInString(f.requests[0].Summary) != 600 || utf8.RuneCountInString(f.requests[0].Title) != 160 || len(f.requests[0].Correlations) != 1 || len(f.requests[0].Correlations["additional"].([]any)) != 5 {
				t.Fatal("request limits/binding")
			}
			wire, _ := json.Marshal(f.requests[0])
			if string(wire) != string(frozen) {
				t.Fatal("exact outgoing request differs from retained snapshot")
			}
		})
	}
	// Prove that the previous free-form binding is rejected by the authored schema.
	req := FromAskDetail("work_write", "tachyon:key", &contractAsk)
	req.Correlations = map[string]any{"host_epoch": "bad"}
	raw, _ := json.Marshal(req)
	if validateRequestBytes(schema, raw) == nil {
		t.Fatal("fixture failed to reject free-form correlations")
	}
}
func TestSchemaInvalidAskNeverEnqueues(t *testing.T) {
	schema := requestSchema(t)
	for _, ask := range []contract.AskDetail{{Prompt: strings.Repeat("界", 4001)}, {Prompt: "Look", Kind: "attention", Impact: &contract.HitlImpact{Approve: "yes", Deny: "no"}}, {Prompt: "P", Impact: &contract.HitlImpact{Approve: strings.Repeat("x", 2001), Deny: "no"}}} {
		f := &fakeClient{}
		r, _ := NewRuntime(f, "")
		raw, _ := json.Marshal(contract.ResultEnvelope{Status: contract.StatusAsk, Ask: &ask})
		out := r.Attach(context.Background(), "work_write", nil, "g", func() bool { return true }, raw)
		if len(f.requests) != 0 {
			wire, _ := json.Marshal(f.requests[0])
			t.Fatal("invalid ask enqueued", validateRequestBytes(schema, wire))
		}
		var e contract.ResultEnvelope
		_ = json.Unmarshal(out, &e)
		if e.Ask.Unavailable == "" || e.Ask.OperationID != "" {
			t.Fatal(string(out))
		}
	}
}
func TestUnconfiguredPreservesRawAskExactly(t *testing.T) {
	r, _ := NewRuntime(nil, "")
	for _, raw := range []string{` {"status":"ask","trace_id":"trace-secret","ask":{"title":"Title","prompt":"Proceed?","context":{"number":9007199254740993}}} `, `{"status":"ask","ask":{"prompt":"p","options":"bad"}}`} {
		out := r.Attach(context.Background(), "work_write", []byte("not json"), "gen", func() bool { t.Fatal("unconfigured identity lookup"); return false }, []byte(raw))
		if string(out) != raw {
			t.Fatal("unconfigured ask changed", string(out))
		}
	}
}
func TestEnqueueFailuresReleaseCapacity(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		f := &failureClient{invalid: invalid}
		r, _ := NewRuntime(f, "")
		for i := 0; i < PendingCapacity+10; i++ {
			a := ask(t, r, `{}`, func() bool { return true })
			if a.OperationID != "" || a.Expiry != "" || a.Unavailable == "capacity" {
				t.Fatal(a)
			}
		}
		if len(r.entries) != 0 {
			t.Fatal("failed entries retained")
		}
	}
}

type failureClient struct {
	fakeClient
	invalid bool
}

func (f *failureClient) Enqueue(context.Context, EnqueueRequest) (EnqueueHandle, error) {
	if f.invalid {
		return EnqueueHandle{}, nil
	}
	return EnqueueHandle{}, context.DeadlineExceeded
}

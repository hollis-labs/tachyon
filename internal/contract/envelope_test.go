package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnvelope(t *testing.T) {
	t.Run("OK with struct", func(t *testing.T) {
		type Dummy struct {
			Foo string `json:"foo"`
		}
		env, err := OK(Dummy{Foo: "bar"})
		if err != nil {
			t.Fatalf("OK() error = %v", err)
		}
		if env.Status != StatusOK {
			t.Errorf("expected status %q, got %q", StatusOK, env.Status)
		}
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"status":"ok","data":{"foo":"bar"}}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})

	t.Run("OK with nil", func(t *testing.T) {
		env, err := OK(nil)
		if err != nil {
			t.Fatalf("OK() error = %v", err)
		}
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"status":"ok"}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})

	t.Run("Err", func(t *testing.T) {
		env := Err("CODE1", "some error")
		if env.Status != StatusError {
			t.Errorf("expected status %q, got %q", StatusError, env.Status)
		}
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"status":"error","error":{"code":"CODE1","message":"some error"}}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})

	t.Run("Ask with options", func(t *testing.T) {
		env := Ask("proceed?", "yes", "no")
		if env.Status != StatusAsk {
			t.Errorf("expected status %q, got %q", StatusAsk, env.Status)
		}
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"status":"ask","ask":{"prompt":"proceed?","options":["yes","no"]}}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})

	t.Run("Ask with no options", func(t *testing.T) {
		env := Ask("proceed?")
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"status":"ask","ask":{"prompt":"proceed?"}}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})

	t.Run("JSON round-trip", func(t *testing.T) {
		original := Err("TEST_CODE", "test message")
		b, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		var decoded ResultEnvelope
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatalf("Unmarshal error = %v", err)
		}
		if !reflect.DeepEqual(original, decoded) {
			t.Errorf("round-trip failed: expected %+v, got %+v", original, decoded)
		}
	})

	t.Run("Status constants string values", func(t *testing.T) {
		if StatusOK != "ok" {
			t.Errorf("expected StatusOK = 'ok', got %q", StatusOK)
		}
		if StatusError != "error" {
			t.Errorf("expected StatusError = 'error', got %q", StatusError)
		}
		if StatusAsk != "ask" {
			t.Errorf("expected StatusAsk = 'ask', got %q", StatusAsk)
		}
	})
}

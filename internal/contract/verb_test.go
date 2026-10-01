package contract

import (
	"encoding/json"
	"testing"
)

func TestVerbInvokeParams(t *testing.T) {
	t.Run("MethodVerbInvoke constant", func(t *testing.T) {
		if MethodVerbInvoke != "verb/invoke" {
			t.Errorf("expected MethodVerbInvoke to be 'verb/invoke', got %q", MethodVerbInvoke)
		}
	})

	t.Run("Marshals correctly with verb and payload", func(t *testing.T) {
		params := VerbInvokeParams{
			Verb:    "mod1_action",
			Payload: json.RawMessage(`{"foo":"bar"}`),
		}
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"verb":"mod1_action","payload":{"foo":"bar"}}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})

	t.Run("Marshals correctly with verb and nil payload", func(t *testing.T) {
		params := VerbInvokeParams{
			Verb: "mod1_action",
		}
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		expected := `{"verb":"mod1_action"}`
		if string(b) != expected {
			t.Errorf("expected %s, got %s", expected, string(b))
		}
	})
}

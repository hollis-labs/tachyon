package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// TestCapabilitiesJSON validates the capabilities.json file against
// the contract.PluginCapabilities schema. This test does NOT depend on
// the //go:embed in main.go — it reads the file directly so it can run
// even when main.go doesn't compile (pluginkit dependency).
func TestCapabilitiesJSON(t *testing.T) {
	data, err := os.ReadFile("capabilities.json")
	if err != nil {
		t.Fatal(err)
	}

	var caps contract.PluginCapabilities
	if err := json.Unmarshal(data, &caps); err != nil {
		t.Fatalf("unmarshal capabilities.json: %v", err)
	}

	if err := caps.Validate(); err != nil {
		t.Fatalf("capabilities validation failed: %v", err)
	}

	// Module check.
	if len(caps.Modules) != 1 || caps.Modules[0] != "observe" {
		t.Errorf("modules = %v, want [observe]", caps.Modules)
	}

	// All verbs should be reads.
	expectedVerbs := []string{
		"observe_activity",
		"observe_logs",
		"observe_metrics",
		"observe_events",
		"observe_status",
		"observe_subscribe",
	}

	if len(caps.Verbs) != len(expectedVerbs) {
		t.Fatalf("got %d verbs, want %d", len(caps.Verbs), len(expectedVerbs))
	}

	for _, v := range expectedVerbs {
		decl, ok := caps.Verbs[v]
		if !ok {
			t.Errorf("missing verb %q", v)
			continue
		}
		if decl.Effect != contract.EffectReads {
			t.Errorf("verb %q has effect %q, want %q", v, decl.Effect, contract.EffectReads)
		}
	}
}

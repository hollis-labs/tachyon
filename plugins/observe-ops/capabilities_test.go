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

// TestCapabilitiesNavDeclaration validates the nav declaration in
// capabilities.json.
func TestCapabilitiesNavDeclaration(t *testing.T) {
	data, err := os.ReadFile("capabilities.json")
	if err != nil {
		t.Fatal(err)
	}

	var raw struct {
		Nav *contract.NavDeclaration `json:"nav"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal capabilities.json: %v", err)
	}
	if raw.Nav == nil {
		t.Fatal("capabilities.json missing nav declaration")
	}
	if len(raw.Nav.Groups) != 1 {
		t.Fatalf("got %d nav groups, want 1", len(raw.Nav.Groups))
	}
	g := raw.Nav.Groups[0]
	if g.ID != "observability" {
		t.Errorf("group id = %q, want observability", g.ID)
	}
	if g.Priority != 400 {
		t.Errorf("group priority = %d, want 400", g.Priority)
	}

	if len(raw.Nav.Items) != 3 {
		t.Fatalf("got %d nav items, want 3", len(raw.Nav.Items))
	}
	for _, item := range raw.Nav.Items {
		if item.Group != "observability" {
			t.Errorf("item %q group = %q, want observability", item.ID, item.Group)
		}
		if item.RequiresVerb == "" {
			t.Errorf("item %q missing requires_verb", item.ID)
		}
	}
}

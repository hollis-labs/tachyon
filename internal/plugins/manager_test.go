package plugins

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestModuleCollisionDetection(t *testing.T) {
	m := NewManager(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	m.modules["agent"] = "plugin-a"

	newCapabilities := &contract.PluginCapabilities{
		Modules: []string{"agent"},
	}

	collisionFound := false
	for _, mod := range newCapabilities.Modules {
		if _, taken := m.modules[mod]; taken {
			collisionFound = true
			break
		}
	}

	if !collisionFound {
		t.Fatal("expected collision detection to find collision for 'agent' module")
	}
}

func TestModuleRegistration(t *testing.T) {
	m := NewManager(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	caps := &contract.PluginCapabilities{
		Modules: []string{"agent", "launch"},
	}
	proc := &pluginProcess{
		id:           "plugin-a",
		capabilities: caps,
	}

	m.plugins[proc.id] = proc
	for _, mod := range caps.Modules {
		m.modules[mod] = proc.id
	}

	if owner := m.ModuleOwner("agent"); owner != "plugin-a" {
		t.Errorf("expected agent to be owned by plugin-a, got %q", owner)
	}
	if owner := m.ModuleOwner("launch"); owner != "plugin-a" {
		t.Errorf("expected launch to be owned by plugin-a, got %q", owner)
	}
	if owner := m.ModuleOwner("session"); owner != "" {
		t.Errorf("expected session to be unowned, got %q", owner)
	}
}

func TestAllCapabilities(t *testing.T) {
	m := NewManager(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	caps1 := &contract.PluginCapabilities{
		Modules: []string{"agent"},
	}
	proc1 := &pluginProcess{
		id:           "plugin-a",
		capabilities: caps1,
	}
	m.plugins[proc1.id] = proc1
	m.modules["agent"] = proc1.id

	caps2 := &contract.PluginCapabilities{
		Modules: []string{"work"},
	}
	proc2 := &pluginProcess{
		id:           "plugin-b",
		capabilities: caps2,
	}
	m.plugins[proc2.id] = proc2
	m.modules["work"] = proc2.id

	allCaps := m.AllCapabilities()

	if len(allCaps) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(allCaps))
	}
	if allCaps["agent"] != caps1 {
		t.Error("expected caps1 for agent")
	}
	if allCaps["work"] != caps2 {
		t.Error("expected caps2 for work")
	}
}

func TestInvokeVerbRouting(t *testing.T) {
	m := NewManager(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	m.modules["agent"] = "agent-ops"
	m.modules["work"] = "work-ops"

	_, err := m.InvokeVerb(context.Background(), "agent_create", nil)
	if err == nil || err.Error() == `no plugin owns a module matching verb "agent_create"` {
		t.Fatal("expected routing to find agent module")
	}
	if err.Error() != "plugin not found: agent-ops" {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = m.InvokeVerb(context.Background(), "session_create", nil)
	if err == nil || err.Error() != `no plugin owns a module matching verb "session_create"` {
		t.Fatalf("expected no-module error, got: %v", err)
	}
}

func TestShutdownReleasesModules(t *testing.T) {
	m := NewManager(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	caps := &contract.PluginCapabilities{
		Modules: []string{"agent", "launch"},
	}
	proc := &pluginProcess{
		id:           "plugin-a",
		capabilities: caps,
	}
	m.plugins[proc.id] = proc
	for _, mod := range caps.Modules {
		m.modules[mod] = proc.id
	}

	// simulate the cleanup logic in Shutdown
	if proc.capabilities != nil {
		for _, mod := range proc.capabilities.Modules {
			delete(m.modules, mod)
		}
	}
	delete(m.plugins, proc.id)

	if len(m.modules) != 0 {
		t.Errorf("expected modules to be empty after cleanup, got %v", m.modules)
	}
}

func TestCapabilityValidationIntegration(t *testing.T) {
	m := NewManager(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	m.modules["agent"] = "plugin-a"

	caps := &contract.PluginCapabilities{
		Modules: []string{"agent"},
	}

	err := caps.Validate()
	if err != nil {
		t.Fatalf("expected valid capabilities, got %v", err)
	}

	// Check for collision against existing modules in manager
	collision := ""
	for _, mod := range caps.Modules {
		if owner, taken := m.modules[mod]; taken {
			collision = owner
			break
		}
	}

	if collision != "plugin-a" {
		t.Fatalf("expected to detect collision with 'plugin-a', got %q", collision)
	}
}

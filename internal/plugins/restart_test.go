package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestRestartReloadsSettingsAndPreservesOtherPlugins(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TACHYON_DATA_DIR", root)
	pluginDir := t.TempDir()
	path := filepath.Join(pluginDir, "first-plugin")
	caps := declarationFor("first", "First")
	caps.Settings = &contract.SettingsDeclaration{Fields: []contract.SettingsField{{Key: "enabled", Type: contract.SettingsFieldBoolean, Default: true}}}
	encoded, _ := json.Marshal(caps)
	if err := os.WriteFile(filepath.Join(pluginDir, "capabilities.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	var spawned []*pluginProcess
	var configs []map[string]string
	m.spawn = func(ctx context.Context, binary string) (*pluginProcess, error) {
		if ctx != lifetime || binary != path {
			t.Errorf("restart changed lifetime/path")
		}
		_, config, err := pluginInitSettings(binary)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
		proc := fakeProcess(t, "first-plugin", "declared", caps)
		spawned = append(spawned, proc)
		return proc, nil
	}
	if err := m.LoadPlugin(lifetime, path); err != nil {
		t.Fatal(err)
	}
	other := declarationFor("other", "Other")
	if err := m.initializePlugin(lifetime, fakeProcess(t, "other-plugin", "declared", other)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "config-ops"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config-ops", "settings.json"), []byte(`{"first-plugin":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.RestartPlugin("first-plugin"); err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 || configs[0]["enabled"] != "true" || configs[1]["enabled"] != "false" {
		t.Fatalf("settings not reloaded: %+v", configs)
	}
	if spawned[0] == m.plugins["first-plugin"] || !spawned[0].stopped || m.plugins["other-plugin"] == nil || m.ModuleOwner("other") != "other-plugin" || m.ModuleOwner("first") != "first-plugin" {
		t.Fatal("incorrect process/module ownership")
	}
	// A surviving shared group becomes first-loaded after the original owner
	// is removed; the restarted plugin contributes its distinct item again.
	nav := m.MergedNav()
	if len(nav.Groups) != 1 || nav.Groups[0].Label != "Other" || len(nav.Items) != 2 {
		t.Fatalf("incorrect nav re-election: %+v", nav)
	}
	if _, err := callProcess(lifetime, spawned[0], "command/execute", nil); err == nil || !strings.Contains(err.Error(), "unloaded") {
		t.Fatalf("old process accepted call: %v", err)
	}
}

func TestFailedRestartLeavesNoRegistration(t *testing.T) {
	for _, mode := range []string{"spawn-error", "bad-declaration", "changed-identity"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			m := NewManager(slog.New(slog.NewJSONHandler(&logs, nil)))
			old := fakeProcess(t, "old", "declared", declarationFor("first", "First"))
			old.binaryPath = "fake"
			if err := m.initializePlugin(context.Background(), old); err != nil {
				t.Fatal(err)
			}
			other := fakeProcess(t, "other", "declared", declarationFor("other", "Other"))
			if err := m.initializePlugin(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			var failed *pluginProcess
			m.spawn = func(context.Context, string) (*pluginProcess, error) {
				if mode == "spawn-error" {
					return nil, errors.New("missing executable")
				}
				caps := declarationFor("first", "New")
				id := "old"
				if mode == "bad-declaration" {
					caps.Nav.Items[0].RequiresVerb = "unknown"
				} else {
					id = "changed"
				}
				failed = fakeProcess(t, id, "declared", caps)
				return failed, nil
			}
			if err := m.RestartPlugin("old"); err == nil {
				t.Fatal("bad restart succeeded")
			}
			if !old.stopped || m.plugins["old"] != nil || m.plugins["changed"] != nil || m.ModuleOwner("first") != "" || m.ModuleOwner("other") != "other" {
				t.Fatal("half-registered failed restart")
			}
			if failed != nil && !failed.stopped {
				t.Fatal("failed process not stopped")
			}
			if nav := m.MergedNav(); len(nav.Items) != 1 || nav.Groups[0].Label != "Other" {
				t.Fatalf("stale nav: %+v", nav)
			}
			if !strings.Contains(logs.String(), "plugin remains unloaded") {
				t.Fatalf("failure not logged: %s", logs.String())
			}
		})
	}
}

func TestRestartWaitsForSerialWireAndShutdownClearsRegistry(t *testing.T) {
	m := NewManager(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	old := fakeProcess(t, "old", "declared")
	old.binaryPath = "fake"
	if err := m.initializePlugin(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	spawned := make(chan struct{}, 1)
	m.spawn = func(context.Context, string) (*pluginProcess, error) {
		spawned <- struct{}{}
		return fakeProcess(t, "old", "declared"), nil
	}
	old.callMu.Lock()
	done := make(chan error, 1)
	go func() { done <- m.RestartPlugin("old") }()
	select {
	case <-spawned:
		old.callMu.Unlock()
		t.Fatal("respawn bypassed wire lock")
	case <-time.After(20 * time.Millisecond):
	}
	if m.ModuleOwner("agent") != "old" {
		old.callMu.Unlock()
		t.Fatal("removed plugin during active round trip")
	}
	old.callMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.AllCapabilities()) != 0 || m.ModuleOwner("agent") != "" || len(m.MergedNav().Items) != 0 {
		t.Fatal("shutdown retained registrations")
	}
	if err := m.RestartPlugin("missing"); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("not-found error: %v", err)
	}
}

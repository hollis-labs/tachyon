package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func retiredFixture(t *testing.T) (*Manager, contract.SettingsTarget) {
	t.Helper()
	m := watchdogManager()
	proc, _, _ := stalledProcess(t, "decode")
	proc.name = "Hung Plugin"
	registerStalled(m, proc)
	before := time.Now()
	if _, err := m.CallPlugin(context.Background(), "hung", "command/execute", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	awaitCondition(t, func() bool { return m.ModuleOwner("hung") == "" })
	targets := m.SettingsTargets()
	if len(targets) != 1 {
		t.Fatalf("retired target missing: %+v", targets)
	}
	target := targets[0]
	if target.ID != "hung" || target.Name != "Hung Plugin" || target.State != "unloaded" || target.Reason != "timeout" || target.RetiredAt == nil || target.RetiredAt.Before(before) || target.RetiredAt.After(time.Now()) || target.RetiredAt.Location() != time.UTC || len(target.Settings.Fields) != 0 {
		t.Fatalf("wrong retirement metadata: %+v", target)
	}
	if got := m.BuildRegistry(); len(got.Plugins) != 0 || !reflect.DeepEqual(got.RetiredPlugins, targets) || len(m.AllCapabilities()) != 0 || len(m.MergedNav().Items) != 0 {
		t.Fatal("retired plugin regained routing claims or registry metadata disagrees")
	}
	return m, target
}

func TestRetirementMetadataLifecycle(t *testing.T) {
	for _, action := range []string{"success", "busy", "failure", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			m, before := retiredFixture(t)
			switch action {
			case "success":
				m.spawn = func(context.Context, string) (*pluginProcess, error) {
					p := fakeProcess(t, "hung", "declared", declarationFor("hung", "Recovered"))
					p.name = "Recovered"
					return p, nil
				}
				if err := m.RestartPlugin("hung"); err != nil {
					t.Fatal(err)
				}
				targets := m.SettingsTargets()
				if len(targets) != 1 || targets[0].State != "" || targets[0].Reason != "" || targets[0].RetiredAt != nil || targets[0].Name != "Recovered" || len(m.BuildRegistry().RetiredPlugins) != 0 || m.ModuleOwner("hung") != "hung" {
					t.Fatalf("successful restart left retirement data: %+v", targets)
				}
				if _, err := m.InvokeVerb(context.Background(), "hung_list", nil); err != nil {
					t.Fatal(err)
				}
			case "busy":
				m.lifecycleMu.Lock()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				err := m.restartPlugin(ctx, "hung")
				cancel()
				m.lifecycleMu.Unlock()
				if !errors.Is(err, ErrRestartBusy) {
					t.Fatal(err)
				}
			case "failure":
				m.spawn = func(context.Context, string) (*pluginProcess, error) { return nil, errors.New("fake spawn failure") }
				if err := m.RestartPlugin("hung"); err == nil {
					t.Fatal("failed spawn succeeded")
				}
			case "shutdown":
				if err := m.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				if len(m.SettingsTargets()) != 0 || len(m.BuildRegistry().RetiredPlugins) != 0 {
					t.Fatal("shutdown retained metadata")
				}
			}
			if action == "busy" || action == "failure" {
				if targets := m.SettingsTargets(); len(targets) != 1 || !reflect.DeepEqual(targets[0], before) {
					t.Fatalf("unsuccessful restart changed retirement: %+v", targets)
				}
			}
			if err := m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadedSettingsAndRegistryJSONCompatibility(t *testing.T) {
	m := watchdogManager()
	m.plugins["legacy"] = &pluginProcess{id: "legacy", name: "Legacy"}
	encoded, err := json.Marshal(m.SettingsTargets())
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `[{"id":"legacy","name":"Legacy","settings":{}}]` {
		t.Fatalf("loaded target JSON changed: %s", encoded)
	}
	encoded, err = json.Marshal(m.BuildRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var core map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &core); err != nil {
		t.Fatal(err)
	}
	if _, present := core["retired_plugins"]; present {
		t.Fatal("loaded registry gained retirement data")
	}
	// Existing SDK decoders ignore new top-level fields and keep active maps.
	m, before := retiredFixture(t)
	encoded, err = json.Marshal(m.BuildRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var old registry.Response
	if err := json.Unmarshal(encoded, &old); err != nil || len(old.Plugins) != 0 {
		t.Fatalf("legacy registry decode failed: %s %v", encoded, err)
	}
	var legacyTargets []struct {
		ID, Name string
		Settings contract.SettingsDeclaration
	}
	encoded, _ = json.Marshal(m.SettingsTargets())
	if err := json.Unmarshal(encoded, &legacyTargets); err != nil || len(legacyTargets) != 1 || legacyTargets[0].ID != before.ID {
		t.Fatalf("legacy Settings decode failed: %s %v", encoded, err)
	}
	// Metadata returned to a caller cannot mutate the tombstone timestamp.
	returned := m.SettingsTargets()
	*returned[0].RetiredAt = time.Time{}
	if got := m.SettingsTargets()[0]; got.RetiredAt.IsZero() {
		t.Fatal("timestamp escaped by reference")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRetirementReasonClasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want string
	}{
		{"transport", context.Background(), errors.New("private provider detail"), "transport_error"},
		{"syntax", context.Background(), &json.SyntaxError{}, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pluginProcess{}
			interruptCall(p, tc.ctx, tc.err)
			interruptProcess(p) // Later teardown must preserve the initial reason.
			if !p.dead.Load() || p.deathReason != tc.want {
				t.Fatalf("reason=%s", p.deathReason)
			}
		})
	}
}

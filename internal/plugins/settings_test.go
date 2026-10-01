package plugins

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestReadPersistedSettingsForSpawn(t *testing.T) {
	root := t.TempDir()
	pluginDir := t.TempDir()
	caps := contract.PluginCapabilities{Modules: []string{"example"}, Settings: &contract.SettingsDeclaration{Fields: []contract.SettingsField{
		{Key: "endpoint", Type: contract.SettingsFieldString, Default: "http://localhost:1"},
		{Key: "enabled", Type: contract.SettingsFieldBoolean, Default: true},
		{Key: "count", Type: contract.SettingsFieldNumber, Default: 1},
	}}}
	encoded, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "capabilities.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := readPluginSettings(root, pluginDir, "example")
	if err != nil || config["endpoint"] != "http://localhost:1" || config["enabled"] != "true" || config["count"] != "1" {
		t.Fatalf("defaults: %+v %v", config, err)
	}
	dir := filepath.Join(root, "config-ops")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"example":{"endpoint":"http://localhost:2","enabled":false,"count":0,"unknown":"ignore"},"other":{"endpoint":"http://wrong"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = readPluginSettings(root, pluginDir, "example")
	if err != nil || config["endpoint"] != "http://localhost:2" || config["enabled"] != "false" || config["count"] != "0" {
		t.Fatalf("overrides: %+v %v", config, err)
	}
	if _, ok := config["unknown"]; ok {
		t.Fatal("undeclared field passed to init")
	}
	for _, body := range []string{`{`, `null`, `{"example":{"enabled":"not a boolean"}}`, `{"example":{"count":null}}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPluginSettings(root, pluginDir, "example"); err == nil {
			t.Fatalf("invalid storage accepted: %s", body)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(pluginDir, "capabilities.json")); err != nil {
		t.Fatal(err)
	}
	if config, err := readPluginSettings(root, pluginDir, "legacy"); err != nil || len(config) != 0 {
		t.Fatalf("legacy: %+v %v", config, err)
	}
}

func TestHostSuppliesDataDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TACHYON_DATA_DIR", root)
	binary := filepath.Join(t.TempDir(), "example")
	dataDir, config, err := pluginInitSettings(binary)
	if err != nil || dataDir != filepath.Join(root, "example") || len(config) != 0 {
		t.Fatalf("init: %s %+v %v", dataDir, config, err)
	}
	if _, err := os.Stat(filepath.Join(root, "example")); !os.IsNotExist(err) {
		t.Fatal("host created plugin storage")
	}
}

func TestConfigInvocationSyncsEveryLoadedSchema(t *testing.T) {
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.plugins["example"] = &pluginProcess{id: "example", name: "Example", capabilities: &contract.PluginCapabilities{Settings: &contract.SettingsDeclaration{Fields: []contract.SettingsField{{Key: "enabled", Type: contract.SettingsFieldBoolean}}}}}
	m.plugins["legacy"] = &pluginProcess{id: "legacy", name: "Legacy"}
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	t.Cleanup(func() { inputWriter.Close(); outputReader.Close() })
	m.plugins["config-ops"] = &pluginProcess{id: "config-ops", stdin: inputWriter, stdout: outputReader, stdoutDec: json.NewDecoder(outputReader), capabilities: &contract.PluginCapabilities{Modules: []string{"config"}, Verbs: map[string]contract.VerbDeclaration{"config_get": {Effect: contract.EffectReads}}}}
	m.modules["config"] = "config-ops"
	done := make(chan []string, 1)
	go func() {
		defer inputReader.Close()
		defer outputWriter.Close()
		dec := json.NewDecoder(inputReader)
		enc := json.NewEncoder(outputWriter)
		var calls []string
		for i := 0; i < 2; i++ {
			var req struct {
				Method string                       `json:"method"`
				Params subprocess.CommandExecParams `json:"params"`
			}
			if err := dec.Decode(&req); err != nil {
				done <- append(calls, "decode failed")
				return
			}
			calls = append(calls, req.Params.Name)
			if i == 0 {
				var targets []contract.SettingsTarget
				if err := json.Unmarshal([]byte(req.Params.Args), &targets); err != nil {
					calls = append(calls, "bad schemas")
				}
				seen := map[string]bool{}
				for _, target := range targets {
					seen[target.ID] = true
					if target.ID == "example" && len(target.Settings.Fields) == 0 {
						calls = append(calls, "lost fields")
					}
				}
				if !seen["example"] || !seen["legacy"] || !seen["config-ops"] {
					calls = append(calls, "missing target")
				}
			}
			content, _ := json.Marshal(contract.ResultEnvelope{Status: contract.StatusOK})
			result, _ := json.Marshal(subprocess.CommandExecResult{Action: "message", Content: string(content)})
			if err := enc.Encode(subprocess.RPCResponse{JSONRPC: "2.0", Result: result}); err != nil {
				done <- append(calls, "encode failed")
				return
			}
		}
		done <- calls
	}()
	if _, err := m.InvokeVerb(context.Background(), "config_get", json.RawMessage(`{"plugin":"example"}`)); err != nil {
		t.Fatal(err)
	}
	if calls := <-done; strings.Join(calls, ",") != "config_schemas,config_get" {
		t.Fatalf("calls: %v", calls)
	}
}

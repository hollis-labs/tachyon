package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

func commandEnvelope(t *testing.T, p *plugin, name, payload string) contract.ResultEnvelope {
	t.Helper()
	result, err := p.Command(context.Background(), subprocess.CommandRequest{Name: name, Args: payload})
	if err != nil {
		t.Fatal(err)
	}
	var envelope contract.ResultEnvelope
	if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestConfigCommands(t *testing.T) {
	p := &plugin{}
	if _, err := p.Init(context.Background(), subprocess.InitParams{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]ConfigTarget{testTarget()})
	if err != nil {
		t.Fatal(err)
	}
	if result := commandEnvelope(t, p, "config_schemas", string(encoded)); result.Status != contract.StatusOK {
		t.Fatalf("sync: %+v", result)
	}
	for _, verb := range []string{"config_schema", "config_get", "config_list"} {
		if result := commandEnvelope(t, p, verb, `{"plugin":"example"}`); result.Status != contract.StatusOK {
			t.Fatalf("%s: %+v", verb, result)
		}
	}
	result := commandEnvelope(t, p, "config_set", `{"plugin":"example","values":{"enabled":false}}`)
	if result.Status != contract.StatusOK {
		t.Fatalf("set: %+v", result)
	}
	var data struct {
		Plugin          string       `json:"plugin"`
		RestartRequired bool         `json:"restart_required"`
		Config          TargetConfig `json:"config"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Plugin != "example" || !data.RestartRequired || data.Config.Values["enabled"] != false {
		t.Fatalf("restart contract: %+v", data)
	}
	if result := commandEnvelope(t, p, "config_reset", `{"plugin":"example"}`); result.Status != contract.StatusOK {
		t.Fatalf("reset: %+v", result)
	}
	for _, payload := range []string{`{}`, `{"plugin":"example"}`, `{"plugin":"example","values":{"unknown":"do not echo me"}}`, `{"plugin":"example","values":{"enabled":"do not echo me"}}`} {
		result := commandEnvelope(t, p, "config_set", payload)
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "do not echo me") {
			t.Fatal("rejected value echoed")
		}
		if result.Status != contract.StatusError || result.Error.Code != "validation" {
			t.Fatalf("validation: %+v", result)
		}
	}
	if result := commandEnvelope(t, p, "config_get", `{"plugin":"unknown"}`); result.Status != contract.StatusError {
		t.Fatalf("unknown: %+v", result)
	}
	resultCaps, err := p.Command(context.Background(), subprocess.CommandRequest{Name: pluginkit.CommandCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	var caps contract.PluginCapabilities
	if err := json.Unmarshal([]byte(resultCaps.Content), &caps); err != nil {
		t.Fatal(err)
	}
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"config_update", "config_validate", "config_schemas"} {
		if _, ok := p.Capabilities().Verbs[verb]; ok {
			t.Fatalf("reserved/obsolete verb advertised: %s", verb)
		}
	}
	if _, err := p.Command(context.Background(), subprocess.CommandRequest{Name: "unknown"}); err == nil {
		t.Fatal("unknown command accepted")
	}
}

func TestInitNeedsDataDirAndBadSyncPreservesSchemas(t *testing.T) {
	p := &plugin{}
	if _, err := p.Init(context.Background(), subprocess.InitParams{}); err == nil {
		t.Fatal("missing DataDir accepted")
	}
	p.adapter = configuredAdapter(t, t.TempDir())
	if _, err := p.Command(context.Background(), subprocess.CommandRequest{Name: "config_schemas", Args: `{`}); err == nil {
		t.Fatal("invalid schema payload accepted")
	}
	if view, err := p.adapter.Read(context.Background(), "example"); err != nil || view.Target.ID != "example" {
		t.Fatalf("bad sync replaced targets: %+v %v", view, err)
	}
}

func TestConfigListPreservesRetiredTargetMetadata(t *testing.T) {
	p := &plugin{}
	if _, err := p.Init(context.Background(), subprocess.InitParams{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	// Host's config_schemas carrier includes additive metadata. A future field
	// remains ignored, preserving compatibility across independent host builds.
	payload := `[{"id":"hung","name":"Hung","settings":{},"state":"unloaded","reason":"timeout","retired_at":"2026-10-01T19:00:00Z","future_field":"ignored"}]`
	if env := commandEnvelope(t, p, "config_schemas", payload); env.Status != contract.StatusOK {
		t.Fatalf("metadata sync failed: %+v", env)
	}
	env := commandEnvelope(t, p, "config_list", `{}`)
	var targets []contract.SettingsTarget
	if err := json.Unmarshal(env.Data, &targets); err != nil {
		t.Fatal(err)
	}
	if env.Status != contract.StatusOK || len(targets) != 1 || targets[0].State != "unloaded" || targets[0].Reason != "timeout" || targets[0].RetiredAt == nil || targets[0].ID != "hung" {
		t.Fatalf("config_list lost metadata: %s", env.Data)
	}
	if env := commandEnvelope(t, p, "config_schemas", `[]`); env.Status != contract.StatusOK {
		t.Fatal("clear failed")
	}
	env = commandEnvelope(t, p, "config_list", `{}`)
	if err := json.Unmarshal(env.Data, &targets); err != nil || len(targets) != 0 {
		t.Fatalf("removed target persisted: %s", env.Data)
	}
}

func TestConfigWriteRestartRequired(t *testing.T) {
	for _, tc := range []struct {
		name        string
		initial     map[string]any
		verb        string
		values      map[string]any
		wantRestart bool
		wantEnabled bool
	}{
		{name: "no-op set defaults", verb: "config_set", values: map[string]any{"enabled": true}, wantEnabled: true},
		{name: "no-op set existing override", initial: map[string]any{"enabled": false}, verb: "config_set", values: map[string]any{"enabled": false}},
		{name: "no-op set numeric default", verb: "config_set", values: map[string]any{"count": 1}, wantEnabled: true},
		{name: "no-op padded URL", verb: "config_set", values: map[string]any{"endpoint": " \thttp://localhost:1\n"}, wantEnabled: true},
		{name: "no-op padded select", verb: "config_set", values: map[string]any{"mode": " \tlocal\n"}, wantEnabled: true},
		{name: "no-op reset padded override", initial: map[string]any{"endpoint": " \thttp://localhost:1\n"}, verb: "config_reset", wantEnabled: true},
		{name: "no-op reset defaults", verb: "config_reset", wantEnabled: true},
		{name: "no-op reset default override", initial: map[string]any{"enabled": true}, verb: "config_reset", wantEnabled: true},
		{name: "real set", verb: "config_set", values: map[string]any{"enabled": false}, wantRestart: true},
		{name: "real reset", initial: map[string]any{"enabled": false}, verb: "config_reset", wantRestart: true, wantEnabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := configuredAdapter(t, dir)
			if tc.initial != nil {
				if _, err := a.Update(context.Background(), "example", tc.initial); err != nil {
					t.Fatal(err)
				}
			}
			// Reopen storage as a new process would; schemas and numbers arrive as JSON.
			a = configuredAdapter(t, dir)
			schema, err := json.Marshal([]ConfigTarget{testTarget()})
			if err != nil {
				t.Fatal(err)
			}
			p := &plugin{adapter: a}
			if result := commandEnvelope(t, p, "config_schemas", string(schema)); result.Status != contract.StatusOK {
				t.Fatalf("schema: %+v", result)
			}
			payload, err := json.Marshal(map[string]any{"plugin": "example", "values": tc.values})
			if err != nil {
				t.Fatal(err)
			}
			result := commandEnvelope(t, p, tc.verb, string(payload))
			if result.Status != contract.StatusOK {
				t.Fatalf("write: %+v", result)
			}
			var data struct {
				RestartRequired *bool        `json:"restart_required"`
				Config          TargetConfig `json:"config"`
			}
			if err := json.Unmarshal(result.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.RestartRequired == nil || *data.RestartRequired != tc.wantRestart {
				t.Fatalf("restart_required: %s", result.Data)
			}
			if data.Config.Values["enabled"] != tc.wantEnabled {
				t.Fatalf("write result: %+v", data.Config)
			}
			persisted, err := configuredAdapter(t, dir).Read(context.Background(), "example")
			if err != nil || persisted.Values["enabled"] != tc.wantEnabled {
				t.Fatalf("persisted result: %+v %v", persisted, err)
			}
		})
	}
}

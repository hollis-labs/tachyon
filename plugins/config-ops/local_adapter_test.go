package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func testTarget() ConfigTarget {
	return ConfigTarget{ID: "example", Name: "Example", Settings: contract.SettingsDeclaration{Fields: []contract.SettingsField{
		{Key: "endpoint", Type: contract.SettingsFieldString, Required: true, Default: "http://localhost:1"},
		{Key: "enabled", Type: contract.SettingsFieldBoolean, Default: true},
		{Key: "count", Type: contract.SettingsFieldNumber, Default: 1},
		{Key: "mode", Type: contract.SettingsFieldSelect, Default: "local", Options: []contract.SettingsOption{{Value: "local"}, {Value: "remote"}}},
	}}}
}

func configuredAdapter(t *testing.T, dir string) *LocalAdapter {
	t.Helper()
	a, err := NewLocalAdapter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetSchemas([]ConfigTarget{testTarget()}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPersistenceValidationAndReset(t *testing.T) {
	dir := t.TempDir()
	a := configuredAdapter(t, dir)
	ctx := context.Background()
	view, err := a.Read(ctx, "example")
	if err != nil || view.Values["endpoint"] != "http://localhost:1" || !view.Validation.Valid {
		t.Fatalf("defaults: %+v %v", view, err)
	}
	patch := map[string]any{"endpoint": "http://localhost:2", "enabled": false, "count": 0, "mode": "remote"}
	validation, err := a.Validate(ctx, "example", patch)
	if err != nil || !validation.Valid {
		t.Fatalf("validate: %+v %v", validation, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("validate wrote settings")
	}
	view, err = a.Update(ctx, "example", patch)
	if err != nil || view.Values["enabled"] != false || view.Values["count"] != 0 {
		t.Fatalf("update: %+v %v", view, err)
	}
	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions: %v %v", info, err)
	}
	reopened := configuredAdapter(t, dir)
	view, err = reopened.Read(ctx, "example")
	if err != nil || view.Values["endpoint"] != "http://localhost:2" || view.Values["enabled"] != false || view.Values["count"] != float64(0) {
		t.Fatalf("reopen: %+v %v", view, err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Update(ctx, "example", map[string]any{"mode": "invalid"}); err == nil {
		t.Fatal("bad update accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(before) != string(after) {
		t.Fatal("invalid update changed persisted settings")
	}
	view, err = a.Reset(ctx, "example")
	if err != nil || view.Values["endpoint"] != "http://localhost:1" || view.Values["enabled"] != true {
		t.Fatalf("reset: %+v %v", view, err)
	}
	reopened = configuredAdapter(t, dir)
	view, err = reopened.Read(ctx, "example")
	if err != nil || view.Values["mode"] != "local" {
		t.Fatalf("reset not persisted: %+v %v", view, err)
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	a := configuredAdapter(t, t.TempDir())
	ctx := context.Background()
	for _, patch := range []map[string]any{
		{"unknown": "value"}, {"enabled": "false"}, {"count": "1"}, {"mode": "invalid"}, {"endpoint": " "}, {"endpoint": nil},
	} {
		validation, err := a.Validate(ctx, "example", patch)
		if err != nil || validation.Valid {
			t.Fatalf("invalid patch %+v: %+v %v", patch, validation, err)
		}
		if _, err := a.Update(ctx, "example", patch); err == nil {
			t.Fatalf("invalid patch persisted: %+v", patch)
		}
	}
	if _, err := a.Update(ctx, "unknown", map[string]any{}); err == nil {
		t.Fatal("unknown plugin accepted")
	}
	if _, err := a.Reset(ctx, "unknown"); err == nil {
		t.Fatal("unknown plugin reset")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.Update(ctx, "example", map[string]any{}); err == nil {
		t.Fatal("canceled write succeeded")
	}
}

func TestMissingRequiredDefaultsAndSchemaChanges(t *testing.T) {
	a := configuredAdapter(t, t.TempDir())
	ctx := context.Background()
	target := testTarget()
	target.Settings.Fields[0].Default = nil
	if err := a.SetSchemas([]ConfigTarget{target}); err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(ctx, "example")
	if err != nil || view.Validation.Valid || view.Validation.Errors["endpoint"] == "" {
		t.Fatalf("required: %+v %v", view, err)
	}
	if _, err := a.Update(ctx, "example", map[string]any{"endpoint": "http://localhost:3"}); err != nil {
		t.Fatal(err)
	}
	view, err = a.Reset(ctx, "example")
	if err != nil || view.Validation.Valid {
		t.Fatalf("reset required value: %+v %v", view, err)
	}
	if err := a.SetSchemas([]ConfigTarget{target, target}); err == nil {
		t.Fatal("duplicate target accepted")
	}
	bad := testTarget()
	bad.Settings.Fields[0].Type = "secret"
	if err := a.SetSchemas([]ConfigTarget{bad}); err == nil {
		t.Fatal("secret schema accepted")
	}
	if _, err := a.Read(ctx, "example"); err != nil {
		t.Fatal("invalid schema replaced old schema")
	}
}

func TestRemovedFieldsAreNotEchoed(t *testing.T) {
	a := configuredAdapter(t, t.TempDir())
	ctx := context.Background()
	if _, err := a.Update(ctx, "example", map[string]any{"endpoint": "http://old"}); err != nil {
		t.Fatal(err)
	}
	target := testTarget()
	target.Settings.Fields = target.Settings.Fields[1:]
	if err := a.SetSchemas([]ConfigTarget{target}); err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(ctx, "example")
	if err != nil || view.Validation.Valid || view.Validation.Errors["endpoint"] == "" {
		t.Fatalf("removed field validation: %+v %v", view, err)
	}
	if _, exists := view.Values["endpoint"]; exists {
		t.Fatal("undeclared stored value echoed")
	}
}

func TestCorruptStorageAndWriteFailure(t *testing.T) {
	for _, body := range []string{"{", "null", "{} {}"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewLocalAdapter(dir); err == nil {
			t.Fatalf("corrupt body accepted: %s", body)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "settings.json")); string(got) != body {
			t.Fatal("corrupt storage modified")
		}
	}
	if _, err := NewLocalAdapter(""); err == nil {
		t.Fatal("missing DataDir accepted")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	a := configuredAdapter(t, dir)
	a.path = filepath.Join(blocked, "settings.json")
	if _, err := a.Update(context.Background(), "example", map[string]any{"enabled": false}); err == nil {
		t.Fatal("write failure lost")
	}
	view, err := a.Read(context.Background(), "example")
	if err != nil || view.Values["enabled"] != true {
		t.Fatalf("failed write changed memory: %+v %v", view, err)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestUpdateRevalidatesAfterSchemaChange(t *testing.T) {
	dir := t.TempDir()
	a := configuredAdapter(t, dir)
	ctx := context.Background()
	if _, err := a.Update(ctx, "example", map[string]any{"endpoint": "http://fixture"}); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"endpoint": "http://user:private@fixture"}
	// A preflight under the old declaration is insufficient: the locked update
	// must validate the full candidate against the current declaration again.
	if result, err := a.Validate(ctx, "example", patch); err != nil || !result.Valid {
		t.Fatalf("preflight: %+v %v", result, err)
	}
	target := testTarget()
	target.Settings.Fields[0].Validation = contract.SettingsValidationHTTPBaseURL
	if err := a.SetSchemas([]ConfigTarget{target}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Update(ctx, "example", patch)
	var invalid *ConfigurationValidationError
	if !errors.As(err, &invalid) || invalid.Validation.Errors["endpoint"] == "" {
		t.Fatalf("locked validation missing: %v", err)
	}
	env, _ := configResult(nil, err)
	encoded, _ := json.Marshal(env)
	if env.Error == nil || env.Error.Code != "validation" || strings.Contains(string(encoded), "private") {
		t.Fatalf("unsafe validation envelope: %s", encoded)
	}
	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed validation changed storage", err)
	}
	view, err := a.Read(ctx, "example")
	if err != nil || view.Values["endpoint"] != "http://fixture" {
		t.Fatal("failed validation changed memory", err)
	}
}

func TestSemanticPatchScopeKeepsFullReporting(t *testing.T) {
	dir := t.TempDir()
	a := configuredAdapter(t, dir)
	ctx := context.Background()
	legacy := "http://user:private@fixture"
	if _, err := a.Update(ctx, "example", map[string]any{"endpoint": legacy}); err != nil {
		t.Fatal(err)
	}
	target := testTarget()
	target.Settings.Fields[0].Validation = contract.SettingsValidationHTTPBaseURL
	if err := a.SetSchemas([]ConfigTarget{target}); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"enabled": false}
	preflight, err := a.Validate(ctx, "example", patch)
	if err != nil || preflight.Valid || preflight.Errors["endpoint"] == "" {
		t.Fatal("preflight hid untouched semantic issue", err)
	}
	view, err := a.Update(ctx, "example", patch)
	if err != nil || view.Values["endpoint"] != legacy || view.Values["enabled"] != false || view.Validation.Valid || view.Validation.Errors["endpoint"] == "" {
		t.Fatal("unrelated save blocked or hid legacy issue", err)
	}
	a = configuredAdapter(t, dir)
	if err := a.SetSchemas([]ConfigTarget{target}); err != nil {
		t.Fatal(err)
	}
	view, err = a.Read(ctx, "example")
	if err != nil || view.Values["endpoint"] != legacy || view.Validation.Valid || view.Validation.Errors["endpoint"] == "" {
		t.Fatal("read hid persisted legacy issue", err)
	}
	if _, err := a.Update(ctx, "example", map[string]any{"endpoint": "http://another:private@fixture"}); err == nil {
		t.Fatal("patched invalid semantic value accepted")
	}
	if _, err := a.Update(ctx, "example", map[string]any{"endpoint": "http://fixture"}); err != nil {
		t.Fatal("valid replacement rejected", err)
	}
	// A structural defect in an untouched stored field still blocks the merged
	// candidate. Semantic patch scoping must not weaken type admission.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"example":{"enabled":"false"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a = configuredAdapter(t, dir)
	if _, err := a.Update(ctx, "example", map[string]any{"mode": "remote"}); err == nil {
		t.Fatal("untouched structural defect was ignored")
	}
}

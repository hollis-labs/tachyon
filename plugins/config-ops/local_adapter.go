package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hollis-labs/tachyon/internal/contract"
)

type LocalAdapter struct {
	mu        sync.RWMutex
	path      string
	targets   map[string]ConfigTarget
	overrides map[string]map[string]any
}

// NewLocalAdapter requires a host-owned persistent directory. Corrupt existing
// data is an error, never a reason to silently replace it with empty defaults.
func NewLocalAdapter(dataDir string) (*LocalAdapter, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("persistent data directory is required")
	}
	a := &LocalAdapter{path: filepath.Join(dataDir, "settings.json"), targets: map[string]ConfigTarget{}, overrides: map[string]map[string]any{}}
	f, err := os.Open(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err := dec.Decode(&a.overrides); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	if a.overrides == nil {
		return nil, fmt.Errorf("settings must be an object")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("settings contains trailing content")
	}
	return a, nil
}

func (a *LocalAdapter) SetSchemas(targets []ConfigTarget) error {
	next := make(map[string]ConfigTarget, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.ID) == "" {
			return fmt.Errorf("target id is required")
		}
		if _, exists := next[target.ID]; exists {
			return fmt.Errorf("duplicate target %q", target.ID)
		}
		caps := contract.PluginCapabilities{Modules: []string{"config"}, Settings: &target.Settings}
		if err := caps.Validate(); err != nil {
			return fmt.Errorf("target %q schema: %w", target.ID, err)
		}
		// Detach schemas from the caller before retaining them.
		encoded, err := json.Marshal(target)
		if err != nil {
			return err
		}
		var copy ConfigTarget
		if err := json.Unmarshal(encoded, &copy); err != nil {
			return err
		}
		next[target.ID] = copy
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.targets = next
	return nil
}

func (a *LocalAdapter) List(ctx context.Context) ([]ConfigTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]ConfigTarget, 0, len(a.targets))
	for _, target := range a.targets {
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func copyValues(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func (a *LocalAdapter) effective(id string) (ConfigTarget, map[string]any, error) {
	target, ok := a.targets[id]
	if !ok {
		return ConfigTarget{}, nil, fmt.Errorf("unknown config target %q", id)
	}
	values := map[string]any{}
	for _, field := range target.Settings.Fields {
		if field.Default != nil {
			values[field.Key] = field.Default
		}
	}
	for key, value := range a.overrides[id] {
		values[key] = value
	}
	return target, values, nil
}

// normalizeValues uses the same accepted-value resolution as the startup reader.
// Leave invalid values intact so validation can identify their field safely.
func normalizeValues(target ConfigTarget, values map[string]any) map[string]any {
	normalized := copyValues(values)
	for _, field := range target.Settings.Fields {
		if value, exists := normalized[field.Key]; exists {
			if resolved, _, err := contract.ResolveSettingValue(field, value); err == nil {
				normalized[field.Key] = resolved
			}
		}
	}
	return normalized
}

// ConfigurationValidationError carries field errors from the locked update
// check, without including proposed values in its message or error detail.
type ConfigurationValidationError struct{ Validation ValidationResult }

func (*ConfigurationValidationError) Error() string { return "proposed configuration is invalid" }

func validateValues(target ConfigTarget, values map[string]any) ValidationResult {
	result := ValidationResult{Valid: true, Errors: map[string]string{}}
	fields := map[string]contract.SettingsField{}
	for _, field := range target.Settings.Fields {
		fields[field.Key] = field
		value, exists := values[field.Key]
		if !exists {
			if field.Required {
				result.Errors[field.Key] = "value is required"
			}
			continue
		}
		resolved, _, err := contract.ResolveSettingValue(field, value)
		if err != nil {
			result.Errors[field.Key] = err.Error()
			continue
		}
		if err := contract.ValidateSettingWrite(field, resolved); err != nil {
			result.Errors[field.Key] = err.Error()
		}
	}
	for key := range values {
		if _, exists := fields[key]; !exists {
			result.Errors[key] = "field is not declared"
		}
	}
	result.Valid = len(result.Errors) == 0
	return result
}

// visibleValues avoids returning stale values whose keys were removed from a schema.
func visibleValues(target ConfigTarget, values map[string]any) map[string]any {
	visible := map[string]any{}
	for _, field := range target.Settings.Fields {
		if value, exists := values[field.Key]; exists {
			visible[field.Key] = value
		}
	}
	return visible
}

func (a *LocalAdapter) Read(ctx context.Context, id string) (TargetConfig, error) {
	if err := ctx.Err(); err != nil {
		return TargetConfig{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	target, values, err := a.effective(id)
	if err != nil {
		return TargetConfig{}, err
	}
	return TargetConfig{Target: target, Values: visibleValues(target, normalizeValues(target, values)), Validation: validateValues(target, values)}, nil
}

func (a *LocalAdapter) Validate(ctx context.Context, id string, patch map[string]any) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	target, values, err := a.effective(id)
	if err != nil {
		return ValidationResult{}, err
	}
	for key, value := range patch {
		values[key] = value
	}
	return validateValues(target, values), nil
}

// Update persists only supplied overrides, atomically, after validating the
// effective configuration (defaults + existing overrides + this patch).
func (a *LocalAdapter) Update(ctx context.Context, id string, patch map[string]any) (TargetConfig, error) {
	if err := ctx.Err(); err != nil {
		return TargetConfig{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	target, values, err := a.effective(id)
	if err != nil {
		return TargetConfig{}, err
	}
	for key, value := range patch {
		values[key] = value
	}
	validation := validateValues(target, values)
	if !validation.Valid {
		return TargetConfig{}, &ConfigurationValidationError{Validation: validation}
	}
	values = normalizeValues(target, values)
	next := a.copyOverrides()
	next[id] = copyValues(a.overrides[id])
	for key := range patch {
		next[id][key] = values[key]
	}
	if err := a.persist(ctx, next); err != nil {
		return TargetConfig{}, err
	}
	a.overrides = next
	return TargetConfig{Target: target, Values: values, Validation: validation}, nil
}

// Reset removes overrides even when a required field has no default; Read's
// validation then identifies the values the operator still needs to supply.
func (a *LocalAdapter) Reset(ctx context.Context, id string) (TargetConfig, error) {
	if err := ctx.Err(); err != nil {
		return TargetConfig{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, _, err := a.effective(id); err != nil {
		return TargetConfig{}, err
	}
	next := a.copyOverrides()
	delete(next, id)
	if err := a.persist(ctx, next); err != nil {
		return TargetConfig{}, err
	}
	a.overrides = next
	target, values, _ := a.effective(id)
	return TargetConfig{Target: target, Values: visibleValues(target, normalizeValues(target, values)), Validation: validateValues(target, values)}, nil
}

func (a *LocalAdapter) copyOverrides() map[string]map[string]any {
	next := make(map[string]map[string]any, len(a.overrides))
	for id, values := range a.overrides {
		next[id] = copyValues(values)
	}
	return next
}

func (a *LocalAdapter) persist(ctx context.Context, next map[string]map[string]any) error {
	encoded, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), a.path); err != nil {
		return err
	}
	return nil
}

package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// DataRoot is the host-owned persistent plugin state root. No directory is
// created here: config-ops is the sole writer of its settings.json values file.
func DataRoot() (string, error) {
	if root := os.Getenv("TACHYON_DATA_DIR"); root != "" {
		return filepath.Abs(root)
	}
	if root := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(root) {
		return filepath.Join(root, "tachyon", "plugins"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "tachyon", "plugins"), nil
}

func pluginInitSettings(binaryPath string) (string, map[string]string, error) {
	root, err := DataRoot()
	if err != nil {
		return "", nil, err
	}
	id := filepath.Base(binaryPath)
	config, err := readPluginSettings(root, filepath.Dir(binaryPath), id)
	return filepath.Join(root, id), config, err
}

// readPluginSettings uses the authored declaration next to the executable to
// prepare InitParams.Config before runtime discovery can occur. Values apply
// on spawn, not through a live config-change notification.
func readPluginSettings(root, pluginDir, id string) (map[string]string, error) {
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(pluginDir, "capabilities.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var caps contract.PluginCapabilities
	if err := json.Unmarshal(raw, &caps); err != nil {
		return nil, fmt.Errorf("invalid authored capabilities: %w", err)
	}
	if err := caps.Validate(); err != nil {
		return nil, err
	}
	if caps.Settings == nil {
		return out, nil
	}
	var stored map[string]map[string]any
	raw, err = os.ReadFile(filepath.Join(root, "config-ops", "settings.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &stored); err != nil || stored == nil {
			return nil, fmt.Errorf("invalid persisted settings object")
		}
	}
	for _, field := range caps.Settings.Fields {
		value := field.Default
		if override, ok := stored[id][field.Key]; ok {
			if override == nil {
				return nil, fmt.Errorf("invalid persisted value for field %q", field.Key)
			}
			value = override
		}
		if value == nil {
			continue
		}
		field.Default = value
		check := contract.PluginCapabilities{Modules: []string{"config"}, Settings: &contract.SettingsDeclaration{Fields: []contract.SettingsField{field}}}
		if err := check.Validate(); err != nil {
			return nil, fmt.Errorf("invalid persisted field %q", field.Key)
		}
		switch v := value.(type) {
		case string:
			out[field.Key] = v
		case bool:
			out[field.Key] = strconv.FormatBool(v)
		default:
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("encode settings field %q", field.Key)
			}
			out[field.Key] = string(encoded)
		}
	}
	return out, nil
}

// SettingsTargets includes every loaded plugin, including those with no
// configurable fields. Schemas remain plugin-scoped by the runtime plugin ID.
func (m *Manager) SettingsTargets() []contract.SettingsTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]contract.SettingsTarget, 0, len(m.plugins))
	for id, proc := range m.plugins {
		target := contract.SettingsTarget{ID: id, Name: proc.name}
		if proc.capabilities != nil && proc.capabilities.Settings != nil {
			target.Settings = *proc.capabilities.Settings
		}
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) syncConfigSchemas(ctx context.Context, ownerID string) error {
	payload, err := json.Marshal(m.SettingsTargets())
	if err != nil {
		return err
	}
	raw, err := m.CallPlugin(ctx, ownerID, "command/execute", subprocess.CommandExecParams{Name: "config_schemas", Args: string(payload)})
	if err != nil {
		return fmt.Errorf("sync config schemas: %w", err)
	}
	var result subprocess.CommandExecResult
	if err := json.Unmarshal(raw, &result); err != nil || result.Action != "message" {
		return fmt.Errorf("config schema sync failed")
	}
	var envelope contract.ResultEnvelope
	if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil || envelope.Status != contract.StatusOK {
		return fmt.Errorf("config schema sync rejected")
	}
	return nil
}

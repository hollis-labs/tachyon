package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
)

var declarationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var settingsKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

func (pc *PluginCapabilities) validateNav() error {
	if pc.Nav == nil {
		return nil
	}
	groups := make(map[string]bool, len(pc.Nav.Groups))
	for _, g := range pc.Nav.Groups {
		if !declarationIDPattern.MatchString(g.ID) {
			return fmt.Errorf("capabilities: invalid nav group id %q", g.ID)
		}
		if groups[g.ID] {
			return fmt.Errorf("capabilities: duplicate nav group %q", g.ID)
		}
		groups[g.ID] = true
	}
	items := make(map[string]bool, len(pc.Nav.Items))
	for _, item := range pc.Nav.Items {
		if !declarationIDPattern.MatchString(item.ID) {
			return fmt.Errorf("capabilities: invalid nav item id %q", item.ID)
		}
		if items[item.ID] {
			return fmt.Errorf("capabilities: duplicate nav item %q", item.ID)
		}
		items[item.ID] = true
		// Every plugin is self-contained: cross-plugin group references
		// would make validation depend on plugin load order.
		if !groups[item.Group] {
			return fmt.Errorf("capabilities: nav item %q references undeclared group %q", item.ID, item.Group)
		}
		if item.RequiresVerb != "" {
			if _, ok := pc.Verbs[item.RequiresVerb]; !ok {
				return fmt.Errorf("capabilities: nav item %q requires undeclared verb %q", item.ID, item.RequiresVerb)
			}
		}
	}
	return nil
}

func (pc *PluginCapabilities) validateSettings() error {
	if pc.Settings == nil {
		return nil
	}
	keys := make(map[string]bool, len(pc.Settings.Fields))
	for _, field := range pc.Settings.Fields {
		if !settingsKeyPattern.MatchString(field.Key) {
			return fmt.Errorf("capabilities: invalid settings key %q", field.Key)
		}
		if keys[field.Key] {
			return fmt.Errorf("capabilities: duplicate settings key %q", field.Key)
		}
		keys[field.Key] = true
		switch field.Type {
		case SettingsFieldString, SettingsFieldBoolean, SettingsFieldNumber:
		case SettingsFieldSelect:
			if len(field.Options) == 0 {
				return fmt.Errorf("capabilities: select field %q has no options", field.Key)
			}
			values := make(map[string]bool, len(field.Options))
			for _, opt := range field.Options {
				if strings.TrimSpace(opt.Value) == "" || values[opt.Value] {
					return fmt.Errorf("capabilities: select field %q has empty or duplicate option %q", field.Key, opt.Value)
				}
				values[opt.Value] = true
			}
		default:
			return fmt.Errorf("capabilities: settings field %q has invalid type %q", field.Key, field.Type)
		}
		if err := validateSettingsRule(field); err != nil {
			return fmt.Errorf("capabilities: settings field %q: %w", field.Key, err)
		}
		// Missing (or JSON null) defaults mean the operator must supply
		// required values. False and zero are valid required defaults.
		if field.Default == nil {
			continue
		}
		valid := false
		switch field.Type {
		case SettingsFieldString:
			s, ok := field.Default.(string)
			valid = ok && (!field.Required || strings.TrimSpace(s) != "")
		case SettingsFieldBoolean:
			_, valid = field.Default.(bool)
		case SettingsFieldNumber:
			valid = numberDefault(field.Default)
		case SettingsFieldSelect:
			if s, ok := field.Default.(string); ok {
				for _, opt := range field.Options {
					if opt.Value == s {
						valid = true
						break
					}
				}
			}
		}
		if !valid {
			return fmt.Errorf("capabilities: settings field %q has invalid default", field.Key)
		}
	}
	return nil
}

func numberDefault(value any) bool {
	if number, ok := value.(json.Number); ok {
		f, err := number.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		f := reflect.ValueOf(value).Float()
		return !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return false
	}
}

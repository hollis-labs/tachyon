package contract

import (
	"encoding/json"
	"math"
	"testing"
)

func navSettingsCapabilities() PluginCapabilities {
	return PluginCapabilities{Modules: []string{"config"}, Verbs: map[string]VerbDeclaration{"config_read": {Effect: EffectReads}},
		Nav: &NavDeclaration{Groups: []NavGroup{{ID: "settings", Label: "Settings"}}, Items: []NavItem{{ID: "config_read", Group: "settings", RequiresVerb: "config_read"}}},
		Settings: &SettingsDeclaration{Fields: []SettingsField{
			{Key: "endpoint", Type: SettingsFieldString, Required: true},
			{Key: "enabled", Type: SettingsFieldBoolean, Default: false, Required: true},
			{Key: "count", Type: SettingsFieldNumber, Default: 0, Required: true},
			{Key: "mode", Type: SettingsFieldSelect, Default: "local", Options: []SettingsOption{{Value: "local", Label: "Local"}}},
		}},
	}
}

func TestNavSettingsRoundTrip(t *testing.T) {
	caps := navSettingsCapabilities()
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PluginCapabilities
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Nav == nil || decoded.Nav.Items[0].RequiresVerb != "config_read" || decoded.Settings == nil || decoded.Settings.Fields[1].Default != false || decoded.Settings.Fields[2].Default != float64(0) {
		t.Fatalf("declarations dropped: %s", raw)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	legacy := PluginCapabilities{Modules: []string{"agent"}}
	if err := legacy.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	if _, ok := fields["nav"]; ok {
		t.Fatal("legacy declaration contains nav")
	}
	if _, ok := fields["settings"]; ok {
		t.Fatal("legacy declaration contains settings")
	}
}

func TestInvalidNavDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PluginCapabilities)
	}{
		{"unsafe group", func(c *PluginCapabilities) { c.Nav.Groups[0].ID = "../settings" }},
		{"duplicate group", func(c *PluginCapabilities) { c.Nav.Groups = append(c.Nav.Groups, c.Nav.Groups[0]) }},
		{"unsafe item", func(c *PluginCapabilities) { c.Nav.Items[0].ID = "" }},
		{"duplicate item", func(c *PluginCapabilities) { c.Nav.Items = append(c.Nav.Items, c.Nav.Items[0]) }},
		{"missing local group", func(c *PluginCapabilities) { c.Nav.Items[0].Group = "other_plugin_group" }},
		{"missing own verb", func(c *PluginCapabilities) { c.Nav.Items[0].RequiresVerb = "other_read" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := navSettingsCapabilities()
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid nav accepted")
			}
		})
	}
}

func TestInvalidSettingsDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field SettingsField
	}{
		{"empty key", SettingsField{Type: SettingsFieldString}},
		{"unsafe key", SettingsField{Key: "../secret", Type: SettingsFieldString}},
		{"unknown type", SettingsField{Key: "value", Type: "object"}},
		{"empty select", SettingsField{Key: "value", Type: SettingsFieldSelect}},
		{"empty option", SettingsField{Key: "value", Type: SettingsFieldSelect, Options: []SettingsOption{{Value: " "}}}},
		{"duplicate option", SettingsField{Key: "value", Type: SettingsFieldSelect, Options: []SettingsOption{{Value: "one"}, {Value: "one"}}}},
		{"bad select default", SettingsField{Key: "value", Type: SettingsFieldSelect, Default: "two", Options: []SettingsOption{{Value: "one"}}}},
		{"bad string default", SettingsField{Key: "value", Type: SettingsFieldString, Default: 42}},
		{"blank required default", SettingsField{Key: "value", Type: SettingsFieldString, Required: true, Default: " "}},
		{"bad boolean default", SettingsField{Key: "value", Type: SettingsFieldBoolean, Default: "false"}},
		{"bad number default", SettingsField{Key: "value", Type: SettingsFieldNumber, Default: "zero"}},
		{"nonfinite default", SettingsField{Key: "value", Type: SettingsFieldNumber, Default: math.Inf(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := navSettingsCapabilities()
			c.Settings.Fields = []SettingsField{tc.field}
			if err := c.Validate(); err == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
	c := navSettingsCapabilities()
	c.Settings.Fields = append(c.Settings.Fields, c.Settings.Fields[0])
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestValidDefaultTypes(t *testing.T) {
	for _, field := range []SettingsField{
		{Key: "endpoint.url", Type: SettingsFieldString, Default: "", Required: false},
		{Key: "enabled", Type: SettingsFieldBoolean, Default: false, Required: true},
		{Key: "count", Type: SettingsFieldNumber, Default: json.Number("0"), Required: true},
		{Key: "ratio", Type: SettingsFieldNumber, Default: float32(.5)},
		{Key: "mode", Type: SettingsFieldSelect, Required: true, Options: []SettingsOption{{Value: "one"}}},
	} {
		c := navSettingsCapabilities()
		c.Settings.Fields = []SettingsField{field}
		if err := c.Validate(); err != nil {
			t.Fatalf("valid field %+v: %v", field, err)
		}
	}
}

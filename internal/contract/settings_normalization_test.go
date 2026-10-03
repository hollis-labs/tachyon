package contract

import "testing"

func TestNormalizedSettingsValidateWithoutTypeCoercion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		field   SettingsField
		value   any
		invalid bool
	}{
		{name: "padded_select", field: SettingsField{Key: "provider", Type: SettingsFieldSelect, Options: []SettingsOption{{Value: "nanite"}}}, value: " \tnanite\n"},
		{name: "required_blank", field: SettingsField{Key: "endpoint", Type: SettingsFieldString, Required: true}, value: " \t\u00a0", invalid: true},
		{name: "required_false", field: SettingsField{Key: "enabled", Type: SettingsFieldBoolean, Required: true}, value: false},
		{name: "required_zero", field: SettingsField{Key: "count", Type: SettingsFieldNumber, Required: true}, value: 0},
		{name: "string_is_not_boolean", field: SettingsField{Key: "enabled", Type: SettingsFieldBoolean}, value: " true ", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized := NormalizeSettingValue(tc.field, tc.value)
			tc.field.Default = normalized
			caps := PluginCapabilities{Modules: []string{"config"}, Settings: &SettingsDeclaration{Fields: []SettingsField{tc.field}}}
			if err := caps.Validate(); (err != nil) != tc.invalid {
				t.Fatalf("validation: %v (invalid=%v)", err, tc.invalid)
			}
		})
	}
}

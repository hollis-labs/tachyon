package contract

import "strings"

// NormalizeSettingValue applies the declaration's whitespace policy without
// coercing JSON types or mutating the declaration. Writers and startup readers
// can use the same normalization before validating desired values.
func NormalizeSettingValue(field SettingsField, value any) any {
	text, ok := value.(string)
	if !ok || (field.Type != SettingsFieldString && field.Type != SettingsFieldSelect) {
		return value
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !field.PreserveEdgeWhitespace {
		return trimmed
	}
	return text
}

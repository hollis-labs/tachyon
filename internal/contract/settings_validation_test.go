package contract

import (
	"strconv"
	"strings"
	"testing"
)

func TestWriteValidationConstraints(t *testing.T) {
	for _, tc := range []struct {
		rule           SettingsValidation
		valid, invalid []string
	}{
		{SettingsValidationHTTPBaseURL, []string{"", "http://fixture", "https://[::1]:1234/base/"}, []string{"junk", "http://", "http://:12", "http://fixture:0", "http://fixture:70000", "http://user:secret@fixture", "http://fixture?", "http://fixture?key=secret", "http://fixture#", "http://fixture/#secret", "http://fixture/%E2%80%8B", "http://%E2%80%8Bfixture", "http://fixture:", "http://fixture/\n"}},
		{SettingsValidationAbsolutePath, []string{"", "/fixture/socket ", "~/fixture"}, []string{"relative", "~fixture", "/fixture\n"}},
		{SettingsValidationTetherAddr, []string{"", "unix:/fixture", "unix:~/fixture", "tcp:localhost:1234", "tcp:[::1]:1234", "https://fixture/base"}, []string{"HTTP://fixture", "Https://fixture", "junk", "unix:", "unix:relative", "tcp:", "tcp:fixture", "tcp::1234", "tcp:fixture:0", "tcp:fixture:70000", "tcp:fixture/path:1234", "tcp:fixture:+123", "tcp:%E2%80%8Bfixture:1234", "http://user:secret@fixture", "http://fixture?secret"}},
	} {
		t.Run(string(tc.rule), func(t *testing.T) {
			field := SettingsField{Key: "fixture", Type: SettingsFieldString, Validation: tc.rule, PreserveEdgeWhitespace: true}
			for _, value := range tc.valid {
				if err := ValidateSettingWrite(field, value); err != nil {
					t.Fatalf("valid value rejected: %q %v", value, err)
				}
			}
			for _, value := range append(tc.invalid, "\u200b", "\ufeff", "/fixture\u200b", "http://fixture/\ufeff") {
				if err := ValidateSettingWrite(field, value); err == nil {
					t.Fatalf("invalid value accepted: %q", value)
				} else if strings.Contains(err.Error(), strconv.Quote(value)) || strings.Contains(err.Error(), "secret") || strings.ContainsAny(err.Error(), "\u200b\ufeff") {
					t.Fatal("rejection echoed supplied value")
				}
			}
		})
	}
}

func TestSettingsRuleDeclarationAndLegacyAdmission(t *testing.T) {
	for _, field := range []SettingsField{
		{Key: "fixture", Type: SettingsFieldString, Validation: "custom"},
		{Key: "fixture", Type: SettingsFieldBoolean, Validation: SettingsValidationHTTPBaseURL},
		{Key: "fixture", Type: SettingsFieldString, Validation: SettingsValidationSelect},
		{Key: "fixture", Type: SettingsFieldSelect, Validation: SettingsValidationTetherAddr, Options: []SettingsOption{{Value: "legacy"}}},
	} {
		caps := PluginCapabilities{Modules: []string{"config"}, Settings: &SettingsDeclaration{Fields: []SettingsField{field}}}
		if err := caps.Validate(); err == nil {
			t.Fatal("invalid validation metadata accepted")
		}
	}
	field := SettingsField{Key: "provider", Type: SettingsFieldSelect, Validation: SettingsValidationSelect, Options: []SettingsOption{{Value: "tether"}, {Value: " legacy "}}}
	for _, tc := range []struct {
		value, want string
		retained    bool
	}{{" \ttether\n", "tether", false}, {" legacy ", " legacy ", true}} {
		got, retained, err := ResolveSettingValue(field, tc.value)
		if err != nil || got != tc.want || retained != tc.retained {
			t.Fatalf("legacy admission: %v %v %v", got, retained, err)
		}
		if err := ValidateSettingWrite(field, got); err != nil {
			t.Fatal(err)
		}
	}
	// New rules must not enter structural admission, even for an authored or
	// stored value that a plugin already accepts. Writers report the constraint.
	field = SettingsField{Key: "endpoint", Type: SettingsFieldString, Validation: SettingsValidationHTTPBaseURL, Default: "http://user:secret@fixture"}
	if _, _, err := ResolveSettingValue(field, field.Default); err != nil {
		t.Fatal("new startup refusal", err)
	}
	if err := ValidateSettingWrite(field, field.Default); err == nil {
		t.Fatal("write constraint not enforced")
	}
}

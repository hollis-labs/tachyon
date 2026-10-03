package contract

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// ResolveSettingValue shares the host's existing type/required/select admission
// and normalization with configuration writers. A legacy select option that is
// invalid after trimming keeps its formerly accepted spelling. Semantic write
// constraints are deliberately separate so they cannot refuse startup.
func ResolveSettingValue(field SettingsField, value any) (any, bool, error) {
	check := func(value any) error {
		if value == nil {
			return errors.New("value must match the declared type")
		}
		field.Default = value
		caps := PluginCapabilities{Modules: []string{"config"}, Settings: &SettingsDeclaration{Fields: []SettingsField{field}}}
		return caps.Validate()
	}
	normalized := NormalizeSettingValue(field, value)
	if err := check(normalized); err == nil {
		return normalized, false, nil
	}
	if err := check(value); err != nil {
		return nil, false, errors.New("value must satisfy the declared type, required flag and options")
	}
	return value, true, nil
}

func validateSettingsRule(field SettingsField) error {
	switch field.Validation {
	case "":
		return nil
	case SettingsValidationHTTPBaseURL, SettingsValidationAbsolutePath, SettingsValidationTetherAddr:
		if field.Type == SettingsFieldString {
			return nil
		}
	case SettingsValidationSelect:
		if field.Type == SettingsFieldSelect {
			return nil
		}
	default:
		return errors.New("unknown validation rule")
	}
	return errors.New("validation rule is incompatible with field type")
}

// ValidateSettingWrite checks a resolved value without I/O or echoing its bytes.
// Call ResolveSettingValue first; structural admission remains a separate check.
func ValidateSettingWrite(field SettingsField, value any) error {
	if err := validateSettingsRule(field); err != nil {
		return err
	}
	if field.Validation == "" {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return errors.New("value must match the declared type")
	}
	if strings.ContainsAny(text, "\u200b\ufeff") || strings.ContainsFunc(text, unicode.IsControl) {
		return errors.New("value must not contain invisible or control characters")
	}
	if text == "" && !field.Required && field.Type != SettingsFieldSelect {
		return nil
	}
	switch field.Validation {
	case SettingsValidationHTTPBaseURL:
		if !validHTTPBase(text) {
			return errors.New("value must be an HTTP(S) base URL without credentials, query or fragment")
		}
	case SettingsValidationAbsolutePath:
		if !validAbsolutePath(text) {
			return errors.New("value must be an absolute path or start with ~/")
		}
	case SettingsValidationTetherAddr:
		valid := false
		switch {
		case strings.HasPrefix(text, "unix:"):
			valid = validAbsolutePath(strings.TrimPrefix(text, "unix:"))
		case strings.HasPrefix(text, "tcp:"):
			host, port, err := net.SplitHostPort(strings.TrimPrefix(text, "tcp:"))
			valid = err == nil && host != "" && !strings.ContainsAny(host, " /?#@") && validPort(port) && validHTTPBase("http://"+strings.TrimPrefix(text, "tcp:"))
		default:
			valid = (strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://")) && validHTTPBase(text)
		}
		if !valid {
			return errors.New("value must use unix:/path, unix:~/path, tcp:host:port or an HTTP(S) base URL")
		}
	}
	return nil
}

func validAbsolutePath(text string) bool {
	return filepath.IsAbs(text) || strings.HasPrefix(text, "~/")
}

func validPort(text string) bool {
	if text == "" || strings.ContainsFunc(text, func(r rune) bool { return r < '0' || r > '9' }) {
		return false
	}
	port, err := strconv.Atoi(text)
	return err == nil && port > 0 && port <= 65535
}

func validHTTPBase(text string) bool {
	u, err := url.Parse(text)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), "\u200b\ufeff") || strings.ContainsFunc(u.Hostname(), unicode.IsSpace) || strings.HasSuffix(u.Host, ":") || u.User != nil || u.ForceQuery || u.RawQuery != "" || strings.Contains(text, "#") {
		return false
	}
	if strings.ContainsAny(u.Path, "\u200b\ufeff") || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return false
	}
	return u.Port() == "" || validPort(u.Port())
}

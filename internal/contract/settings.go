package contract

import "time"

// SettingsFieldType is the data type of a plugin settings field.
type SettingsFieldType string

const (
	SettingsFieldString  SettingsFieldType = "string"
	SettingsFieldBoolean SettingsFieldType = "boolean"
	SettingsFieldNumber  SettingsFieldType = "number"
	SettingsFieldSelect  SettingsFieldType = "select"
)

// SettingsValidation identifies a pure write-time constraint. Startup readers may
// warn about these constraints, but must retain the existing Init admission rules.
type SettingsValidation string

const (
	SettingsValidationHTTPBaseURL  SettingsValidation = "http_base_url"
	SettingsValidationAbsolutePath SettingsValidation = "absolute_path"
	SettingsValidationTetherAddr   SettingsValidation = "tether_addr"
	SettingsValidationSelect       SettingsValidation = "select"
)

// SettingsField declares a single configurable field exposed by a
// plugin. The host aggregates these into a merged settings schema
// served to the frontend's Settings page for dynamic form rendering.
type SettingsField struct {
	// Key is the unique field identifier within this plugin's settings
	// namespace. Scoped by plugin ID, so collisions across plugins
	// are impossible.
	Key string `json:"key"`

	// Type determines the input control rendered in the Settings UI.
	Type SettingsFieldType `json:"type"`

	// Label is the human-readable field label.
	Label string `json:"label"`

	// Description provides context shown as help text.
	Description string `json:"description,omitempty"`

	// Default is the default value if the user hasn't set one.
	Default any `json:"default,omitempty"`

	// Required marks the field as mandatory. A missing/null default means the
	// operator must supply a value. Provided defaults must match Type; required
	// strings cannot be blank. False and zero remain valid defaults.
	Required bool `json:"required,omitempty"`

	// PreserveEdgeWhitespace keeps nonblank string/select values verbatim when
	// edge spaces are meaningful (for example, a filesystem path or opaque ID).
	// Whitespace-only values always normalize to empty, even with this opt-out.
	// Other string/select values are trimmed before they reach plugin Init.
	PreserveEdgeWhitespace bool `json:"preserve_edge_whitespace,omitempty"`

	// Validation adds a closed-enum semantic rule for configuration writes.
	// Optional empty strings retain fallback semantics. Constrained values reject
	// U+200B and U+FEFF; errors never include the supplied value.
	Validation SettingsValidation `json:"validation,omitempty"`

	// Options lists allowed values when Type is "select".
	Options []SettingsOption `json:"options,omitempty"`
}

// SettingsOption is one choice in a select-type settings field.
type SettingsOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SettingsDeclaration is the settings contribution a plugin includes
// in its capability declaration. The host reads it at load time and
// exposes it through the config module's API.
type SettingsDeclaration struct {
	Fields []SettingsField `json:"fields,omitempty"`
}

// SettingsTarget binds a plugin-owned settings schema to its stable identity.
type SettingsTarget struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Settings SettingsDeclaration `json:"settings"`
	// Retirement metadata is additive; absent fields mean a loaded target.
	State     string     `json:"state,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

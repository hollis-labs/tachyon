package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var verbIDPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// VerbDeclaration describes a single verb's effect classification,
// declared by a plugin at registration time.
type VerbDeclaration struct {
	Effect Effect `json:"effect"`
}

// PluginCapabilities is the capability declaration a plugin includes in
// its plugin_capabilities command response (D-47, D-48). The host validates it at load
// time. Non-nav violations are hard load errors; navigation is normalized
// separately with structured warn-and-drop diagnostics.
type PluginCapabilities struct {
	navSchemaMalformed json.RawMessage
	// Modules lists the module namespaces this plugin claims. A module
	// is a verb namespace representing a coherent capability domain.
	// Only one plugin may claim a given module (D-49: collision = fatal).
	Modules []string `json:"modules"`

	// Verbs maps verb IDs to their declarations. Every verb ID must
	// follow the <module>_<verb> pattern (D-49) and be prefixed with
	// one of this plugin's declared modules.
	Verbs map[string]VerbDeclaration `json:"verbs"`

	NavSchema int `json:"nav_schema,omitempty"`

	Nav      *NavDeclaration      `json:"nav,omitempty"`
	Settings *SettingsDeclaration `json:"settings,omitempty"`
}

// Validate checks the capability declaration for internal consistency.
// It does NOT check cross-plugin module collisions — that is the host
// manager's responsibility.
func (pc *PluginCapabilities) Validate() error {
	if len(pc.Modules) == 0 {
		return fmt.Errorf("capabilities: at least one module must be declared")
	}

	moduleSet := make(map[string]bool, len(pc.Modules))
	for _, mod := range pc.Modules {
		if !verbIDPattern.MatchString(mod) {
			return fmt.Errorf("capabilities: module name %q does not match %s", mod, verbIDPattern)
		}
		if moduleSet[mod] {
			return fmt.Errorf("capabilities: duplicate module %q", mod)
		}
		moduleSet[mod] = true
	}

	for verb, decl := range pc.Verbs {
		if !verbIDPattern.MatchString(verb) {
			return fmt.Errorf("capabilities: verb %q does not match %s", verb, verbIDPattern)
		}

		// Every verb must be prefixed with one of the declared modules (D-49).
		owned := false
		for _, mod := range pc.Modules {
			if strings.HasPrefix(verb, mod+"_") {
				owned = true
				break
			}
		}
		if !owned {
			return fmt.Errorf("capabilities: verb %q is not prefixed with any declared module %v", verb, pc.Modules)
		}

		if !decl.Effect.Valid() {
			return fmt.Errorf("capabilities: verb %q declares invalid effect %q", verb, decl.Effect)
		}
	}

	return pc.validateSettings()
}

// ModuleForVerb returns the owning module for a verb ID, or empty string
// if the verb doesn't match any declared module.
func (pc *PluginCapabilities) ModuleForVerb(verb string) string {
	for _, mod := range pc.Modules {
		if strings.HasPrefix(verb, mod+"_") {
			return mod
		}
	}
	return ""
}

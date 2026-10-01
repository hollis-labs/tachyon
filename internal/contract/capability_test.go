package contract

import (
	"strings"
	"testing"
)

func TestPluginCapabilities_Validate(t *testing.T) {
	t.Run("Valid declaration passes", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"mod1"},
			Verbs: map[string]VerbDeclaration{
				"mod1_read":    {Effect: EffectReads},
				"mod1_write":   {Effect: EffectWrites},
				"mod1_destroy": {Effect: EffectDestroys},
			},
		}
		if err := pc.Validate(); err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})

	t.Run("Empty modules list fails", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{},
			Verbs:   map[string]VerbDeclaration{},
		}
		if err := pc.Validate(); err == nil {
			t.Errorf("expected error for empty modules list")
		}
	})

	t.Run("Invalid module name fails", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"Mod1!"},
		}
		if err := pc.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Errorf("expected regex match error, got %v", err)
		}
	})

	t.Run("Duplicate module fails", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"mod1", "mod1"},
		}
		if err := pc.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate module") {
			t.Errorf("expected duplicate module error, got %v", err)
		}
	})

	t.Run("Verb without matching module prefix fails", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"mod1"},
			Verbs: map[string]VerbDeclaration{
				"mod2_read": {Effect: EffectReads},
			},
		}
		if err := pc.Validate(); err == nil || !strings.Contains(err.Error(), "is not prefixed with any declared module") {
			t.Errorf("expected unowned verb error, got %v", err)
		}
	})

	t.Run("Verb with invalid regex fails", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"mod1"},
			Verbs: map[string]VerbDeclaration{
				"mod1_Read!": {Effect: EffectReads},
			},
		}
		if err := pc.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Errorf("expected verb regex error, got %v", err)
		}
	})

	t.Run("Verb with invalid effect fails", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"mod1"},
			Verbs: map[string]VerbDeclaration{
				"mod1_read": {Effect: "invalid_effect"},
			},
		}
		if err := pc.Validate(); err == nil || !strings.Contains(err.Error(), "invalid effect") {
			t.Errorf("expected invalid effect error, got %v", err)
		}
	})

	t.Run("Multiple modules work correctly", func(t *testing.T) {
		pc := &PluginCapabilities{
			Modules: []string{"mod1", "mod2"},
			Verbs: map[string]VerbDeclaration{
				"mod1_action": {Effect: EffectReads},
				"mod2_action": {Effect: EffectReads},
			},
		}
		if err := pc.Validate(); err != nil {
			t.Errorf("expected nil error for valid multiple modules, got %v", err)
		}
	})
}

func TestModuleForVerb(t *testing.T) {
	pc := &PluginCapabilities{
		Modules: []string{"mod1", "mod2"},
	}

	t.Run("Returns correct module for matching verb", func(t *testing.T) {
		if mod := pc.ModuleForVerb("mod1_action"); mod != "mod1" {
			t.Errorf("expected mod1, got %q", mod)
		}
		if mod := pc.ModuleForVerb("mod2_action"); mod != "mod2" {
			t.Errorf("expected mod2, got %q", mod)
		}
	})

	t.Run("Returns empty string for non-matching verb", func(t *testing.T) {
		if mod := pc.ModuleForVerb("mod3_action"); mod != "" {
			t.Errorf("expected empty string, got %q", mod)
		}
	})
}

package main

import (
	"context"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// ConfigAdapter manages schema-backed overrides, not provider execution state.
type ConfigAdapter interface {
	SetSchemas([]ConfigTarget) error
	List(context.Context) ([]ConfigTarget, error)
	Read(context.Context, string) (TargetConfig, error)
	Update(context.Context, string, map[string]any) (TargetConfig, error)
	Reset(context.Context, string) (TargetConfig, error)
	Validate(context.Context, string, map[string]any) (ValidationResult, error)
}

type ConfigTarget = contract.SettingsTarget

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors map[string]string `json:"errors,omitempty"`
}

type TargetConfig struct {
	Target     ConfigTarget     `json:"target"`
	Values     map[string]any   `json:"values"`
	Validation ValidationResult `json:"validation"`
}

package contract

import (
	"testing"
)

func TestEffect_Valid(t *testing.T) {
	tests := []struct {
		effect   Effect
		expected bool
	}{
		{EffectReads, true},
		{EffectWrites, true},
		{EffectDestroys, true},
		{EffectOpenWorld, true},
		{Effect(""), false},
		{Effect("executes"), false},
	}

	for _, tt := range tests {
		if got := tt.effect.Valid(); got != tt.expected {
			t.Errorf("Effect(%q).Valid() = %v, expected %v", tt.effect, got, tt.expected)
		}
	}
}

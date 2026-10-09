package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

func TestInitWhitespaceOnlyRootInputs(t *testing.T) {
	const blank = " \t\n "
	for _, tc := range []struct {
		name, config, env string
		useEnvFixture     bool
	}{
		{name: "config_falls_through_to_env", config: blank, useEnvFixture: true},
		{name: "blank_env_is_unset", env: blank},
		{name: "both_blank_are_unset", config: blank, env: blank},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			env := tc.env
			if tc.useEnvFixture {
				env = t.TempDir()
			}
			t.Setenv("TACHYON_SCM_REPOS_ROOT", env)
			p := &plugin{}
			if _, err := p.Init(context.Background(), subprocess.InitParams{Config: map[string]string{"repos_root": tc.config}}); err != nil {
				t.Fatal(err)
			}
			got := p.adapter.(*GitAdapter).root
			blankPath, err := filepath.Abs(blank)
			if err != nil {
				t.Fatal(err)
			}
			if got == blank || got == blankPath {
				t.Fatalf("whitespace-only input resolved as a filesystem path: %q", got)
			}
			if tc.useEnvFixture && got != env {
				t.Fatalf("root = %q, want environment fixture %q", got, env)
			}
		})
	}
}

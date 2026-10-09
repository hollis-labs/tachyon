package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestCommandVerbWire(t *testing.T) {
	a, repo := fixture(t)
	commitFixture(t, repo)
	ctx := context.Background()
	p := &plugin{}
	if _, err := p.Init(ctx, subprocess.InitParams{Config: map[string]string{"repos_root": a.root}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(ctx); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"scm_list", "scm_read", "scm_activity", "scm_status", "scm_diff"} {
		result, err := p.Command(ctx, subprocess.CommandRequest{Name: verb, Args: `{"id":"repo with spaces"}`})
		if err != nil {
			t.Fatal(err)
		}
		var envelope contract.ResultEnvelope
		if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Status != contract.StatusOK {
			t.Fatalf("%s: %s", verb, result.Content)
		}
	}
	result, err := p.Command(ctx, subprocess.CommandRequest{Name: "plugin_capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	var caps contract.PluginCapabilities
	if err := json.Unmarshal([]byte(result.Content), &caps); err != nil {
		t.Fatal(err)
	}
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	for verb, decl := range caps.Verbs {
		if decl.Effect != contract.EffectReads {
			t.Errorf("%s effect: %s", verb, decl.Effect)
		}
	}
}

func TestVerbErrors(t *testing.T) {
	a, _ := fixture(t)
	p := &plugin{adapter: a}
	for _, tc := range []struct{ verb, payload, code string }{
		{"scm_status", `{`, "validation"},
		{"scm_read", `{}`, "validation"},
		{"scm_status", `{"id":"../escape"}`, "validation"},
		{"scm_status", `{"id":"missing"}`, "not_found"},
		{"scm_activity", `{"id":"repo with spaces","limit":-1}`, "validation"},
		{"scm_create", `{}`, "unknown_verb"},
	} {
		env, err := p.HandleVerb(context.Background(), tc.verb, json.RawMessage(tc.payload))
		if err != nil {
			t.Fatal(err)
		}
		if env.Status != contract.StatusError || env.Error == nil || env.Error.Code != tc.code {
			t.Fatalf("%s %s: %+v", tc.verb, tc.payload, env)
		}
	}
}

func TestInitRootEnvironment(t *testing.T) {
	a, _ := fixture(t)
	t.Setenv("TACHYON_SCM_REPOS_ROOT", a.root)
	p := &plugin{}
	if _, err := p.Init(context.Background(), subprocess.InitParams{}); err != nil {
		t.Fatal(err)
	}
	if got := p.adapter.(*GitAdapter).root; got != a.root {
		t.Fatalf("root = %s", got)
	}
}

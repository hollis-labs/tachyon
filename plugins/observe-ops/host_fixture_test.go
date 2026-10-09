package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// A disposable pipe-only plugin. Its writes never contact a provider.
type hostFeedFixture struct{}

func (*hostFeedFixture) Init(context.Context, subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{ID: "fixture-work", Name: "Fake Work", Version: "test", Protocol: subprocess.ProtocolVersion, CapabilityContract: 1}, nil
}
func (*hostFeedFixture) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (*hostFeedFixture) Unload(context.Context) error { return nil }
func (*hostFeedFixture) Command(_ context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	var value any
	if req.Name == "plugin_capabilities" {
		value = contract.PluginCapabilities{Modules: []string{"work"}, Verbs: map[string]contract.VerbDeclaration{"work_read": {Effect: contract.EffectReads}, "work_write": {Effect: contract.EffectWrites}, "work_ask": {Effect: contract.EffectOpenWorld}, "work_error": {Effect: contract.EffectDestroys}}}
	} else {
		status := "ok"
		if req.Name == "work_ask" {
			status = "ask"
		}
		if req.Name == "work_error" {
			status = "error"
		}
		value = map[string]any{"status": status, "data": map[string]string{"value": "OUTPUT-SECRET"}, "ask": map[string]string{"prompt": "PROMPT-SECRET"}, "error": map[string]string{"message": "UPSTREAM-SECRET", "detail": "CREDENTIAL-SECRET"}}
	}
	raw, err := json.Marshal(value)
	return subprocess.CommandResult{Action: "message", Content: string(raw)}, err
}
func TestHostFeedFakeWorker(t *testing.T) {
	if os.Getenv("TACHYON_OBSERVE_FAKE_WORKER") != "1" {
		return
	}
	subprocess.Serve(&hostFeedFixture{})
	os.Exit(0)
}

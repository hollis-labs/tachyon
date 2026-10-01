package pluginkit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

type stubPlugin struct {
	payload  json.RawMessage
	err      error
	envelope contract.ResultEnvelope
}

func (*stubPlugin) Capabilities() contract.PluginCapabilities {
	return contract.PluginCapabilities{Modules: []string{"test"}, Verbs: map[string]contract.VerbDeclaration{"test_read": {Effect: contract.EffectReads}}}
}
func (p *stubPlugin) HandleVerb(_ context.Context, _ string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	p.payload = payload
	return p.envelope, p.err
}
func TestDispatch(t *testing.T) {
	p := &stubPlugin{envelope: contract.Ask("continue?", "yes")}
	result, handled, err := Dispatch(context.Background(), p, subprocess.CommandRequest{Name: CommandCapabilities})
	if !handled || err != nil || result.Action != "message" {
		t.Fatalf("discovery: %+v %v %v", result, handled, err)
	}
	var caps contract.PluginCapabilities
	if err := json.Unmarshal([]byte(result.Content), &caps); err != nil || caps.Validate() != nil {
		t.Fatalf("invalid declaration: %s", result.Content)
	}
	for _, args := range []string{"", `{"id":"abc"}`} {
		result, handled, err = Dispatch(context.Background(), p, subprocess.CommandRequest{Name: "test_read", Args: args})
		if !handled || err != nil || string(p.payload) != args {
			t.Fatalf("verb dispatch: %+v %v %v payload=%s", result, handled, err, p.payload)
		}
		if args == "" && p.payload != nil {
			t.Fatal("empty args must be nil payload")
		}
		var env contract.ResultEnvelope
		if err := json.Unmarshal([]byte(result.Content), &env); err != nil || env.Status != contract.StatusAsk || env.Ask.Prompt != "continue?" {
			t.Fatalf("lost envelope: %s", result.Content)
		}
	}
	_, handled, err = Dispatch(context.Background(), p, subprocess.CommandRequest{Name: "legacy"})
	if handled || err != nil {
		t.Fatal("legacy command was intercepted")
	}
	p.err = errors.New("adapter failed")
	_, handled, err = Dispatch(context.Background(), p, subprocess.CommandRequest{Name: "test_read"})
	if !handled || !errors.Is(err, p.err) {
		t.Fatal("handler error not propagated")
	}
	p.err = nil
	p.envelope = contract.ResultEnvelope{Status: contract.StatusOK, Data: json.RawMessage(`invalid`)}
	_, handled, err = Dispatch(context.Background(), p, subprocess.CommandRequest{Name: "test_read"})
	if !handled || err == nil {
		t.Fatal("encoding error not propagated")
	}
}

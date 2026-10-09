package plugins

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// fakeProcess uses real JSON-RPC framing through pipes without spawning binaries.
func fakeProcess(t *testing.T, id, mode string, declarations ...contract.PluginCapabilities) *pluginProcess {
	t.Helper()
	requests, input := io.Pipe()
	output, responses := io.Pipe()
	t.Cleanup(func() { input.Close(); output.Close() })
	go func() {
		defer requests.Close()
		defer responses.Close()
		dec := json.NewDecoder(requests)
		enc := json.NewEncoder(responses)
		loaded := false
		for {
			var req struct {
				ID     subprocess.RPCID `json:"id"`
				Method string           `json:"method"`
				Params json.RawMessage  `json:"params"`
			}
			if dec.Decode(&req) != nil {
				return
			}
			resp := subprocess.RPCResponse{ID: req.ID, JSONRPC: "2.0"}
			switch req.Method {
			case "plugin/load":
				loaded = true
				resp.Result = json.RawMessage(`{}`)
			case "command/execute":
				var params subprocess.CommandExecParams
				json.Unmarshal(req.Params, &params)
				if !loaded {
					resp.Error = &subprocess.RPCError{Code: -32000, Message: "not loaded"}
				} else if params.Name == "plugin_capabilities" {
					if mode == "legacy" {
						resp.Error = &subprocess.RPCError{Code: -32601, Message: "method not found"}
					} else if mode == "error" {
						resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "error", Content: "unknown command"})
					} else {
						effect := contract.EffectReads
						if mode == "bad" {
							effect = "invalid"
						}
						caps := contract.PluginCapabilities{Modules: []string{"agent"}, Verbs: map[string]contract.VerbDeclaration{"agent_list": {Effect: effect}}}
						if len(declarations) > 0 {
							caps = declarations[0]
						}
						content, _ := json.Marshal(caps)
						resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "message", Content: string(content)})
					}
				} else {
					env, _ := contract.OK(map[string]string{"payload": params.Args, "verb": params.Name})
					content, _ := json.Marshal(env)
					resp.Result, _ = json.Marshal(subprocess.CommandExecResult{Action: "message", Content: string(content)})
				}
			}
			if enc.Encode(resp) != nil {
				return
			}
		}
	}()
	return &pluginProcess{id: id, stdin: input, stdout: output, stdoutDec: json.NewDecoder(output)}
}

func TestCapabilityCarrier(t *testing.T) {
	for _, mode := range []string{"declared", "legacy", "error", "bad"} {
		t.Run(mode, func(t *testing.T) {
			m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
			err := m.initializePlugin(context.Background(), fakeProcess(t, "test", mode))
			if mode == "bad" {
				if err == nil || !strings.Contains(err.Error(), "validation failed") || len(m.plugins) != 0 {
					t.Fatalf("invalid declaration admitted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode != "declared" {
				if m.ModuleOwner("agent") != "" {
					t.Fatal("legacy plugin claimed module")
				}
				return
			}
			if m.ModuleOwner("agent") != "test" {
				t.Fatal("missing module")
			}
			raw, err := m.InvokeVerb(context.Background(), "agent_list", json.RawMessage(`{"filter":"active"}`))
			if err != nil {
				t.Fatal(err)
			}
			var env contract.ResultEnvelope
			if err := json.Unmarshal(raw, &env); err != nil || env.Status != contract.StatusOK {
				t.Fatalf("not an unwrapped envelope: %s", raw)
			}
			var data map[string]string
			json.Unmarshal(env.Data, &data)
			if data["payload"] != `{"filter":"active"}` || data["verb"] != "agent_list" {
				t.Fatalf("wrong carrier: %s", raw)
			}
			if _, err := m.InvokeVerb(context.Background(), "agent_unknown", nil); err == nil {
				t.Fatal("undeclared verb dispatched")
			}
			err = m.initializePlugin(context.Background(), fakeProcess(t, "other", "declared"))
			if err == nil || !strings.Contains(err.Error(), "already owned") || m.ModuleOwner("agent") != "test" || len(m.plugins) != 1 {
				t.Fatalf("collision admitted: %v", err)
			}
		})
	}
}

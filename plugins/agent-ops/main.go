// Agent Ops is a subprocess plugin for Tachyon that provides agent
// operational capabilities including session management, task tracking,
// and agent lifecycle operations.
//
// This plugin uses an adapter-based architecture to support multiple
// agent frameworks. The AgentAdapter interface defines provider-agnostic
// operations (list/get/create/update/delete/launch), and concrete adapters
// implement these operations for specific frameworks.
//
// The first concrete adapter is NaniteAdapter, which calls Nanite's HTTP
// API directly at http://localhost:8090 (the nanite-api-service daemon).
// Future adapters can plug in for other agent frameworks.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

type plugin struct {
	adapter AgentAdapter
}

// HandleCall implements custom RPC method handling for agent operations.
func (p *plugin) HandleCall(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
	switch method {
	case "agent-ops/list":
		return p.adapter.ListAgents(ctx)

	case "agent-ops/get":
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return p.adapter.GetAgent(ctx, req.ID)

	case "agent-ops/create":
		var req CreateAgentRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return p.adapter.CreateAgent(ctx, req)

	case "agent-ops/update":
		var req struct {
			ID     string              `json:"id"`
			Update UpdateAgentRequest `json:"update"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return p.adapter.UpdateAgent(ctx, req.ID, req.Update)

	case "agent-ops/delete":
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return nil, p.adapter.DeleteAgent(ctx, req.ID)

	case "agent-ops/create-session":
		var req CreateSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		sessionID, err := p.adapter.CreateSession(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]string{"session_id": sessionID}, nil

	case "agent-ops/launch-session":
		var req struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return p.adapter.LaunchSession(ctx, req.SessionID)

	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func (p *plugin) Init(ctx context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	// Default Nanite API URL - can be overridden via config
	naniteURL := "http://localhost:8090"
	if url, ok := params.Config["nanite_url"]; ok {
		naniteURL = url
	}

	// Initialize the Nanite adapter (first concrete implementation)
	p.adapter = NewNaniteAdapter(naniteURL)

	return subprocess.InitResult{
		ID:          "agent-ops",
		Name:        "Agent Ops",
		Version:     "0.1.0",
		Description: "Agent operational capabilities for Tachyon (adapter-based, Nanite HTTP API)",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *plugin) Load(ctx context.Context) (subprocess.LoadResult, error) {
	// Plugin is ready - adapter initialized during Init
	return subprocess.LoadResult{}, nil
}

func (p *plugin) Unload(ctx context.Context) error {
	// Clean shutdown - adapter cleanup if needed
	return nil
}

func main() {
	p := &plugin{}

	// Custom RPC server loop that handles both lifecycle and custom methods
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		var req subprocess.RPCRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		var resp subprocess.RPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "plugin/init":
			var params subprocess.InitParams
			if err := json.Unmarshal(req.Params.(json.RawMessage), &params); err != nil {
				resp.Error = &subprocess.RPCError{Code: -32602, Message: err.Error()}
			} else {
				result, err := p.Init(context.Background(), params)
				if err != nil {
					resp.Error = &subprocess.RPCError{Code: -32000, Message: err.Error()}
				} else {
					resultBytes, _ := json.Marshal(result)
					resp.Result = resultBytes
				}
			}

		case "plugin/load":
			result, err := p.Load(context.Background())
			if err != nil {
				resp.Error = &subprocess.RPCError{Code: -32000, Message: err.Error()}
			} else {
				resultBytes, _ := json.Marshal(result)
				resp.Result = resultBytes
			}

		case "plugin/unload":
			err := p.Unload(context.Background())
			if err != nil {
				resp.Error = &subprocess.RPCError{Code: -32000, Message: err.Error()}
			} else {
				resp.Result = json.RawMessage("{}")
			}

		default:
			// Handle custom methods
			result, err := p.HandleCall(context.Background(), req.Method, req.Params.(json.RawMessage))
			if err != nil {
				resp.Error = &subprocess.RPCError{Code: -32000, Message: err.Error()}
			} else {
				resultBytes, _ := json.Marshal(result)
				resp.Result = resultBytes
			}
		}

		if err := encoder.Encode(resp); err != nil {
			break
		}
	}
}

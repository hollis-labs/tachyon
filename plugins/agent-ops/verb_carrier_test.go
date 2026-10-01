package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

type listStub struct{ AgentAdapter }

func (listStub) ListAgents(context.Context) ([]Agent, error) { return []Agent{{ID: "stub-agent"}}, nil }
func TestAgentListCommandCarrier(t *testing.T) {
	p := &plugin{adapter: listStub{}}
	caps := p.Capabilities()
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	result, err := p.Command(context.Background(), subprocess.CommandRequest{Name: "agent_list"})
	if err != nil {
		t.Fatal(err)
	}
	var env contract.ResultEnvelope
	if err := json.Unmarshal([]byte(result.Content), &env); err != nil || env.Status != contract.StatusOK {
		t.Fatalf("invalid envelope: %s", result.Content)
	}
	var agents []Agent
	if err := json.Unmarshal(env.Data, &agents); err != nil || len(agents) != 1 || agents[0].ID != "stub-agent" {
		t.Fatalf("unexpected agents: %s", env.Data)
	}
	if _, err := p.Command(context.Background(), subprocess.CommandRequest{Name: "unknown"}); err == nil {
		t.Fatal("legacy fallback lost")
	}
}

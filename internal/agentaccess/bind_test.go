package agentaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestBindAndIssueAgent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var out bytes.Buffer
	tok := filepath.Join(dir, "tok")
	if err := Command(ctx, dir, []string{"issue", "--name", "task-ops-chat-plan", "--preset", "chat-plan", "--token-file", tok}, &out); err != nil {
		t.Fatal(err)
	}
	var issued Token
	if err := json.Unmarshal(out.Bytes(), &issued); err != nil || len(issued.Agents) != 0 {
		t.Fatal(out.String(), err)
	}
	secret, _ := os.ReadFile(tok)
	scopesOf := func() []string {
		db := openStore(t, dir)
		defer db.DB.Close()
		p, err := db.Verify(ctx, string(bytes.TrimSpace(secret)))
		if err != nil {
			t.Fatal(err)
		}
		return p.Scopes
	}
	if slices.Contains(scopesOf(), "agent:taskops-manager") {
		t.Fatal("unbound credential carries an agent scope")
	}
	// Bind the already-issued identity: no reissue, the existing token picks it up.
	out.Reset()
	if err := Command(ctx, dir, []string{"bind", "--identity", issued.IdentityID, "--agent", "msg://agent/agent-mux/taskops-manager,agt_n5yntua30e"}, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(scopesOf(), "agent:taskops-manager") || !slices.Contains(scopesOf(), "agent:agt_n5yntua30e") {
		t.Fatalf("binding not applied to the existing token: %v", scopesOf())
	}
	// Idempotent, and unbind removes.
	if err := Command(ctx, dir, []string{"bind", "--identity", issued.IdentityID, "--agent", "taskops-manager"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := Command(ctx, dir, []string{"unbind", "--identity", issued.IdentityID, "--agent", "agt_n5yntua30e"}, &out); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(scopesOf(), "agent:agt_n5yntua30e") {
		t.Fatal("unbind left the scope")
	}
	// Unknown identity, missing flags.
	if err := Command(ctx, dir, []string{"bind", "--identity", "5b0d3a3e-1111-4111-8111-111111111111", "--agent", "x"}, &out); err == nil {
		t.Fatal("bound a missing identity")
	}
	if err := Command(ctx, dir, []string{"bind", "--identity", issued.IdentityID}, &out); err == nil {
		t.Fatal("bind without --agent accepted")
	}
	// Issue with --agent binds in one step.
	out.Reset()
	if err := Command(ctx, dir, []string{"issue", "--name", "n2", "--preset", "chat-plan", "--agent", "tether://agent/foo", "--token-file", filepath.Join(dir, "tok2")}, &out); err != nil {
		t.Fatal(err)
	}
	var second Token
	if err := json.Unmarshal(out.Bytes(), &second); err != nil || !slices.Equal(second.Agents, []string{"foo"}) {
		t.Fatal(out.String(), err)
	}
}

func TestAgentKey(t *testing.T) {
	for in, want := range map[string]string{"msg://agent/agent-mux/taskops-manager": "taskops-manager", "tether://agent/X": "x", "tachyon://agent/app-taskops-manager": "app-taskops-manager", " agt_1 ": "agt_1", "": ""} {
		if got := AgentKey(in); got != want {
			t.Errorf("%q: %q != %q", in, got, want)
		}
	}
}

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(dir, "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	return &Store{DB: db}
}

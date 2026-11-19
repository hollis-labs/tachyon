package dataverbs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/terminal"
)

// The terminal verbs on the agent API (slice B12), as the old build served them. Off (no service) they are
// not listed. On: terminal.machines lists the policy's machines; terminal.propose takes an app thread
// only, refuses a command the policy denies and otherwise waits for the operator, recording the agent's
// service identity as the requester; terminal.run.get shows a run only to the identity that proposed it.
func TestTerminalVerbs(t *testing.T) {
	f := newChatFix(t)
	verbs := []string{"terminal.machines", "terminal.propose", "terminal.run.get"}
	p, id := f.keyFor(t, "runner", verbs...)
	for _, v := range verbs {
		f.refused(t, p, v, map[string]any{}) // the fixture's chat has no terminal: unlisted, as on an old host without one
	}

	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"machines":[{"id":"hub","label":"Hub","host":"hub-host"}],"rules":[{"id":"no-rm","machine":"*","prefix":["rm"],"decision":"deny"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &terminal.Config{PolicyFile: pol}
	st := &terminal.Store{DB: f.db}
	svc := &terminal.Service{Store: st, Config: cfg, Mgr: &terminal.Manager{Store: st, Config: cfg, Base: context.Background()},
		Exists: func(_ context.Context, ref string) error { return chatThreadExists(context.Background(), ref) }}
	f.h = NewAgentHTTP(f.local, f.acc, Chat{Terminal: &ChatTerminal{Service: svc}})

	if code, out := f.do(t, p, "terminal.machines", map[string]any{}, nil); code != 200 || !strings.Contains(fmt.Sprint(out["machines"]), "machine_id:hub") {
		t.Fatalf("machines: %d %v", code, out)
	}
	if code, out := f.do(t, p, "terminal.propose", map[string]any{"thread_ref": "tether:s1", "machine_id": "hub", "command": "uptime"}, nil); code != 400 {
		t.Errorf("a Tether thread: %d %v", code, out)
	}
	if code, out := f.do(t, p, "terminal.propose", map[string]any{"thread_ref": "app:t1", "machine_id": "hub", "command": "rm -rf /"}, nil); code != 403 {
		t.Errorf("a denied command: %d %v", code, out)
	}
	if code, out := f.do(t, p, "terminal.propose", map[string]any{"thread_ref": "app:t1", "machine_id": "elsewhere", "command": "uptime"}, nil); code != 400 {
		t.Errorf("an unlisted machine: %d %v", code, out)
	}
	code, out := f.do(t, p, "terminal.propose", map[string]any{"thread_ref": "app:t1", "machine_id": "hub", "command": "uptime", "reason": "load"}, nil)
	run, _ := out["run_id"].(string)
	if code != 200 || out["state"] != terminal.StatePending || out["approval_mode"] != "user" || run == "" {
		t.Fatalf("propose: %d %v", code, out)
	}
	if r, err := st.Run(context.Background(), run); err != nil || r.RequestedBy != id {
		t.Fatalf("requested_by %q, want the identity %q (%v)", r.RequestedBy, id, err)
	}
	if code, out := f.do(t, p, "terminal.run.get", map[string]any{"run_id": run}, nil); code != 200 || out["state"] != terminal.StatePending || out["command"] != "uptime" {
		t.Errorf("run.get by the proposer: %d %v", code, out)
	}
	other, _ := f.keyFor(t, "other", "terminal.run.get")
	if code, out := f.do(t, other, "terminal.run.get", map[string]any{"run_id": run}, nil); code != 404 {
		t.Errorf("run.get by another identity: %d %v", code, out)
	}
}

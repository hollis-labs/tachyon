package plugins

import (
	"context"
	"github.com/hollis-labs/tachyon/internal/contract"
	"testing"
)

func TestInvocationIdentityCannotFollowReplacement(t *testing.T) {
	m := watchdogManager()
	old := fakeProcess(t, "agent-ops", "declared")
	m.plugins[old.id] = old
	m.modules["agent"] = old.id
	if _, e := callProcess(context.Background(), old, "plugin/load", nil); e != nil {
		t.Fatal(e)
	}
	raw, id, e := m.InvokeVerbCaptured(context.Background(), "agent_list", nil)
	if e != nil || len(raw) == 0 || !m.IdentityCurrent(id) {
		t.Fatal(string(raw), e)
	}
	replacement := fakeProcess(t, old.id, "declared")
	m.mu.Lock()
	m.plugins[old.id] = replacement
	m.mu.Unlock()
	if m.IdentityCurrent(id) {
		t.Fatal("old result adopted replacement identity")
	}
}

func TestCapturedInvocationUnknownCompletionNeverRetries(t *testing.T) {
	m := watchdogManager()
	proc, _, writes := stalledProcess(t, "decode")
	registerStalled(m, proc)
	proc.capabilities.Verbs["hung_write"] = contract.VerbDeclaration{Effect: contract.EffectWrites}
	if _, _, err := m.InvokeVerbCaptured(context.Background(), "hung_write", nil); err == nil {
		t.Fatal("hung write succeeded")
	}
	if writes.Load() != 1 {
		t.Fatal("write replayed", writes.Load())
	}
	if _, _, err := m.InvokeVerbCaptured(context.Background(), "hung_write", nil); err == nil {
		t.Fatal("dead process accepted write")
	}
	if writes.Load() != 1 {
		t.Fatal("unknown write replayed")
	}
}

package builderchange

import (
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

func TestApplyRetireAgent(t *testing.T) {
	live := "id: a\nname: a\nenabled: true\n"
	edits := []Edit{
		{Type: builderapi.OpSet, Path: "runtime.enabled", Value: []byte("false")},
		{Type: builderapi.OpSet, Path: "runtime.lifecycle_state", Value: []byte(`"retired"`)},
	}
	out, _, err := Apply(builderapi.TargetAgent, "a", "agents/a.yaml", live, true, edits, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(out, "enabled: false") || !strings.Contains(out, "lifecycle_state: retired") {
		t.Fatalf("content = %q", out)
	}
}

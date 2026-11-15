package builderparts

import "testing"

func TestDriftPromptAndPolicy(t *testing.T) {
	file := "id: a\nbody: |\n  <!-- tachyon:prompt style@2 -->\n  Be terse.\n  <!-- /tachyon:prompt -->\npermissions:\n  permission_mode: plan\nmcp_scopes: [a, b]\n"
	pr := Ref{Kind: KindPrompt, PartID: "style", Hash: TextHash("Be terse.")}
	if Drifted(file, pr, nil) {
		t.Fatal("clean block reads as drift")
	}
	pol := map[string]any{"permission_mode": "plan", "mcp_scopes": []any{"a", "b"}}
	pf := Ref{Kind: KindPolicy, PartID: "p", Hash: PolicyHash(pol)}
	if Drifted(file, pf, []string{"permission_mode", "mcp_scopes"}) {
		t.Fatal("clean policy reads as drift")
	}
	if !Drifted(file+"x: 1\n", Ref{Kind: KindPrompt, PartID: "gone", Hash: "h"}, nil) {
		t.Fatal("missing block is drift")
	}
}

func TestBlockRoundTrip(t *testing.T) {
	b := PutBlock("Intro.\n", "s", 1, "One.\n")
	b = PutBlock(b, "s", 2, "Two.")
	if got, ok := BlockText(b, "s"); !ok || got != "Two." {
		t.Fatalf("%q %v", got, ok)
	}
	if DropBlock(b, "s") != "Intro.\n\n" && DropBlock(b, "s") != "Intro.\n" {
		t.Fatalf("drop: %q", DropBlock(b, "s"))
	}
}

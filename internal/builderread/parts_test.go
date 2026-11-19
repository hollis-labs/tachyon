package builderread

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hollis-labs/tachyon/internal/builderparts"
)

func TestLibraryPartsReadWithDrift(t *testing.T) {
	r, mux, src, ix := fixture(t, nil)
	ctx := context.Background()
	if got := get(t, mux, "/library/prompts", 200)["items"].([]any); len(got) != 0 {
		t.Fatalf("empty library: %v", got)
	}
	// builder carries the prompt in its body and the policy in its fields.
	src.files["agents/builder.yaml"] = agentV1 + "body: |\n  " + "<!-- tachyon:prompt style@2 -->\n  Be terse.\n  <!-- /tachyon:prompt -->\n"
	src.plan = "plan_2"
	pol := map[string]any{"permission_mode": "acceptEdits"}
	err := r.Index.DB.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
		if err := builderparts.Commit(ctx, tx, &builderparts.Effects{
			Parts: []builderparts.Part{
				{Kind: "prompt", ID: "style", Name: "Style", Summary: "house", Text: "Be terse.", Revision: 2},
				{Kind: "policy", ID: "edits", Name: "Edits", Bundle: pol, Revision: 1},
			},
			Attach: []builderparts.Ref{
				{AgentID: "builder", Kind: "prompt", PartID: "style", Rev: 2, Hash: builderparts.TextHash("Be terse.")},
				{AgentID: "builder", Kind: "policy", PartID: "edits", Rev: 1, Hash: builderparts.PolicyHash(pol)},
			}}, "op", "plan_1", 1e9); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	items := get(t, mux, "/library/prompts", 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["used_by_count"].(float64) != 1 || items[0].(map[string]any)["revision"].(float64) != 2 {
		t.Fatalf("prompts: %v", items)
	}
	d := get(t, mux, "/library/prompts/style", 200)
	ra := d["referencing_agents"].([]any)[0].(map[string]any)
	if d["body"] != "Be terse." || ra["id"] != "builder" || ra["drifted"] != false {
		t.Fatalf("detail: %v", d)
	}
	if p := get(t, mux, "/library/policies/edits", 200); p["referencing_agents"].([]any)[0].(map[string]any)["drifted"] != false || p["privileged"] != true {
		t.Fatalf("policy: %v", p)
	}
	a := get(t, mux, "/agents/builder", 200)
	if len(a["parts"].([]any)) != 2 || len(a["prompt"].(map[string]any)["parts"].([]any)) != 1 {
		t.Fatalf("agent parts: %v", a["parts"])
	}
	if h := get(t, mux, "/library/prompts/style/history", 200)["entries"].([]any); len(h) != 1 || h[0].(map[string]any)["revision"].(float64) != 2 {
		t.Fatalf("history: %v", h)
	}
	if u := get(t, mux, "/library/prompts/style/used_by", 200); u["counts"].(map[string]any)["agents"].(float64) != 1 {
		t.Fatalf("used-by: %v", u)
	}
	if sum, err := ix.Summary(ctx); err != nil || sum.Prompts != 1 || sum.Policies != 1 {
		t.Fatalf("summary: %+v %v", sum, err)
	}
	// Hand-edit the block and the policy field: both read as drifted.
	src.plan = "plan_3"
	src.files["agents/builder.yaml"] = agentV1 + "body: |\n  <!-- tachyon:prompt style@2 -->\n  Be rude.\n  <!-- /tachyon:prompt -->\n"
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	for _, a := range get(t, mux, "/agents/builder", 200)["parts"].([]any) {
		if a.(map[string]any)["kind"] == "prompt" && a.(map[string]any)["drifted"] != true {
			t.Fatalf("hand edit not drifted: %v", a)
		}
	}
	get(t, mux, "/library/prompts/ghost", 404)
}

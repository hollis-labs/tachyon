package alertsrules

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

func TestDefaultPackParsesClean(t *testing.T) {
	p, problems := ParsePack(alertsapi.DefaultRulePack())
	if len(problems) != 0 || len(p.Rules) == 0 {
		t.Fatalf("default pack: %d rules, problems %v", len(p.Rules), problems)
	}
}

// The shared table: the TS evaluator runs the same cases until it is deleted (AL8).
func TestConformance(t *testing.T) {
	p, _ := ParsePack(alertsapi.DefaultRulePack())
	var conf struct {
		Cases []struct {
			Name    string   `json:"name"`
			Engines []string `json:"engines"`
			Event   Event    `json:"event"`
			Expect  Result   `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(alertsapi.Conformance(), &conf); err != nil {
		t.Fatal(err)
	}
	if len(conf.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range conf.Cases {
		if len(c.Engines) > 0 && !slices.Contains(c.Engines, "go") {
			continue
		}
		got := p.Evaluate(c.Event)
		if !reflect.DeepEqual(got, c.Expect) {
			g, _ := json.Marshal(got)
			w, _ := json.Marshal(c.Expect)
			t.Errorf("%s:\n got  %s\n want %s", c.Name, g, w)
		}
	}
}

func TestParsePackSkipsBadRulesAndClearsCatchAllNeedsYou(t *testing.T) {
	p, problems := ParsePack([]byte(`{"version":1,"id":"x","rules":[
	 {"id":"a","match":{"kinds":["k"]},"alert":{"title":"t","icon":"nope"}},
	 {"id":"b","match":{"kinds":["k"]},"alert":{"title":"t","severity":"loud"}},
	 {"id":"c","match":{},"alert":{"title":"t","needsYou":true}},
	 {"id":"c","match":{},"action":"drop"},
	 {"id":"d","match":{"minSeverity":"x"},"action":"drop"},
	 {"match":{},"action":"drop"}]}`))
	if len(p.Rules) != 1 || p.Rules[0].ID != "c" || p.Rules[0].Alert.NeedsYou || len(problems) != 6 {
		t.Errorf("rules %+v problems %v", p.Rules, problems)
	}
	if p, probs := ParsePack([]byte(`{"version":2}`)); len(p.Rules) != 0 || len(probs) != 1 {
		t.Errorf("version 2: %v", probs)
	}
	if _, probs := ParsePack([]byte(`nope`)); len(probs) != 1 {
		t.Error("invalid JSON must report")
	}
}

func TestDisabledRuleIsSkipped(t *testing.T) {
	p, _ := ParsePack([]byte(`{"version":1,"id":"x","rules":[
	 {"id":"off","enabled":false,"match":{"kinds":["k"]},"action":"drop"},
	 {"id":"on","match":{"kinds":["k"]},"alert":{"title":"{{kind}}"}}]}`))
	if r := p.Evaluate(Event{Kind: "k", Severity: "info"}); r.RuleID == nil || *r.RuleID != "on" || r.Alert.Title != "k" {
		t.Errorf("%+v", r)
	}
}

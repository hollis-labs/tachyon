package server

import (
	"testing"

	"github.com/hollis-labs/tachyon/internal/plannerassign"
)

func TestPlannerWorkIndexKeepsTheEarliestTaskAndThemePerAgent(t *testing.T) {
	by := plannerWorkIndex([]plannerassign.View{
		{AgentID: "a", Target: plannerassign.Target{Kind: "torque.task", Ref: "CW-1"}},
		{AgentID: "a", Target: plannerassign.Target{Kind: "torque.task", Ref: "CW-2"}},
		{AgentID: "a", Target: plannerassign.Target{DB: "themes", ID: "t1"}},
		{AgentID: "b", Target: plannerassign.Target{DB: "decisions", ID: "d1"}},
		{TeamID: "team", Target: plannerassign.Target{DB: "themes", ID: "t2"}},
	})
	if by["a"] != [2]string{"CW-1", "planner:theme/t1"} {
		t.Errorf("a: %v", by["a"])
	}
	if by["b"] != [2]string{} || len(by) != 2 {
		t.Errorf("decisions and team assignments are left out: %v", by)
	}
}

package server

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
	"github.com/hollis-labs/tachyon/internal/plannerassign"
)

func init() { registerServerMount(mountPlannerAssign) }

// mountPlannerAssign serves the real Planner assignments (PL2): the assigned_to relation on assignment
// objects, and the agent work list. Each pattern is more specific than the planner subtree mount. With
// TACHYON_PLANNER_STUBS=1 the PL0 stub owns the same patterns, so this stands down.
func mountPlannerAssign(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if s.store == nil || os.Getenv("TACHYON_PLANNER_STUBS") == "1" {
		return
	}
	svc := plannerassign.New(objects.New(s.store), strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.TorqueURL), "/"))
	planner.TaskAssignment = func(ctx context.Context, task string) planner.Item {
		as, err := svc.Assignees(ctx, task)
		if err != nil || len(as) == 0 {
			return nil
		}
		a := as[0]
		out := planner.Item{"id": a.ID, "state": a.State}
		if a.AgentID != "" {
			out["agent_id"] = a.AgentID
		} else {
			out["team_id"] = a.TeamID
		}
		return out
	}
	h := operator.RequireOperator(svc.Handler())
	for _, route := range plannerassign.Routes {
		method, path, _ := strings.Cut(route, " ")
		mux.Handle(method+" "+planner.Prefix+path, h)
	}
}

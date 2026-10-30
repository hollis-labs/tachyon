package server

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/chatwork"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/plannerassign"
	"github.com/hollis-labs/tachyon/internal/torqueread"
)

func init() { registerServerMount(mountChatWork) }

// mountChatWork serves the chat Work panel (T7) over the Planner assignments (PL2), the thread's own plan
// links and torqueread. It is more specific than the chat stub's prefix, so it wins when both are on;
// with TACHYON_PLANNER_STUBS=1 the planner is a stub, so the chat stub keeps the route.
func mountChatWork(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if s.store == nil || s.chatPlan == nil || s.chatThreads == nil || os.Getenv("TACHYON_PLANNER_STUBS") == "1" {
		return
	}
	torque := strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.TorqueURL), "/")
	assign := plannerassign.New(objects.New(s.store), torque)
	svc := &chatwork.Service{
		Plan: func(ctx context.Context, ref chatapi.ThreadRef) (chatplan.Plan, error) {
			return s.chatPlan.Get(ctx, ref, "thread")
		},
		Assigned: assign.Work,
	}
	if torque != "" {
		svc.Task = torqueread.New(torque, s.cfg.Upstreams.TorqueToken).GetTask
	}
	exists := func(ctx context.Context, ref string) error { _, err := s.chatThreads.Agg.Get(ctx, ref); return err }
	h := operator.RequireOperator(chatwork.Handler(svc, exists))
	for _, route := range chatwork.Routes {
		method, path, _ := strings.Cut(route, " ")
		mux.Handle(method+" "+chatapi.Prefix+path, h)
	}
}

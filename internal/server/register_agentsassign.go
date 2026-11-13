package server

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/builderchange"

	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/forgeassign"

	"github.com/hollis-labs/tachyon/internal/agentsassign"
	"github.com/hollis-labs/tachyon/internal/agentsserve"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
	"github.com/hollis-labs/tachyon/internal/plannerassign"
)

// agentsAssigner is the A5 join for Session.assignment: the session's Torque task, the Planner theme that
// holds it, and "task k of m". It needs the object store and a Torque URL; without either the field stays null.
func (s *Server) agentsAssigner() agentsserve.Assigner {
	torque := strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.TorqueURL), "/")
	if s.store == nil || torque == "" {
		return nil
	}
	return &agentsassign.Joiner{DB: s.store, Planner: agentsassign.Live{
		Owned: &planner.Owned{Objects: objects.New(s.store)},
		Tasks: planner.NewTasks(torque, s.cfg.Upstreams.TorqueToken),
	}}
}

// agentsWorker lets the Agents monitor show a Planner assignment (agent assigned to a task or a theme) on
// the agent's session, the other direction of the Planner's "assigned to". Without a store it is off.
func (s *Server) agentsWorker() agentsserve.Worker {
	if s.store == nil {
		return nil
	}
	return &plannerWork{svc: plannerassign.New(objects.New(s.store), strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.TorqueURL), "/"))}
}

// plannerWork reads every active assignment once and serves it per agent for 30 s, so a sessions list of
// any size costs one read.
type plannerWork struct {
	svc *plannerassign.Service
	mu  sync.Mutex
	at  time.Time
	by  map[string][2]string // agent id -> {task ref, theme ref}
}

func (p *plannerWork) Work(ctx context.Context, agentID string) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.by == nil || time.Since(p.at) > 30*time.Second {
		if views, err := p.svc.List(ctx, plannerassign.Filter{State: plannerassign.StateActive}); err == nil {
			p.by = plannerWorkIndex(views)
		} else if p.by == nil {
			p.by = map[string][2]string{}
		}
		p.at = time.Now()
	}
	w := p.by[agentID]
	return w[0], w[1]
}

// plannerWorkIndex keeps, per agent, the earliest assigned task and the earliest assigned theme (List
// returns assignments earliest first). Team assignments and other target kinds are left out.
func plannerWorkIndex(views []plannerassign.View) map[string][2]string {
	by := map[string][2]string{}
	for _, v := range views {
		if v.AgentID == "" {
			continue
		}
		w := by[v.AgentID]
		switch {
		case v.Target.Ref != "" && w[0] == "":
			w[0] = v.Target.Ref
		case v.Target.DB == "themes" && v.Target.ID != "" && w[1] == "":
			w[1] = "planner:theme/" + v.Target.ID
		}
		by[v.AgentID] = w
	}
	return by
}

// agentsSender delivers Agents nudges as Tether mail with the messages-scope token (the same one the PR
// assign-agent op uses). Without a Tether address or token the nudge capability stays off.
func (s *Server) agentsSender() agentsserve.Sender {
	addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr)
	if addr == "" || messageTokenFile(s) == "" {
		return nil
	}
	base, c, err := builderread.NewTetherClient(addr, messageTokenFile(s))
	if err != nil {
		return nil
	}
	return &forgeassign.TetherMail{Base: base, Client: c}
}

// agentsRestarter restarts sessions with the Tether control token (TACHYON_TETHER_CONTROL_TOKEN_FILE, sessions
// scope). Without it restart stays off.
func (s *Server) agentsRestarter() agentsserve.Restarter {
	tf := strings.TrimSpace(os.Getenv("TACHYON_TETHER_CONTROL_TOKEN_FILE"))
	if tf == "" {
		return nil
	}
	base, c, err := builderchange.NewControlClient(controlAddr(s.cfg.Upstreams.TetherAddr), tf)
	if err != nil {
		return nil
	}
	return &agentsserve.TetherRestarter{Base: base, Client: c}
}

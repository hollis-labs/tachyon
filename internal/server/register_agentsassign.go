package server

import (
	"strings"

	"github.com/hollis-labs/tachyon/internal/agentsassign"
	"github.com/hollis-labs/tachyon/internal/agentsserve"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
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

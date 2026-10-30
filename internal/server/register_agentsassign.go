package server

import (
	"strings"

	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/forgeassign"

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

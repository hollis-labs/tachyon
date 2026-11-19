package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/forgeassign"
)

// Result mail uses the existing message credential, never an owner credential.
// Missing address/grant leaves the durable outbox pending; it does not fall
// back to owner relay. The receiver deduplicates the fixed run key across retries.
func (s *Server) actionResultNotifier() func(context.Context, actionrun.Run) error {
	addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr)
	if addr == "" || messageTokenFile(s) == "" {
		return nil
	}
	base, reader, err := builderread.NewTetherClient(addr, s.cfg.Upstreams.TetherTokenFile)
	if err != nil {
		return nil
	}
	_, writer, err := builderread.NewTetherClient(addr, messageTokenFile(s))
	if err != nil {
		return nil
	}
	agents := &forgeassign.TetherAgents{Base: base, Client: reader}
	mail := &forgeassign.TetherMail{Base: base, Client: writer}
	return func(ctx context.Context, r actionrun.Run) error { return sendActionResult(ctx, agents, mail, r) }
}

func sendActionResult(ctx context.Context, agents interface {
	Lookup(context.Context, string) (forgeassign.Agent, error)
}, mail interface {
	Send(context.Context, string, string, string, map[string]string) (string, error)
}, r actionrun.Run) error {
	agent, err := agents.Lookup(ctx, r.Requester.Agent)
	if err != nil {
		return err
	}
	// No terminal text, reason or typed input is included in durable mail.
	body := fmt.Sprintf("Registered action %s on %s: %s. Approval %s; run %s; digest %s.", r.Action, r.Machine, r.State, r.ItemID, r.ID, r.OpDigest)
	if r.ExitCode != nil {
		body += fmt.Sprintf(" Exit code: %d.", *r.ExitCode)
	} else {
		body += " No command exit code was recorded."
	}
	if r.Requester.Task != "" {
		body += " Task (requester supplied): " + r.Requester.Task + "."
	}
	if plan := r.Params["plan_id"]; plan != "" {
		body += " Catalog plan: " + plan + ". Catalog approval/apply is separate; this message grants no authorization."
	}
	receipt, err := mail.Send(ctx, agent.URN, "Action result: "+r.State, body, map[string]string{"source": "tachyon-actions", "run_id": r.ID, "item_id": r.ItemID, "tether.message_id": "tachyon-action-result:" + r.ID})
	if err == nil && receipt == "" {
		return errors.New("Tether returned no action result receipt")
	}
	return err
}

package chatterminal

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

var (
	ErrDenied  = errors.New("denied by policy")
	ErrInvalid = errors.New("invalid")
)

// Service is the one place a command becomes a proposal, a run, a stream message and an event. The HTTP
// routes (operator) and the agent verb both go through it.
type Service struct {
	Store  *Store
	Mgr    *Manager
	Config *Config
	// Exists reports whether the thread exists (nil error).
	Exists func(ctx context.Context, ref string) error
	// CloseOnComplete is the saved default for new runs.
	CloseOnComplete func(ctx context.Context) bool
	// Message adds the run block to the thread's stream and returns its message ref. Tether threads have
	// no stream Tachyon writes to, so it may return "" for them.
	Message func(ctx context.Context, thread, runID string) (string, error)
	Publish func(ctx context.Context, thread, typ string, fields map[string]any)
}

func (s *Service) publish(thread string, r Run, extra map[string]any) {
	if s.Publish == nil {
		return
	}
	f := map[string]any{"run_id": r.RunID, "state": r.State, "seq": 0, "closed": r.ClosedAt != nil}
	for k, v := range extra {
		f[k] = v
	}
	s.Publish(context.Background(), thread, chatapi.EventTerminal, f)
}

// Propose is the agent's way in. requestedBy is the agent address. A policy "deny" returns ErrDenied; an
// "allow" starts the run at once with approval.mode=policy; anything else waits for the operator.
func (s *Service) Propose(ctx context.Context, thread, machineID, command, reason, requestedBy string) (Run, Proposal, error) {
	cmd, ok := cleanCommand(command)
	if !ok {
		return Run{}, Proposal{}, ErrInvalid
	}
	if s.Exists != nil {
		if err := s.Exists(ctx, thread); err != nil {
			return Run{}, Proposal{}, ErrNotFound
		}
	}
	mach, err := s.Config.Machine(machineID)
	if err != nil {
		return Run{}, Proposal{}, err
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	decision, rule := s.Config.Decide(mach.ID, cmd)
	if decision == "deny" {
		return Run{}, Proposal{}, ErrDenied
	}
	n := NewRun{Thread: thread, Machine: mach.ID, User: mach.SSHUser, Command: cmd, Reason: reason, RequestedBy: requestedBy, Mode: "user", TTL: proposalTTL}
	if decision == "allow" {
		n.Mode, n.RuleID = "policy", rule
	}
	return s.create(ctx, n)
}

func (s *Service) create(ctx context.Context, n NewRun) (Run, Proposal, error) {
	if s.CloseOnComplete != nil {
		n.CloseOnComplete = s.CloseOnComplete(ctx)
	}
	run, prop, err := s.Store.Create(ctx, n)
	if err != nil {
		return Run{}, Proposal{}, err
	}
	if s.Message != nil {
		if ref, err := s.Message(ctx, run.ThreadRef, run.RunID); err == nil && ref != "" {
			_ = s.Store.SetMessage(ctx, run.RunID, ref)
			run.MessageRef = ref
		}
	}
	s.publish(run.ThreadRef, run, nil)
	if run.State == StateRunning {
		s.Mgr.StartRun(ctx, run)
	}
	return run, prop, nil
}

// Approve is the operator's Run. digest, when given, must equal the proposal's.
func (s *Service) Approve(ctx context.Context, proposalID, digest string) (Run, error) {
	p, err := s.Store.Proposal(ctx, proposalID)
	if err != nil {
		return Run{}, err
	}
	if digest != "" && digest != p.CommandDigest {
		return Run{}, ErrDigest
	}
	run, err := s.Store.Approve(ctx, proposalID, operatorActor)
	if err != nil {
		return Run{}, err
	}
	s.publish(run.ThreadRef, run, nil)
	s.Mgr.StartRun(ctx, run)
	if r, err := s.Store.Run(ctx, run.RunID); err == nil {
		run = r
	}
	s.Mgr.LiveOutput(&run)
	return run, nil
}

var ErrDigest = errors.New("digest mismatch")

func (s *Service) Deny(ctx context.Context, proposalID string) (Run, error) {
	run, err := s.Store.Deny(ctx, proposalID, operatorActor)
	if err != nil {
		return Run{}, err
	}
	s.publish(run.ThreadRef, run, nil)
	if s.Mgr.Ended != nil {
		s.Mgr.Ended(ctx, run)
	}
	return run, nil
}

// Edit replaces a pending proposal with a new one for the edited command (new digest). The old one is
// superseded and can no longer be approved.
func (s *Service) Edit(ctx context.Context, proposalID, command string) (Proposal, error) {
	cmd, ok := cleanCommand(command)
	if !ok {
		return Proposal{}, ErrInvalid
	}
	old, err := s.Store.Proposal(ctx, proposalID)
	if err != nil {
		return Proposal{}, err
	}
	oldRun, err := s.Store.Run(ctx, old.RunID)
	if err != nil {
		return Proposal{}, err
	}
	if decision, _ := s.Config.Decide(old.MachineID, cmd); decision == "deny" {
		return Proposal{}, ErrDenied
	}
	// An edited command is always the operator's to approve, whatever the allow rules say.
	_, prop, err := s.create(ctx, NewRun{Thread: old.ThreadRef, Machine: old.MachineID, User: oldRun.SSHUser, Command: cmd, Reason: old.Reason,
		RequestedBy: old.RequestedBy, SessionID: deref(old.SessionID), Mode: "user", Supersedes: old.ID, TTL: proposalTTL})
	if err != nil {
		return Proposal{}, err
	}
	s.publish(oldRun.ThreadRef, Run{RunID: oldRun.RunID, State: StateDenied}, nil)
	return prop, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Sweep expires proposals nobody answered, until ctx ends.
func (s *Service) Sweep(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ExpireNow(ctx)
		}
	}
}

func (s *Service) ExpireNow(ctx context.Context) {
	runs, err := s.Store.ExpireDue(ctx)
	if err != nil {
		return
	}
	for _, r := range runs {
		s.publish(r.ThreadRef, r, nil)
		if s.Mgr.Ended != nil {
			s.Mgr.Ended(ctx, r)
		}
	}
}

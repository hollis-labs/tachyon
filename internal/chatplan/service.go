package chatplan

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Service is the plan operations shared by the REST handler and the agent verbs: it resolves
// the plan's owner (thread or project), applies the change, and fires the side effects.
type Service struct {
	Store *Store
	// Exists reports whether the thread exists (nil error).
	Exists func(ctx context.Context, ref string) error
	// ProjectOf returns the thread's Torque project id, or "" when it has none.
	ProjectOf func(ctx context.Context, ref string) (string, error)
	// Publish sends a thread event (types plan, panel); may be nil.
	Publish func(ctx context.Context, thread, typ string, fields map[string]any)
	// Note appends the plan card message to an app thread; may be nil.
	Note func(ctx context.Context, thread string, p Plan)
	// Activity records the change in the thread's feed (by is "user" or "agent"); may be nil.
	Activity func(ctx context.Context, thread, by, reason string, p Plan)
	// Notify refreshes the thread's summary on its stream and the spine; may be nil.
	Notify func(ctx context.Context, thread string)
	// Carried records a plan moved to a replacement session in the new thread's feed; may be nil.
	Carried func(ctx context.Context, from, to string, p Plan)
}

// Attach is called for a session acting through the pinned Tether proxy: it records the session
// in the lineage and, when the session continues another of the same logical agent and has no
// plan yet, moves that conversation's plan to it. Nothing is asked of the agent. A refused or
// failed carry leaves the session on a clean start; the old plan stays under the old thread.
func (s *Service) Attach(ctx context.Context, session, agent, continues string) {
	res, err := s.Store.Carry(ctx, session, agent, continues)
	if err != nil {
		slog.Warn("chat plan carry-over not applied", "session", session, "continues", continues, "err", err)
		return
	}
	s.publishCarry(ctx, res)
}

// AssociateContinuation records a verified exact pair before the replacement
// makes any proxied request. Validation of Tether's native admission is the
// responsibility of the caller; this operation commits local chat state only.
func (s *Service) AssociateContinuation(ctx context.Context, source, replacement, agent string) (Association, error) {
	out, err := s.Store.AssociateContinuation(ctx, source, replacement, agent)
	if err == nil {
		s.publishCarry(ctx, out.Carried)
	}
	return out, err
}

func (s *Service) publishCarry(ctx context.Context, res Carried) {
	if !res.Moved {
		return
	}
	ctx = context.WithoutCancel(ctx)
	slog.Info("chat plan carried", "from", res.From, "to", res.To, "plan", res.Plan.ID)
	if s.Carried != nil {
		s.Carried(ctx, res.From, res.To, res.Plan)
	}
	if s.Publish != nil {
		s.Publish(ctx, res.To, chatapi.EventPlan, map[string]any{"plan_id": res.Plan.ID, "revision": res.Plan.Revision, "progress": res.Plan.Progress()})
		s.Publish(ctx, res.To, chatapi.EventPanel, map[string]any{"panel": "plan", "ref": res.Plan.ID, "reason": "plan carried over from the previous session", "source": "agent"})
	}
	if s.Notify != nil {
		s.Notify(ctx, res.From)
		s.Notify(ctx, res.To)
	}
}

// Clear removes the thread's plan and, for a Tether thread, stops older plans from being carried
// into it or any later replacement.
func (s *Service) Clear(ctx context.Context, ref chatapi.ThreadRef) error {
	if s.Exists != nil {
		if err := s.Exists(ctx, ref.String()); err != nil {
			return ErrNotFound
		}
	}
	if err := s.Store.Clear(ctx, ref.String()); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if s.Publish != nil {
		s.Publish(ctx, ref.String(), chatapi.EventPlan, map[string]any{"plan_id": "", "revision": 0, "progress": Progress{}})
	}
	if s.Notify != nil {
		s.Notify(ctx, ref.String())
	}
	return nil
}

// ErrNotContinuable refuses the Continue flag on a thread that is not a Tether session: only
// those have a lineage a replacement session can follow.
var ErrNotContinuable = errors.New("continue applies to Tether sessions only")

func (s *Service) continuable(ctx context.Context, ref chatapi.ThreadRef) error {
	if ref.Source != chatapi.SourceTether {
		return ErrNotContinuable
	}
	if s.Exists != nil {
		if err := s.Exists(ctx, ref.String()); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

// Continue reports whether the session's plan follows to the session that continues it.
func (s *Service) Continue(ctx context.Context, ref chatapi.ThreadRef) (bool, error) {
	if err := s.continuable(ctx, ref); err != nil {
		return false, err
	}
	return s.Store.Continue(ctx, ref.ID)
}

// SetContinue turns the session's Continue on or off. Off: its replacement starts fresh, as if
// Tether had reported no continues; the plan stays where it is.
func (s *Service) SetContinue(ctx context.Context, ref chatapi.ThreadRef, on bool) error {
	if err := s.continuable(ctx, ref); err != nil {
		return err
	}
	return s.Store.SetContinue(ctx, ref.ID, on)
}

func (s *Service) owner(ctx context.Context, ref chatapi.ThreadRef, scope string) (string, string, error) {
	if s.Exists != nil {
		if err := s.Exists(ctx, ref.String()); err != nil {
			return "", "", ErrNotFound
		}
	}
	switch scope {
	case "", "thread":
		return "thread", ref.String(), nil
	case "project":
		if s.ProjectOf == nil {
			return "", "", ErrNoProject
		}
		id, err := s.ProjectOf(ctx, ref.String())
		if err != nil {
			return "", "", err
		}
		if id == "" {
			return "", "", ErrNoProject
		}
		return "project", id, nil
	}
	return "", "", ErrInvalid
}

// Get returns the plan, or ErrNotFound when none was written yet.
func (s *Service) Get(ctx context.Context, ref chatapi.ThreadRef, scope string) (Plan, error) {
	sc, owner, err := s.owner(ctx, ref, scope)
	if err != nil {
		return Plan{}, err
	}
	if sc == "project" {
		return s.Store.ForProject(ctx, owner)
	}
	return s.Store.ForThread(ctx, owner)
}

func (s *Service) after(ctx context.Context, ref chatapi.ThreadRef, p Plan, by, reason string) {
	ctx = context.WithoutCancel(ctx)
	if s.Publish != nil {
		pr := p.Progress()
		s.Publish(ctx, ref.String(), chatapi.EventPlan, map[string]any{"plan_id": p.ID, "revision": p.Revision, "progress": pr})
		if by == "agent" {
			s.Publish(ctx, ref.String(), chatapi.EventPanel, map[string]any{"panel": "plan", "ref": p.ID, "reason": reason, "source": "agent"})
		}
	}
	if s.Activity != nil {
		s.Activity(ctx, ref.String(), by, reason, p)
	}
	if s.Notify != nil {
		s.Notify(ctx, ref.String())
	}
}

// Write upserts the whole plan. An agent write also appends a plan message to the thread.
func (s *Service) Write(ctx context.Context, ref chatapi.ThreadRef, scope string, in WriteIn) (Plan, error) {
	sc, owner, err := s.owner(ctx, ref, scope)
	if err != nil {
		return Plan{}, err
	}
	p, err := s.Store.Write(ctx, sc, owner, in)
	if err != nil {
		return Plan{}, err
	}
	if in.By == "agent" && sc == "thread" && s.Note != nil {
		s.Note(context.WithoutCancel(ctx), ref.String(), p)
	}
	s.after(ctx, ref, p, in.By, "plan written")
	return p, nil
}

func (s *Service) plan(ctx context.Context, ref chatapi.ThreadRef, scope string) (Plan, error) {
	return s.Get(ctx, ref, scope)
}

// PlanOf returns one of the thread's plans by id; an empty id means the first plan. A plan of another thread is ErrNotFound.
func (s *Service) PlanOf(ctx context.Context, ref chatapi.ThreadRef, planID string) (Plan, error) {
	if planID == "" {
		return s.Get(ctx, ref, "thread")
	}
	if err := s.known(ctx, ref); err != nil {
		return Plan{}, err
	}
	p, err := s.Store.Get(ctx, planID)
	if err != nil {
		return Plan{}, err
	}
	if p.Scope != "thread" || p.ThreadRef == nil || *p.ThreadRef != ref.String() {
		return Plan{}, ErrNotFound
	}
	return p, nil
}

func (s *Service) PatchStep(ctx context.Context, ref chatapi.ThreadRef, scope, stepID string, in StepIn, expected int64, by string) (Plan, Step, error) {
	return s.PatchStepOf(ctx, ref, scope, "", stepID, in, expected, by)
}

// PatchStepOf is PatchStep on a named plan of the thread (planID empty: the first plan).
func (s *Service) PatchStepOf(ctx context.Context, ref chatapi.ThreadRef, scope, planID, stepID string, in StepIn, expected int64, by string) (Plan, Step, error) {
	var cur Plan
	var err error
	if planID != "" && scope != "project" {
		cur, err = s.PlanOf(ctx, ref, planID)
	} else {
		cur, err = s.plan(ctx, ref, scope)
	}
	if err != nil {
		return Plan{}, Step{}, err
	}
	p, st, err := s.Store.PatchStep(ctx, cur.ID, stepID, in, expected)
	if err != nil {
		return Plan{}, Step{}, err
	}
	s.after(ctx, ref, p, by, "step changed")
	return p, st, nil
}

func (s *Service) AddStep(ctx context.Context, ref chatapi.ThreadRef, scope string, in StepIn, expected int64, by string) (Plan, Step, error) {
	return s.AddStepOf(ctx, ref, scope, "", in, expected, by)
}

// AddStepOf is AddStep on a named plan of the thread (planID empty: the first plan).
func (s *Service) AddStepOf(ctx context.Context, ref chatapi.ThreadRef, scope, planID string, in StepIn, expected int64, by string) (Plan, Step, error) {
	var cur Plan
	var err error
	if planID != "" && scope != "project" {
		cur, err = s.PlanOf(ctx, ref, planID)
	} else {
		cur, err = s.plan(ctx, ref, scope)
	}
	if err != nil {
		return Plan{}, Step{}, err
	}
	p, st, err := s.Store.AddStep(ctx, cur.ID, in, expected)
	if err != nil {
		return Plan{}, Step{}, err
	}
	s.after(ctx, ref, p, by, "step added")
	return p, st, nil
}

func (s *Service) Reorder(ctx context.Context, ref chatapi.ThreadRef, scope string, ids []string, expected int64, by string) (Plan, error) {
	cur, err := s.plan(ctx, ref, scope)
	if err != nil {
		return Plan{}, err
	}
	p, err := s.Store.Reorder(ctx, cur.ID, ids, expected)
	if err != nil {
		return Plan{}, err
	}
	s.after(ctx, ref, p, by, "steps reordered")
	return p, nil
}

// IsNotFound reports a missing plan, step or thread.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

func (s *Service) known(ctx context.Context, ref chatapi.ThreadRef) error {
	if s.Exists != nil {
		if err := s.Exists(ctx, ref.String()); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

// Plans lists the thread's plans in order; none is an empty list, not an error.
func (s *Service) Plans(ctx context.Context, ref chatapi.ThreadRef) ([]Plan, error) {
	if err := s.known(ctx, ref); err != nil {
		return nil, err
	}
	return s.Store.PlansForThread(ctx, ref.String())
}

// CreatePlan adds an empty plan to the thread.
func (s *Service) CreatePlan(ctx context.Context, ref chatapi.ThreadRef, title *string, by string) (Plan, error) {
	if err := s.known(ctx, ref); err != nil {
		return Plan{}, err
	}
	p, err := s.Store.CreatePlan(ctx, ref.String(), title, by)
	if err != nil {
		return Plan{}, err
	}
	s.after(ctx, ref, p, by, "plan added")
	return p, nil
}

// DeletePlan removes one of the thread's plans.
func (s *Service) DeletePlan(ctx context.Context, ref chatapi.ThreadRef, planID string, expected int64, by string) error {
	if err := s.known(ctx, ref); err != nil {
		return err
	}
	p, err := s.Store.Get(ctx, planID)
	if err != nil {
		return err
	}
	if err := s.Store.DeletePlan(ctx, ref.String(), planID, expected); err != nil {
		return err
	}
	s.after(ctx, ref, p, by, "plan removed")
	return nil
}

// ReorderPlans sets the order of the thread's plans.
func (s *Service) ReorderPlans(ctx context.Context, ref chatapi.ThreadRef, ids []string, by string) ([]Plan, error) {
	if err := s.known(ctx, ref); err != nil {
		return nil, err
	}
	ps, err := s.Store.ReorderPlans(ctx, ref.String(), ids)
	if err != nil {
		return nil, err
	}
	if len(ps) > 0 {
		s.after(ctx, ref, ps[0], by, "plans reordered")
	}
	return ps, nil
}

// MoveStep moves a step between two of the thread's plans; each side gets its own plan.changed entry.
func (s *Service) MoveStep(ctx context.Context, ref chatapi.ThreadRef, from, to, stepID string, pos int, expFrom, expTo int64, by string) (Plan, Plan, Step, error) {
	if err := s.known(ctx, ref); err != nil {
		return Plan{}, Plan{}, Step{}, err
	}
	src, dst, st, err := s.Store.MoveStep(ctx, ref.String(), from, to, stepID, pos, expFrom, expTo)
	if err != nil {
		return Plan{}, Plan{}, Step{}, err
	}
	s.after(ctx, ref, src, by, "step moved out")
	s.after(ctx, ref, dst, by, "step moved in")
	return src, dst, st, nil
}

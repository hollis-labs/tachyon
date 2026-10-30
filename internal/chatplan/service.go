package chatplan

import (
	"context"
	"errors"

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

func (s *Service) PatchStep(ctx context.Context, ref chatapi.ThreadRef, scope, stepID string, in StepIn, expected int64, by string) (Plan, Step, error) {
	cur, err := s.plan(ctx, ref, scope)
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
	cur, err := s.plan(ctx, ref, scope)
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

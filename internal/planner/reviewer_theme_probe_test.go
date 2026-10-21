package planner

import (
	"context"
	"errors"
	"testing"
)

// Probes supplied by the Reviewer against 8f0bafd, which both failed with a nil
// error before terminal statuses were centralised in guardTheme.

func TestReviewerParkThenFinishRequiresOperator(t *testing.T) {
	s, _ := ownedFixture(t)
	approved := mustAct(t, s, newTheme(t, s, "T", "ready"), "approve", nil, op)
	parked := mustAct(t, s, approved, "park", Item{"reason": "blocked"}, agent)
	_, err := act(t, s, parked, "finish", nil, agent)
	if !errors.Is(err, ErrOperatorRequired) {
		t.Fatalf("agent finished parked formerly approved theme: %v", err)
	}
}

func TestReviewerPatchArchiveRequiresOperator(t *testing.T) {
	s, _ := ownedFixture(t)
	row := newTheme(t, s, "T", "proposed")
	_, err := s.Patch(context.Background(), "themes", textField(row, "id"), revision(row), Item{"status": "archived"}, agent)
	if !errors.Is(err, ErrOperatorRequired) {
		t.Fatalf("agent archived proposed theme via patch: %v", err)
	}
}

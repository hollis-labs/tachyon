package chatplan

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

func tetherRef(id string) chatapi.ThreadRef {
	return chatapi.ThreadRef{Source: chatapi.SourceTether, ID: id}
}

// Continue off on s1: s2 continuing s1 starts fresh and the plan stays on s1, untouched.
func TestContinueOffStartsReplacementFresh(t *testing.T) {
	s, events := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s1", "agent-a", "")
	old := writePlan(t, s, "s1", "Ship")
	if err := s.SetContinue(ctx, tetherRef("s1"), false); err != nil {
		t.Fatal(err)
	}
	*events = nil
	s.Attach(ctx, "s2", "agent-a", "s1")
	if _, err := planOf(s, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("plan followed a session whose Continue is off")
	}
	if p, err := planOf(s, "s1"); err != nil || p.ID != old.ID || p.Revision != old.Revision {
		t.Fatalf("plan on s1 changed: %+v %v", p, err)
	}
	if len(*events) != 0 {
		t.Fatalf("no carry events expected, got %v", *events)
	}
	// The replacement still writes its lineage row (continues s1), first sight wins.
	var cont string
	if err := s.Store.DB.Reader.QueryRow(`SELECT continues_id FROM chat_session_lineage WHERE session_id='s2'`).Scan(&cont); err != nil || cont != "s1" {
		t.Fatalf("s2 lineage: %q %v", cont, err)
	}
}

// Off then on again before the relaunch: continuity is back, because off deleted nothing.
func TestContinueBackOnRestoresCarry(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s1", "agent-a", "")
	old := writePlan(t, s, "s1", "Ship")
	for _, on := range []bool{false, true} {
		if err := s.SetContinue(ctx, tetherRef("s1"), on); err != nil {
			t.Fatal(err)
		}
	}
	if on, err := s.Continue(ctx, tetherRef("s1")); err != nil || !on {
		t.Fatalf("Continue = %v %v", on, err)
	}
	s.Attach(ctx, "s2", "agent-a", "s1")
	if p, err := planOf(s, "s2"); err != nil || p.ID != old.ID {
		t.Fatalf("plan not carried after Continue back on: %+v %v", p, err)
	}
}

// Off on a middle session stops the walk there: s2 -> s1(off) -> s0(plan) carries nothing.
func TestContinueOffStopsWalk(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s0", "agent-a", "")
	writePlan(t, s, "s0", "Deep")
	mustExec(t, s, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on) VALUES('s1','agent-a','s0',0,1,0)`)
	s.Attach(ctx, "s2", "agent-a", "s1")
	if _, err := planOf(s, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("walk passed a session whose Continue is off")
	}
}

// New sessions take the setting when first seen; changing it later never flips them.
func TestContinueDefaultAppliesToNewSessionsOnly(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	def := false
	s.Store.ContinueDefault = func(context.Context) bool { return def }
	s.Attach(ctx, "s1", "agent-a", "") // first sight through the proxy
	if _, err := s.Continue(ctx, tetherRef("s2")); err != nil { // first sight in the chat header
		t.Fatal(err)
	}
	def = true
	for _, id := range []string{"s1", "s2"} {
		if on, err := s.Continue(ctx, tetherRef(id)); err != nil || on {
			t.Fatalf("%s: Continue = %v %v; the default in force at first sight must stick", id, on, err)
		}
	}
	s.Attach(ctx, "s3", "agent-a", "")
	if on, _ := s.Continue(ctx, tetherRef("s3")); !on {
		t.Fatal("a session first seen after the default changed must take the new default")
	}
	// The header pinned s2 before Tether named its agent; Carry fills the agent and keeps the flag.
	s.Attach(ctx, "s2", "agent-a", "")
	var agent string
	var on int
	if err := s.Store.DB.Reader.QueryRow(`SELECT agent_id,continue_on FROM chat_session_lineage WHERE session_id='s2'`).Scan(&agent, &on); err != nil || agent != "agent-a" || on != 0 {
		t.Fatalf("s2 row: %q %d %v", agent, on, err)
	}
}

// With no setting hook (or the setting unset) Continue is on.
func TestContinueDefaultsOn(t *testing.T) {
	s, _ := lineageSetup(t)
	if on, err := s.Continue(context.Background(), tetherRef("fresh")); err != nil || !on {
		t.Fatalf("Continue = %v %v", on, err)
	}
}

func TestContinueHTTP(t *testing.T) {
	e := setup(t)
	if code, out := e.do(t, "GET", "/threads/tether:s1/continue", ""); code != 200 || out["continue"] != true {
		t.Fatalf("get: %d %v", code, out)
	}
	if code, out := e.do(t, "PUT", "/threads/tether:s1/continue", `{"continue":false}`); code != 200 || out["continue"] != false {
		t.Fatalf("put: %d %v", code, out)
	}
	if code, out := e.do(t, "GET", "/threads/tether:s1/continue", ""); code != 200 || out["continue"] != false {
		t.Fatalf("persisted: %d %v", code, out)
	}
	if code, _ := e.do(t, "PUT", "/threads/tether:s1/continue", `{}`); code != 400 {
		t.Fatalf("missing value: %d", code)
	}
	if code, out := e.do(t, "GET", "/threads/app:t1/continue", ""); code != 409 || out["code"] != "not_continuable" {
		t.Fatalf("app thread: %d %v", code, out)
	}
}

package chatplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func lineageSetup(t *testing.T) (*Service, *[]string) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var events []string
	svc := &Service{Store: &Store{DB: db}, Exists: func(context.Context, string) error { return nil },
		Publish: func(_ context.Context, thread, typ string, _ map[string]any) { events = append(events, thread+" "+typ) },
		Carried: func(_ context.Context, from, to string, _ Plan) { events = append(events, "carried "+from+" "+to) }}
	return svc, &events
}

func writePlan(t *testing.T, s *Service, session, title string) Plan {
	t.Helper()
	ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: session}
	ti := title
	p, err := s.Write(context.Background(), ref, "thread", WriteIn{By: "agent", Title: &ti, HasTitle: true, Steps: []StepIn{{Title: &ti}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func planOf(s *Service, session string) (Plan, error) {
	return s.Store.ForThread(context.Background(), "tether:"+session)
}

func TestCarryMovesPlanToReplacement(t *testing.T) {
	s, events := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s1", "agent-a", "")
	old := writePlan(t, s, "s1", "Ship")
	*events = nil
	s.Attach(ctx, "s2", "agent-a", "s1")
	got, err := planOf(s, "s2")
	if err != nil || got.ID != old.ID || got.Revision != old.Revision+1 || len(got.Steps) != 1 {
		t.Fatalf("plan not moved: %+v %v", got, err)
	}
	if _, err := planOf(s, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old thread still holds the plan: one live plan expected")
	}
	n := 0
	for _, e := range *events {
		if e == "carried tether:s1 tether:s2" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one carried event, got %v", *events)
	}
	// A repeat call is a no-op: no second move, no extra revision.
	s.Attach(ctx, "s2", "agent-a", "s1")
	if again, _ := planOf(s, "s2"); again.Revision != got.Revision {
		t.Fatal("repeat attach bumped the revision")
	}
}

// s2 -> s1 -> s0 where only s0 had a plan and s1 never touched it.
func TestCarryWalksChainThroughPlanlessSession(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s0", "agent-a", "")
	writePlan(t, s, "s0", "Deep")
	mustExec(t, s, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns) VALUES('s1','agent-a','s0',0,1)`)
	s.Attach(ctx, "s2", "agent-a", "s1")
	if p, err := planOf(s, "s2"); err != nil || p.Title == nil || *p.Title != "Deep" {
		t.Fatalf("plan not carried across a planless hop: %+v %v", p, err)
	}
}

func TestFreshSessionCarriesNothing(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s1", "agent-a", "")
	writePlan(t, s, "s1", "Old")
	s.Attach(ctx, "s2", "agent-a", "") // fresh boot: no continues
	if _, err := planOf(s, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("fresh session got a plan")
	}
	if _, err := planOf(s, "s1"); err != nil {
		t.Fatal("fresh session disturbed the older plan:", err)
	}
}

func TestCarryRefusedAcrossAgents(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s1", "agent-a", "")
	writePlan(t, s, "s1", "Mine")
	if _, err := s.Store.Carry(ctx, "s2", "agent-b", "s1"); !errors.Is(err, ErrAgentMismatch) {
		t.Fatalf("want ErrAgentMismatch, got %v", err)
	}
	s.Attach(ctx, "s3", "agent-b", "s1")
	if _, err := planOf(s, "s3"); !errors.Is(err, ErrNotFound) {
		t.Fatal("plan crossed agents")
	}
	if _, err := planOf(s, "s1"); err != nil {
		t.Fatal("refused carry still moved the plan")
	}
}

func TestUnrecordedAncestorCarriesNothing(t *testing.T) {
	s, _ := lineageSetup(t)
	writePlan(t, s, "legacy", "Pre-feature")
	s.Attach(context.Background(), "s2", "agent-a", "legacy")
	if _, err := planOf(s, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("an ancestor never seen through the proxy has no verifiable agent; nothing may move")
	}
}

func TestClearStopsResurrection(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s0", "agent-a", "")
	writePlan(t, s, "s0", "Old")
	s.Attach(ctx, "s1", "agent-a", "s0") // carried to s1
	ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "s1"}
	if err := s.Clear(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := planOf(s, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("clear left the plan")
	}
	s.Attach(ctx, "s2", "agent-a", "s1")
	if _, err := planOf(s, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a cleared plan came back through a replacement")
	}
	// A plan written after the clear is carried again.
	writePlan(t, s, "s1", "New")
	s.Attach(ctx, "s3", "agent-a", "s1")
	if p, err := planOf(s, "s3"); err != nil || *p.Title != "New" {
		t.Fatal("plan written after clear not carried:", p, err)
	}
}

func TestClearMarkerOnUnrecordedSessionBlocksWalk(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s0", "agent-a", "")
	writePlan(t, s, "s0", "Old")
	// "new conversation" on s1 before it ever touched a plan verb, then s2 continues s1 -> s0.
	if err := s.Clear(ctx, chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `UPDATE chat_session_lineage SET continues_id='s0' WHERE session_id='s1'`)
	s.Attach(ctx, "s2", "agent-a", "s1")
	if _, err := planOf(s, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("walk crossed a clear marker")
	}
}

func TestHopLimit(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "n0", "agent-a", "")
	writePlan(t, s, "n0", "Far")
	for i := 1; i <= MaxLineageHops; i++ {
		mustExec(t, s, fmt.Sprintf(`INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns) VALUES('n%d','agent-a','n%d',0,1)`, i, i-1))
	}
	s.Attach(ctx, "top", "agent-a", fmt.Sprintf("n%d", MaxLineageHops)) // n16 .. n0 is 17 hops
	if _, err := planOf(s, "top"); !errors.Is(err, ErrNotFound) {
		t.Fatal("walk exceeded the hop bound")
	}
}

func TestSessionsListsPlanWithoutTurns(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "s1", "agent-a", "")
	writePlan(t, s, "s1", "P")
	s.Attach(ctx, "s2", "agent-a", "")
	got := map[string]bool{}
	for _, st := range s.Store.Sessions(ctx) {
		got[st.Ref] = st.HasPlan
	}
	if !got["tether:s1"] || got["tether:s2"] {
		t.Fatalf("Sessions = %v", got)
	}
}

func mustExec(t *testing.T, s *Service, q string) {
	t.Helper()
	if err := s.Store.DB.Writer.Submit(context.Background(), "test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, q)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

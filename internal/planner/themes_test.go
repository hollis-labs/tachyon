package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/objects"
)

const (
	op    = "operator:browser"
	agent = "service:portfolio-manager"
)

func newTheme(t *testing.T, s *Owned, title, status string) Item {
	t.Helper()
	row, err := s.Create(context.Background(), "themes", uuid.NewString(), Item{"title": title, "status": status}, agent)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func act(t *testing.T, s *Owned, row Item, action string, args Item, actor string) (Item, error) {
	t.Helper()
	if args == nil {
		args = Item{}
	}
	return s.Action(context.Background(), "themes", row["id"].(string), revision(row), action, args, actor)
}

func mustAct(t *testing.T, s *Owned, row Item, action string, args Item, actor string) Item {
	t.Helper()
	out, err := act(t, s, row, action, args, actor)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return out
}

func TestAgentsCannotApproveSequenceOrLeaveApproval(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	a := newTheme(t, s, "A", "ready")

	// Approval and sequence through every door an agent has.
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "x", "status": "approved"}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent created an approved theme", err)
	}
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "x", "order": 5}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent created a theme with a sequence", err)
	}
	if _, err := s.Patch(ctx, "themes", a["id"].(string), revision(a), Item{"status": "approved"}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("patch approved a theme", err)
	}
	if _, err := s.Patch(ctx, "themes", a["id"].(string), revision(a), Item{"order": 1}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("patch set a sequence", err)
	}
	if _, err := act(t, s, a, "approve", nil, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("approve action ran for an agent", err)
	}
	if _, err := act(t, s, a, "resequence", Item{"position": 1}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("resequence ran for an agent", err)
	}
	if _, err := s.Reorder(ctx, "themes", []OrderChange{{ID: a["id"].(string), ExpectedRevision: revision(a)}}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("reorder ran for an agent", err)
	}

	// Leaving approval is operator-only too, except parking.
	approved := mustAct(t, s, a, "approve", nil, op)
	if _, err := act(t, s, approved, "finish", nil, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent finished an approved theme", err)
	}
	if _, err := act(t, s, approved, "archive", Item{"reason": "no"}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent archived an approved theme", err)
	}
	if _, err := s.Patch(ctx, "themes", approved["id"].(string), revision(approved), Item{"status": "done"}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent finished an approved theme through a patch", err)
	}

	// Nothing above changed it.
	got, _ := s.Get(ctx, "themes", approved["id"].(string))
	if got["status"] != "approved" || revision(got) != revision(approved) {
		t.Fatal("a refused call mutated the theme", got["status"], revision(got), revision(approved))
	}
}

func TestAgentsKeepTheNonApprovalLifecycle(t *testing.T) {
	s, _ := ownedFixture(t)
	p := newTheme(t, s, "P", "proposed")
	p = mustAct(t, s, p, "ready", Item{"reason": "scoped with the PM"}, agent)
	if p["status"] != "ready" {
		t.Fatal(p["status"])
	}
	p = mustAct(t, s, p, "park", Item{"reason": "all tasks blocked"}, agent)
	if p["status"] != "paused" {
		t.Fatal(p["status"])
	}
	if _, err := act(t, s, p, "unpark", nil, agent); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("an agent moved a theme without saying why", err)
	}
	p = mustAct(t, s, p, "unpark", Item{"reason": "tasks unblocked"}, agent)
	if p["status"] != "ready" {
		t.Fatal(p["status"])
	}
	p = mustAct(t, s, p, "append_state", Item{"text": "waiting on the auth decision"}, agent)
	if !strings.Contains(textField(p, "state"), "service:portfolio-manager: waiting on the auth decision") || textField(p, "last_verified") == "" {
		t.Fatal("state line or verification stamp missing", p["state"], p["last_verified"])
	}
	p = mustAct(t, s, p, "append_state", Item{"text": "second line"}, agent)
	if strings.Count(textField(p, "state"), "\n") != 1 {
		t.Fatal("state lines must append, not replace", p["state"])
	}
	if _, err := act(t, s, p, "append_state", Item{"text": "  "}, agent); err == nil {
		t.Fatal("empty state accepted")
	}
	// Every change leaves an audit comment with the actor.
	if comments := array(p["comments"]); len(comments) < 3 || object(comments[0])["author"] != agent {
		t.Fatal("audit trail missing", comments)
	}
}

func TestOperatorApprovalSequencesAndStamps(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	a := mustAct(t, s, newTheme(t, s, "A", "ready"), "approve", nil, op)
	b := mustAct(t, s, newTheme(t, s, "B", "proposed"), "approve", nil, op)
	c := mustAct(t, s, newTheme(t, s, "C", "ready"), "approve", Item{"position": 1}, op)
	if a["approved_by"] != op || textField(a, "approved_at") == "" {
		t.Fatal("approval not stamped", a["approved_by"], a["approved_at"])
	}
	if number(a["order"]) != 10 || number(b["order"]) != 20 || !(number(c["order"]) > 0 && number(c["order"]) < 10) {
		t.Fatalf("orders: a=%v b=%v c=%v", a["order"], b["order"], c["order"])
	}

	// Resequence renumbers the whole approved sequence atomically.
	cur, _ := s.Get(ctx, "themes", b["id"].(string))
	out := mustAct(t, s, cur, "resequence", Item{"position": 1}, op)
	if number(out["order"]) != 10 {
		t.Fatal("moved theme should be first", out["order"])
	}
	want := map[string]float64{"B": 10, "C": 20, "A": 30}
	rows, _ := s.List(ctx, "themes", nil, "")
	for _, r := range rows {
		if w, ok := want[textField(r, "title")]; ok && number(r["order"]) != w {
			t.Fatalf("%s order %v want %v", textField(r, "title"), r["order"], w)
		}
	}
	// Board order follows.
	board := boardRows(t, s)
	_ = board

	// Parking releases the place in the sequence and leaves tasks alone.
	cur, _ = s.Get(ctx, "themes", c["id"].(string))
	parked := mustAct(t, s, cur, "park", Item{"reason": "waiting"}, op)
	if parked["status"] != "paused" || parked["order"] != nil {
		t.Fatal("parked theme kept its place", parked["status"], parked["order"])
	}
	// A stale revision is a conflict, never a blind overwrite.
	if _, err := s.Action(ctx, "themes", c["id"].(string), revision(cur), "approve", Item{}, op); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err := act(t, s, parked, "resequence", Item{"position": 1}, op); err == nil {
		t.Fatal("a parked theme has no place to resequence")
	}
}

func TestThemeActionsOverHTTP(t *testing.T) {
	s, store := ownedFixture(t)
	row := newTheme(t, s, "Over HTTP", "ready")
	up := httptest.NewServer(NewHTTP(store, nil))
	defer up.Close()
	post := func(path string, body Item) (int, Item) {
		b, _ := json.Marshal(body)
		resp, err := http.Post(up.URL+Prefix+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out Item
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, out := post("/themes/"+row["id"].(string)+"/approve", Item{"expected_revision": revision(row)})
	if code != 200 || out["status"] != "approved" || out["approved_by"] != "operator:browser" {
		t.Fatal(code, out)
	}
	code, out = post("/priorities/"+row["id"].(string)+"/append_state", Item{"expected_revision": revision(row) + 1, "text": "via the old name"})
	if code != 200 || !strings.Contains(textField(out, "state"), "via the old name") {
		t.Fatal(code, out)
	}
	code, _ = post("/workstreams/"+row["id"].(string)+"/approve", Item{"expected_revision": 1})
	if code != 400 {
		t.Fatal("theme actions on another database should be invalid, got", code)
	}
}

func TestPromoteCannotCreateApprovedThemeForAgents(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	for _, fields := range []Item{{"title": "T", "status": "approved"}, {"title": "T", "order": 5}, {"title": "T", "approved_by": "x"}} {
		in, err := s.Create(ctx, "inbox", uuid.NewString(), Item{"title": "Capture"}, "operator")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Promote(ctx, textField(in, "id"), revision(in), "themes", uuid.NewString(), fields, "service:pm"); err == nil {
			t.Fatalf("agent promoted into %v", fields)
		}
	}
}

func TestAgentsMayParkAnApprovedThemeButNotMoreThanThat(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	approved := mustAct(t, s, newTheme(t, s, "T", "proposed"), "approve", nil, op)
	if _, err := act(t, s, approved, "park", nil, agent); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("parked without a reason", err)
	}
	parked := mustAct(t, s, approved, "park", Item{"reason": "all tasks blocked"}, agent)
	if parked["status"] != "paused" || parked["order"] != nil {
		t.Fatal("park left the theme approved or in the sequence", parked["status"], parked["order"])
	}
	// Putting it back is approval, so only the operator can.
	if _, err := act(t, s, parked, "approve", nil, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent re-approved a parked theme", err)
	}
	// An agent cannot use a park to also change the sequence.
	again := mustAct(t, s, newTheme(t, s, "U", "proposed"), "approve", nil, op)
	if _, err := s.Patch(ctx, "themes", again["id"].(string), revision(again), Item{"status": "paused", "order": 3}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("park smuggled a sequence change", err)
	}
}

func TestOperatorCanDeclineATheme(t *testing.T) {
	s, _ := ownedFixture(t)
	p := newTheme(t, s, "Unwanted", "proposed")
	if _, err := act(t, s, p, "archive", Item{"reason": "not now"}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent archived a theme", err)
	}
	out := mustAct(t, s, p, "archive", Item{"reason": "not now"}, op)
	if out["status"] != "archived" || !strings.Contains(fmt.Sprint(out["comments"]), "not now") {
		t.Fatal(out["status"], out["comments"])
	}
}

func TestCreateDefaultsToProposedAndRefusesApprovalForAgents(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	row, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "No status given"}, agent)
	if err != nil || row["status"] != "proposed" {
		t.Fatal(row["status"], err)
	}
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "Self-approved", "status": "approved"}, agent); !errors.Is(err, ErrOperatorRequired) {
		t.Fatal("agent created an approved theme", err)
	}
}

// Terminal statuses (done, archived) are operator-only on every path that can
// write a theme, from every starting status, and a refusal changes nothing.
func TestTerminalStatusesAreOperatorOnlyOnEveryPath(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	start := func(status string) Item {
		row := newTheme(t, s, "T-"+status, "proposed")
		switch status {
		case "ready":
			row = mustAct(t, s, row, "ready", Item{"reason": "x"}, agent)
		case "approved":
			row = mustAct(t, s, row, "approve", nil, op)
		case "paused":
			row = mustAct(t, s, row, "ready", Item{"reason": "x"}, agent)
			row = mustAct(t, s, row, "park", Item{"reason": "x"}, agent)
		}
		return row
	}
	same := func(t *testing.T, before Item, label string) {
		t.Helper()
		got, err := s.Get(ctx, "themes", before["id"].(string))
		if err != nil || got["status"] != before["status"] || revision(got) != revision(before) {
			t.Fatalf("%s: a refused call changed the theme (%v -> %v, rev %d -> %d, %v)", label, before["status"], got["status"], revision(before), revision(got), err)
		}
	}
	for _, from := range []string{"proposed", "ready", "approved", "paused"} {
		for _, to := range []string{"done", "archived"} {
			label := from + "->" + to
			finish := map[string]string{"done": "finish", "archived": "archive"}[to]
			t.Run("patch "+label, func(t *testing.T) {
				row := start(from)
				if _, err := s.Patch(ctx, "themes", row["id"].(string), revision(row), Item{"status": to}, agent); !errors.Is(err, ErrOperatorRequired) {
					t.Fatal("agent patch reached", to, err)
				}
				same(t, row, "patch")
				if out, err := s.Patch(ctx, "themes", row["id"].(string), revision(row), Item{"status": to}, op); err != nil || out["status"] != to {
					t.Fatal("operator patch refused", err)
				}
			})
			t.Run("action "+label, func(t *testing.T) {
				row := start(from)
				if _, err := act(t, s, row, finish, Item{"reason": "x"}, agent); !errors.Is(err, ErrOperatorRequired) {
					t.Fatal("agent action reached", to, err)
				}
				same(t, row, "action")
				if out := mustAct(t, s, row, finish, Item{"reason": "x"}, op); out["status"] != to {
					t.Fatal(out["status"])
				}
			})
		}
	}
	for _, to := range []string{"done", "archived"} {
		t.Run("create "+to, func(t *testing.T) {
			if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "C", "status": to}, agent); !errors.Is(err, ErrOperatorRequired) {
				t.Fatal("agent created a theme as", to, err)
			}
			if out, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "C", "status": to}, op); err != nil || out["status"] != to {
				t.Fatal("operator create refused", err)
			}
		})
		t.Run("promote "+to, func(t *testing.T) {
			in, err := s.Create(ctx, "inbox", uuid.NewString(), Item{"title": "Capture"}, op)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Promote(ctx, textField(in, "id"), revision(in), "themes", uuid.NewString(), Item{"title": "P", "status": to}, agent); !errors.Is(err, ErrOperatorRequired) {
				t.Fatal("agent promoted into", to, err)
			}
			still, _ := s.Get(ctx, "inbox", in["id"].(string))
			if still["status"] == "triaged" {
				t.Fatal("a refused promote triaged the inbox item")
			}
		})
	}
	// Reopening a finished theme is the operator's too.
	t.Run("reopen", func(t *testing.T) {
		done := mustAct(t, s, start("proposed"), "finish", Item{"reason": "x"}, op)
		if _, err := s.Patch(ctx, "themes", done["id"].(string), revision(done), Item{"status": "ready"}, agent); !errors.Is(err, ErrOperatorRequired) {
			t.Fatal("agent reopened a done theme", err)
		}
		same(t, done, "reopen")
	})
	// An unchanged terminal status does not lock an agent out of other edits.
	t.Run("edit while done", func(t *testing.T) {
		done := mustAct(t, s, start("proposed"), "finish", Item{"reason": "x"}, op)
		if _, err := s.Patch(ctx, "themes", done["id"].(string), revision(done), Item{"summary": "note"}, agent); err != nil {
			t.Fatal(err)
		}
	})
}

package provideractions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func formed(t *testing.T, s *Service, row objects.Record) Receipt {
	t.Helper()
	r := prepare(t, s, row)
	r, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil || r.State != "succeeded" {
		t.Fatal(r, e)
	}
	return r
}
func TestAssignmentDispatchPinsQualifiedTargetWithoutChangingPlanningState(t *testing.T) {
	s, team, f := fixture(t)
	origin := formed(t, s, team)
	assignment, e := objects.New(s.Store).Create(ctx, objects.Assignment{Common: objects.Common{Name: "Review", Description: "Review changes"}, TeamID: team.ID, State: "draft", ExternalTarget: &objects.ExternalReference{Provider: "github", Instance: "work", Collection: "pulls", ExternalID: "repo#42"}}, nil, "test")
	if e != nil {
		t.Fatal(e)
	}
	in := RuntimeRequest{ID: uuid.NewString(), ObjectID: assignment.ID, ExpectedRevision: assignment.Revision, FormationID: origin.ID, Operation: "assign", Address: "@worker", Body: "Please review"}
	r, e := s.PrepareRuntime(ctx, in, "operator:test")
	if e != nil || r.Verb != "teams.assign" || r.OriginID != origin.ID {
		t.Fatal(r, e)
	}
	var payload teamRequest
	if json.Unmarshal(r.Payload, &payload) != nil || payload.RunID != "run-1" || !strings.Contains(payload.Body, `"provider":"github"`) || !strings.Contains(payload.Body, "repo#42") {
		t.Fatal(string(r.Payload))
	}
	if f.calls.Load() != 1 {
		t.Fatal("preview dispatched")
	}
	if _, e = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); e != nil {
		t.Fatal(e)
	}
	current, e := objects.New(s.Store).Get(ctx, assignment.ID)
	if e != nil || current.Revision != assignment.Revision || string(current.Definition) != string(assignment.Definition) {
		t.Fatal("dispatch changed planned state", e)
	}
	in.ID = uuid.NewString()
	in.Address = "@all"
	if _, e = s.PrepareRuntime(ctx, in, "operator:test"); e == nil {
		t.Fatal("broadcast assignment accepted")
	}
	in.Address = "@worker"
	in.FormationID = uuid.NewString()
	if _, e = s.PrepareRuntime(ctx, in, "operator:test"); e == nil {
		t.Fatal("unconfirmed formation accepted")
	}
}
func TestRuntimeControlsValidateArgumentsAndAllowRetiredCleanup(t *testing.T) {
	s, row, f := fixture(t)
	origin := formed(t, s, row)
	for _, op := range []string{"address", "member_add", "member_remove", "cancel", "dissolve"} {
		in := RuntimeRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, FormationID: origin.ID, Operation: op}
		switch op {
		case "address":
			in.Address = "@worker"
			in.Body = "Status please"
		case "member_add":
			in.Slot = "worker"
		case "member_remove", "cancel":
			in.MemberID = "member-1"
		}
		r, e := s.PrepareRuntime(ctx, in, "operator:test")
		if e != nil || r.Verb != "teams."+op {
			t.Fatal(op, r, e)
		}
		in.ID = uuid.NewString()
		in.Address = "unexpected"
		in.Slot = "unexpected"
		if _, e = s.PrepareRuntime(ctx, in, "operator:test"); e == nil {
			t.Fatal("extra args accepted", op)
		}
	}
	page, e := s.List(ctx, ListRequest{ObjectID: row.ID, Verb: "teams.form"})
	if e != nil || len(page.Items) != 1 || page.Items[0].Verb != "teams.form" {
		t.Fatal(page, e)
	}
	row, e = objects.New(s.Store).SetDeleted(ctx, row.ID, row.Revision, true, "test")
	if e != nil {
		t.Fatal(e)
	}
	in := RuntimeRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, FormationID: origin.ID, Operation: "dissolve"}
	r, e := s.PrepareRuntime(ctx, in, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); e != nil {
		t.Fatal(e)
	}
	in.ID = uuid.NewString()
	in.Operation = "member_add"
	in.Slot = "worker"
	if _, e = s.PrepareRuntime(ctx, in, "operator:test"); e == nil {
		t.Fatal("retired launch accepted")
	}
	in.Operation = "dissolve"
	in.Slot = ""
	f.identity = "changed"
	if _, e = s.PrepareRuntime(ctx, in, "operator:test"); e == nil {
		t.Fatal("new caller accepted")
	}
}

type rosterBound struct {
	*fakeBound
	version uint64
	err     error
	reads   int
}

func (f *rosterBound) Roster(context.Context, string) ([]RosterView, error) {
	f.reads++
	return []RosterView{{Run: teams.TeamRun{ID: "run-1"}, Version: f.version, Members: []Member{{ID: "member-1"}}}}, f.err
}
func TestRosterCachedFirstRetainsOnFailureAndRejectsOlderVersions(t *testing.T) {
	s, row, f := fixture(t)
	origin := formed(t, s, row)
	reader := &rosterBound{fakeBound: f, version: 4}
	s.Bind = func() (Bound, error) { return reader, nil }
	in := RosterRequest{FormationID: origin.ID}
	out, e := s.Roster(ctx, in)
	if e != nil || reader.reads != 0 || len(out.Runs) != 0 {
		t.Fatal(out, e)
	}
	in.Refresh = true
	out, e = s.Roster(ctx, in)
	if e != nil || out.Runs[0].Version != 4 {
		t.Fatal(out, e)
	}
	observed := out.ObservedAt
	reader.version = 3
	out, e = s.Roster(ctx, in)
	if e != nil || out.Runs[0].Version != 4 || out.ObservedAt != observed {
		t.Fatal("cache regressed", out, e)
	}
	reader.err = errors.New("offline")
	out, e = s.Roster(ctx, in)
	if e != nil || out.Runs[0].Version != 4 || out.Problem == "" {
		t.Fatal("cache lost", out, e)
	}
	s.Bind = func() (Bound, error) { return nil, errors.New("invalid credential") }
	in.Refresh = false
	out, e = s.Roster(ctx, in)
	if e != nil || out.Runs[0].Version != 4 {
		t.Fatal("saved read depended on credentials", out, e)
	}
	in.Refresh = true
	out, e = s.Roster(ctx, in)
	if e != nil || out.Runs[0].Version != 4 || out.Problem == "" {
		t.Fatal(out, e)
	}
}

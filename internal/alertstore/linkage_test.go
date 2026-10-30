package alertstore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

var linkPack = []byte(`{"version":1,"id":"t","rules":[
 {"id":"drop-clear","match":{"kinds":["alert.cleared"]},"action":"drop"},
 {"id":"appr","match":{"kinds":["approval.requested"]},"alert":{"title":"Approve?","severity":"warning","needsYou":true}},
 {"id":"raised","match":{"kinds":["alert.raised"]},"alert":{"title":"Raised","severity":"error"},"coalesce":{"key":"raised:{{subject.id}}"}}]}`)

func linkStore(t *testing.T) *Store {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pack, _ := alertsrules.ParsePack(linkPack)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	return &Store{DB: db, Pack: func() alertsrules.Pack { return pack }, Now: func() time.Time { return now }}
}

func approvalEvent(id, item string) alertsource.Event {
	return alertsource.Event{Source: "approvals", SourceEventID: id, Kind: "approval.requested", AtMS: 1000,
		Subjects: []alertsapi.Subject{{Kind: ApprovalSubject, Ref: item, Label: "Item"}},
		Payload:  map[string]any{"class": "medium", "api_token": "abc", "note": "Bearer abcdef0123456789", "nested": map[string]any{"a": 1}, "n": 3.0}}
}

func TestApprovalAlertSettlesAndShowsBlock(t *testing.T) {
	s := linkStore(t)
	s.ApprovalState = func(_ context.Context, id string) string { return "presented" }
	_ = s.Ingest(approvalEvent("e1", "item-1"))
	_ = s.Ingest(approvalEvent("e2", "item-2"))
	as, _, _ := s.List(context.Background(), ListQuery{View: "active", Limit: 10})
	if len(as) != 2 {
		t.Fatalf("%d", len(as))
	}
	var one alertsapi.Alert
	for _, a := range as {
		if a.Subjects[0].Ref == "item-1" {
			one = a
		}
	}
	d, err := s.Detail(context.Background(), one.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := d.Approval.(map[string]any)
	if blk["item_id"] != "item-1" || blk["state"] != "presented" || len(d.Actions) == 0 || d.Actions[len(d.Actions)-1]["kind"] != "approval" {
		t.Fatalf("%+v", d)
	}
	fields, _ := d.Technical["fields"].([]map[string]string)
	got := map[string]string{}
	for _, f := range fields {
		got[f["label"]] = f["value"]
	}
	if _, bad := got["api_token"]; bad || got["class"] != "medium" || got["n"] != "3" || strings.Contains(got["note"], "abcdef") || got["nested"] != "" {
		t.Fatalf("technical fields leaked or missing: %v", got)
	}
	n, err := s.SettleApproval(context.Background(), "item-1", "approved")
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	a, _ := s.Get(context.Background(), one.AlertID)
	if a.Status != "settled" || a.Resolution != "Approved" || a.ResolvedBy != "approvals" || !a.Read {
		t.Fatalf("%+v", a)
	}
	if n, _ := s.SettleApproval(context.Background(), "item-1", "approved"); n != 0 {
		t.Fatal("already settled")
	}
	other, _ := s.Get(context.Background(), as[0].AlertID)
	if other.AlertID != one.AlertID && other.Status != "active" {
		t.Fatal("the other item's alert is untouched")
	}
}

type fakeFeed struct{ ch chan ApprovalChange }

func (f fakeFeed) Subscribe() (<-chan ApprovalChange, func()) { return f.ch, func() {} }

func TestFollowApprovals(t *testing.T) {
	s := linkStore(t)
	_ = s.Ingest(approvalEvent("e1", "item-1"))
	feed := fakeFeed{make(chan ApprovalChange, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.FollowApprovals(ctx, feed, nil); close(done) }()
	feed.ch <- ApprovalChange{Type: "item.updated", ItemID: "item-1", State: "presented"} // ignored
	feed.ch <- ApprovalChange{Type: "item.expired", ItemID: "item-1", State: "expired"}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		as, _, _ := s.List(context.Background(), ListQuery{View: "history", Limit: 10})
		if len(as) == 1 && as[0].Resolution == "Expired" {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the expired item did not settle its alert")
}

func TestAlertClearedSettlesByKey(t *testing.T) {
	s := linkStore(t)
	sub := alertsapi.Subject{Kind: "svc", Ref: "db", Label: "db"}
	_ = s.Ingest(alertsource.Event{Source: "x", SourceEventID: "r1", Kind: "alert.raised", AtMS: 1000, Subjects: []alertsapi.Subject{sub}})
	as, _, _ := s.List(context.Background(), ListQuery{View: "active", Limit: 10})
	if len(as) != 1 {
		t.Fatalf("%d", len(as))
	}
	_ = s.Ingest(alertsource.Event{Source: "x", SourceEventID: "c1", Kind: "alert.cleared", AtMS: 2000, Subjects: []alertsapi.Subject{sub}})
	a, _ := s.Get(context.Background(), as[0].AlertID)
	if a.Status != "settled" || a.Resolution != "Cleared by source" {
		t.Fatalf("%+v", a)
	}
}

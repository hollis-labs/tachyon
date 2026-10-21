package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

var ctx = context.Background()

func setup(t *testing.T) (*Local, *objects.Service, *localstore.Store) {
	t.Helper()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := objects.New(db)
	return NewLocal(s), s, db
}
func call[T any](t *testing.T, p Provider, verb string, input any) T {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Invoke(ctx, verb, raw, "test:operator")
	if err != nil {
		t.Fatalf("%s: %v", verb, err)
	}
	var decoded T
	if err = json.Unmarshal(result, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
func createRecord(t *testing.T, p Provider, kind objects.Kind, definition string) objects.Record {
	return call[objects.Record](t, p, "objects.create", CreateRequest{ID: uuid.NewString(), Kind: kind, Definition: json.RawMessage(definition)})
}
func TestLocalVerbsCRUDHistoryAndPagination(t *testing.T) {
	p, s, _ := setup(t)
	req := CreateRequest{ID: uuid.NewString(), Kind: objects.InboxItemKind, Definition: json.RawMessage(`{"name":"Capture","status":"new","priority":2,"properties":{"integer":9007199254740993,"a":"lower","A":"upper"}}`), Metadata: json.RawMessage(`{"custom":9007199254740995}`)}
	created := call[objects.Record](t, p, "objects.create", req)
	if created.Ownership != "tachyon" || !strings.Contains(string(created.Definition), "9007199254740993") || !strings.Contains(string(created.Metadata), "9007199254740995") {
		t.Fatal("content changed", created)
	}
	retried := call[objects.Record](t, p, "objects.create", req)
	if retried.ID != created.ID || retried.Revision != created.Revision {
		t.Fatal("duplicate create")
	}
	got := call[objects.Record](t, p, "objects.get", IDRequest{ID: created.ID})
	if string(got.Definition) != string(created.Definition) {
		t.Fatal("get diverged")
	}
	second := createRecord(t, p, objects.InboxItemKind, `{"name":"Second","status":"new"}`)
	page := call[RecordsPage](t, p, "objects.list", objects.Filter{Kind: objects.InboxItemKind, Limit: 1})
	next := call[RecordsPage](t, p, "objects.list", objects.Filter{Kind: objects.InboxItemKind, Limit: 1, AfterID: page.NextAfterID})
	if len(page.Items) != 1 || len(next.Items) != 1 || page.Items[0].ID == next.Items[0].ID || next.NextAfterID == "" {
		t.Fatal("pagination skipped/repeated")
	}
	empty := call[RecordsPage](t, p, "objects.list", objects.Filter{Kind: objects.InboxItemKind, Limit: 1, AfterID: next.NextAfterID})
	if len(empty.Items) != 0 || empty.NextAfterID != "" {
		t.Fatal("pagination did not end")
	}
	patched := call[objects.Record](t, p, "objects.patch", PatchRequest{RevisionRequest: RevisionRequest{ID: created.ID, ExpectedRevision: created.Revision}, Definition: json.RawMessage(`{"status":"triaged","description":"Reviewed"}`)})
	query := call[RecordsPage](t, p, "planner.list", objects.PlannerFilter{Kind: objects.InboxItemKind, Status: "triaged"})
	if len(query.Items) != 1 || query.Items[0].ID != created.ID {
		t.Fatal("planner query ignored edit")
	}
	deleted := call[objects.Record](t, p, "objects.delete", RevisionRequest{ID: patched.ID, ExpectedRevision: patched.Revision})
	if !deleted.Deleted() {
		t.Fatal("not deleted")
	}
	query = call[RecordsPage](t, p, "planner.list", objects.PlannerFilter{Kind: objects.InboxItemKind})
	if len(query.Items) != 1 || query.Items[0].ID != second.ID {
		t.Fatal("deleted record visible")
	}
	restored := call[objects.Record](t, p, "objects.restore", RevisionRequest{ID: deleted.ID, ExpectedRevision: deleted.Revision})
	if restored.Deleted() {
		t.Fatal("restore failed")
	}
	history := call[HistoryPage](t, p, "objects.history", HistoryRequest{ID: restored.ID, Limit: 2})
	tail := call[HistoryPage](t, p, "objects.history", HistoryRequest{ID: restored.ID, AfterRevision: history.NextAfterRevision, Limit: 2})
	if len(history.Items) != 2 || len(tail.Items) != 2 || tail.Items[1].Operation != "restore" || history.Items[0].Actor != "test:operator" {
		t.Fatal("history/actor lost")
	}
	stored, err := s.Get(ctx, created.ID)
	if err != nil || stored.Revision != restored.Revision {
		t.Fatal("verb did not use domain service", err)
	}
}
func TestLocalVerbsRelationshipsPromotionAndExecution(t *testing.T) {
	p, s, _ := setup(t)
	source := createRecord(t, p, objects.InboxItemKind, `{"name":"Idea capture","status":"new"}`)
	request := objects.PromotionRequest{SourceID: source.ID, ExpectedRevision: source.Revision, TargetID: uuid.NewString(), TargetKind: objects.PlanKind}
	result := call[objects.PromotionResult](t, p, "planner.promote", request)
	repeat := call[objects.PromotionResult](t, p, "planner.promote", request)
	if result.Receipt != repeat.Receipt {
		t.Fatal("promotion retry diverged")
	}
	receipt := call[objects.PromotionReceipt](t, p, "planner.promotion.get", IDRequest{ID: source.ID})
	if receipt.TargetID != result.Target.ID {
		t.Fatal("wrong receipt")
	}
	links := call[LinksResult](t, p, "objects.links", LinksRequest{ID: source.ID})
	if len(links.Items) != 1 || links.Items[0].Relation != "promoted_to" {
		t.Fatal("missing provenance")
	}
	collection := createRecord(t, p, objects.CollectionKind, `{"name":"Work","status":"active"}`)
	collection = call[objects.Record](t, p, "collections.members.set", SetMembersRequest{RevisionRequest: RevisionRequest{ID: collection.ID, ExpectedRevision: collection.Revision}, Members: []objects.Reference{{ID: source.ID, Kind: source.Kind}, {ID: result.Target.ID, Kind: result.Target.Kind}}})
	page := call[objects.CollectionPage](t, p, "collections.members.list", MembersRequest{ID: collection.ID, Limit: 1})
	if page.NextOffset == nil || page.Members[0].Record.ID != source.ID {
		t.Fatal("membership order")
	}
	last := call[objects.CollectionPage](t, p, "collections.members.list", MembersRequest{ID: collection.ID, ExpectedRevision: page.Revision, Offset: *page.NextOffset, Limit: 1})
	if len(last.Members) != 1 || last.Members[0].Record.ID != result.Target.ID || last.NextOffset != nil {
		t.Fatal("membership continuation")
	}
	call[objects.Record](t, p, "collections.members.set", SetMembersRequest{RevisionRequest: RevisionRequest{ID: collection.ID, ExpectedRevision: collection.Revision}, Members: nil})
	session := createRecord(t, p, objects.SessionKind, `{"name":"Execution","status":"unknown"}`)
	execution := call[RecordsPage](t, p, "executions.list", objects.ExecutionFilter{Kind: objects.SessionKind, Status: "unknown"})
	if len(execution.Items) != 1 || execution.Items[0].ID != session.ID {
		t.Fatal("execution query")
	}
	key := objects.SourceKey{SourceScope: objects.SourceScope{Provider: "test", Instance: "host", Collection: "projects", Kind: objects.ProjectKind}, ExternalID: "project-a"}
	remote, err := s.UpdateOrCreate(ctx, objects.Observation{Key: key, ObservedAt: source.CreatedAt, Definition: json.RawMessage(`{"name":"Remote project"}`)}, "sync:test")
	if err != nil {
		t.Fatal(err)
	}
	lookup := call[objects.Record](t, p, "objects.lookup_source", key)
	if lookup.ID != remote.ID {
		t.Fatal("source identity")
	}
	owned := call[objects.Record](t, p, "objects.take_ownership", RevisionRequest{ID: remote.ID, ExpectedRevision: remote.Revision})
	if owned.Ownership != "tachyon" || owned.Source == nil {
		t.Fatal("adoption lost provenance")
	}
}
func TestLocalVerbFailuresAndDiscovery(t *testing.T) {
	p, _, db := setup(t)
	record := createRecord(t, p, objects.ProjectKind, `{"name":"Project"}`)
	for _, test := range []struct{ verb, input, code string }{
		{"objects.get", `{}`, "invalid"},
		{"objects.get", `{"id":null}`, "invalid"},
		{"objects.get", `{"id":" "}`, "invalid"},
		{"objects.get", `{"id":"missing","actor":"admin"}`, "invalid"},
		{"objects.get", `{"id":"a","id":"b"}`, "invalid"},
		{"objects.list", `{"Limit":1}`, "invalid"},
		{"objects.list", `{"limit":-1}`, "invalid"},
		{"objects.list", `{"limit":null}`, "invalid"},
		{"objects.list", `{"limit":501}`, "invalid"},
		{"objects.list", `{"limit":"1"}`, "invalid"},
		{"objects.list", `null`, "invalid"},
		{"objects.list", `[]`, "invalid"},
		{"objects.list", `{} {}`, "invalid"},
		{"objects.list", `{"kind":"made_up"}`, "invalid"},
		{"planner.list", `{"kind":"agent"}`, "invalid"},
		{"executions.list", `{"kind":"task"}`, "invalid"},
		{"objects.get", `{"id":"missing"}`, "not_found"},
		{"objects.history", `{"id":"missing"}`, "not_found"},
		{"objects.links", `{"id":"missing"}`, "not_found"},
		{"planner.promotion.get", `{"id":"missing"}`, "not_found"},
		{"objects.patch", `{"id":"` + record.ID + `","expected_revision":0}`, "invalid"},
		{"objects.patch", `{"id":"` + record.ID + `","expected_revision":99}`, "conflict"},
		{"objects.create", `{"id":"` + record.ID + `","kind":"project","definition":{"name":"Different"}}`, "conflict"},
		{"objects.create", `{"id":"` + uuid.NewString() + `","kind":"project","definition":{"name":"P","unknown":true}}`, "invalid"},
		{"objects.create", `{"id":"` + uuid.NewString() + `","kind":"project","definition":{"name":"P","properties":{"x":1,"x":2}}}`, "invalid"},
		{"collections.members.set", `{"id":"` + record.ID + `","expected_revision":1}`, "invalid"},
		{"objects.reconcile", `{}`, "not_found"},
	} {
		_, err := p.Invoke(ctx, test.verb, json.RawMessage(test.input), "test:operator")
		if string(svcerr.CodeFor(err)) != test.code {
			t.Errorf("%s %s: %v", test.verb, test.input, err)
		}
	}
	_, err := p.Invoke(ctx, "objects.delete", json.RawMessage(`{"id":"`+record.ID+`","expected_revision":1}`), "")
	if !errors.Is(err, svcerr.ErrPermission) {
		t.Fatal("unattributed write accepted", err)
	}
	for _, verb := range p.Verbs() {
		if !verb.Mutates {
			continue
		}
		if _, err := p.Invoke(ctx, verb.Name, json.RawMessage(`{}`), ""); !errors.Is(err, svcerr.ErrPermission) {
			t.Fatalf("unguarded verb %s", verb.Name)
		}
	}
	catalog := p.Verbs()
	catalog[0].InputSchema["type"] = "corrupted"
	if p.Verbs()[0].InputSchema["type"] != "object" {
		t.Fatal("catalog mutation leaked")
	}
	if err = db.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = p.Invoke(ctx, "objects.get", json.RawMessage(`{"id":"`+record.ID+`"}`), "")
	var safe *svcerr.Error
	if !errors.As(err, &safe) || safe.Code != svcerr.CodeInternal || strings.Contains(safe.Message, "database") {
		t.Fatal("internal error exposed", err)
	}
}

// Free-form object parameters must be advertised as objects. They were once
// typed as strings, so clients generated from the schema (MCP wrappers) sent
// JSON text that the server rejects with "Invalid command fields or field types".
func TestFreeFormObjectParametersAreAdvertisedAsObjects(t *testing.T) {
	p, _, _ := setup(t)
	want := map[string][]string{
		"planner.portfolio.create":  {"fields"},
		"planner.portfolio.patch":   {"patch"},
		"planner.portfolio.action":  {"arguments"},
		"planner.portfolio.list":    {"filters"},
		"planner.portfolio.promote": {"fields"},
		"planner.promote":           {"overrides"},
	}
	seen := 0
	for _, verb := range p.Verbs() {
		props, _ := verb.InputSchema["properties"].(map[string]any)
		for _, name := range want[verb.Name] {
			seen++
			if got := props[name].(map[string]any)["type"]; got != "object" {
				t.Errorf("%s.%s advertised as %v, want object", verb.Name, name, got)
			}
		}
	}
	if seen != 6 {
		t.Fatalf("checked %d of 6 parameters", seen)
	}
}

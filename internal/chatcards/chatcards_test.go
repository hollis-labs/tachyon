package chatcards

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func setup(t *testing.T) (*Binder, *approvals.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := &approvals.Store{DB: db}
	return &Binder{Refs: &Store{DB: db}, Get: st.Get, Changes: approvals.Changes(db)}, st, ctx
}

func question(t *testing.T, st *approvals.Store, ctx context.Context, id string) hitl.Record {
	now := time.Now().UTC()
	exp := now.Add(time.Hour)
	rec := hitl.Record{ItemID: id, CallerScope: "t", IdempotencyKey: "k" + id, Digest: "d" + id, Kind: "card.question",
		Request: []byte(`{"kind":"card.question","title":"Which project?","class":"low","data":{"options":[{"value":"a","label":"A"}]}}`),
		State:   hitl.StatePresented, Revision: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: &exp}
	if _, _, err := st.Create(ctx, rec); err != nil {
		t.Skipf("cannot build a queue record: %v", err)
	}
	return rec
}

func TestBindProjectAndCount(t *testing.T) {
	b, st, ctx := setup(t)
	question(t, st, ctx, "i1")
	var events []string
	b.Publish = func(_ context.Context, th, typ string, f map[string]any) { events = append(events, th+typ) }
	if err := b.Bind(ctx, "i1", "card.question", "app:t1", "m_3"); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(ctx, "i1", "card.question", "app:t1", ""); err != nil {
		t.Fatalf("rebinding to the same thread is a no-op: %v", err)
	}
	if err := b.Bind(ctx, "i1", "card.question", "app:t2", ""); err == nil {
		t.Fatal("binding to another thread must fail")
	}
	if len(events) == 0 {
		t.Fatal("no card event published")
	}
	svc := &Service{Refs: b.Refs, Get: b.Get}
	cs, err := svc.Cards(ctx, "app:t1", "", 0)
	if err != nil || len(cs) != 1 {
		t.Fatalf("cards: %v %v", cs, err)
	}
	c := cs[0]
	if c.State != "pending" || c.Class != "low" || c.Caption != "Which project?" || len(c.Fields) != 2 || c.MessageRef == nil || *c.MessageRef != "m_3" {
		t.Fatalf("projection: %+v", c)
	}
	if n := b.OpenCounts(ctx, []string{"app:t1", "app:t2"}); n["app:t1"] != 1 || n["app:t2"] != 0 {
		t.Fatalf("open counts: %v", n)
	}
	h := Handler(svc, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins/console/v1/chat/threads/app:t1/cards?state=resolved", nil))
	var body struct{ Cards []Card }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Cards) != 0 {
		t.Fatalf("state filter: %d %s", w.Code, w.Body)
	}
}

func TestAnswerText(t *testing.T) {
	c := Card{ItemID: "i1", Caption: "Which project?", Result: map[string]any{"decision": "submitted", "answers": []any{map[string]any{"id": "answer", "value": "a"}}}}
	got := AnswerText(c)
	want := `Card answered: "Which project?" (i1). Decision: submitted. Answers: id=answer, value=a.`
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestApprovalRules(t *testing.T) {
	b, _, ctx := setup(t)
	s := b.Refs
	if _, err := s.AddRule(ctx, "app:t1", "", "bash", "medium", nil, nil); err != ErrNotLow {
		t.Fatalf("a medium approval must not become a rule: %v", err)
	}
	r, err := s.AddRule(ctx, "app:t1", "a1", "read_file", "low", map[string]any{"path": "/x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Allows(ctx, "app:t1", "a1", "read_file", "low", map[string]any{"path": "/x", "extra": 1}) {
		t.Fatal("matching call should be allowed")
	}
	for name, ok := range map[string]bool{
		"other path":  s.Allows(ctx, "app:t1", "a1", "read_file", "low", map[string]any{"path": "/y"}),
		"other agent": s.Allows(ctx, "app:t1", "a2", "read_file", "low", map[string]any{"path": "/x"}),
		"other chat":  s.Allows(ctx, "app:t2", "a1", "read_file", "low", map[string]any{"path": "/x"}),
		"medium":      s.Allows(ctx, "app:t1", "a1", "read_file", "medium", map[string]any{"path": "/x"}),
	} {
		if ok {
			t.Errorf("%s must not be allowed", name)
		}
	}
	h := Handler(&Service{Refs: s, Get: b.Get}, nil)
	base := "/api/plugins/console/v1/chat/threads/app:t1/approval-rules"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), r.ID) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", base+"/"+r.ID, nil))
	if w.Code != 200 || s.Allows(ctx, "app:t1", "a1", "read_file", "low", map[string]any{"path": "/x"}) {
		t.Fatalf("revoke: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", base+"/"+r.ID, nil))
	if w.Code != 404 {
		t.Fatalf("second revoke: %d", w.Code)
	}
}

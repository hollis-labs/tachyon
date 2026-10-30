package dataverbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/chattopics"
	"github.com/hollis-labs/tachyon/internal/chatworkspace"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func chatFixture(t *testing.T) (*scopedLocal, *chatworkspace.Store) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := &chatworkspace.Store{DB: db, Dir: filepath.Join(t.TempDir(), "workspace")}
	p := &scopedLocal{
		local:     NewLocal(nil),
		principal: auth.Principal{Verified: true, ID: "agent", Scopes: []string{"verb:chat.workspace.list", "verb:chat.workspace.get"}},
		chat: &ChatWorkspace{Store: st, Thread: func(_ context.Context, ref string) error {
			if ref == "app:ghost" {
				return errors.New("none")
			}
			return nil
		}},
	}
	return p, st
}

func addText(t *testing.T, st *chatworkspace.Store, thread, name, mime string, body []byte, shared bool) chatworkspace.Item {
	t.Helper()
	it := chatworkspace.Item{ID: "ws_" + name, Type: "file", Name: name, Mime: &mime, SizeBytes: int64p(int64(len(body))), Shared: shared, AddedBy: "user"}
	if err := os.MkdirAll(filepath.Dir(st.Path(thread, it.ID)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path(thread, it.ID), body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := st.Add(context.Background(), thread, it)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func int64p(n int64) *int64 { return &n }

func chatCall(p *scopedLocal, verb, args string) (map[string]any, error) {
	raw, err := p.Invoke(context.Background(), verb, json.RawMessage(args), "")
	if err != nil {
		return nil, err
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m, nil
}

func TestChatWorkspaceVerbs(t *testing.T) {
	p, st := chatFixture(t)
	ctx := context.Background()
	addText(t, st, "app:t1", "shared.md", "text/markdown", []byte("héllo"), true)
	addText(t, st, "app:t1", "private.txt", "text/plain", []byte("secret"), false)
	addText(t, st, "app:t1", "big.txt", "text/plain", bytes.Repeat([]byte("é"), 20000), true)
	addText(t, st, "app:t1", "doc.pdf", "application/pdf", []byte("%PDF-1.7"), true)
	link := "https://example.invalid/x"
	if _, err := st.Add(ctx, "app:t1", chatworkspace.Item{ID: "ws_link", Type: "link", Name: "Design", URL: &link, Shared: true, AddedBy: "user"}); err != nil {
		t.Fatal(err)
	}

	out, err := chatCall(p, "chat.workspace.list", `{"thread_ref":"app:t1"}`)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range out["items"].([]any) {
		names[v.(map[string]any)["name"].(string)] = true
	}
	if len(names) != 4 || names["private.txt"] {
		t.Fatalf("list must show shared items only: %v", names)
	}
	out, err = chatCall(p, "chat.workspace.get", `{"thread_ref":"app:t1","id":"ws_shared.md"}`)
	if err != nil || out["text"] != "héllo" || out["truncated"] != false {
		t.Fatal(out, err)
	}
	// Cut at max_bytes without splitting a rune.
	out, err = chatCall(p, "chat.workspace.get", `{"thread_ref":"app:t1","id":"ws_big.txt","max_bytes":101}`)
	if err != nil || out["truncated"] != true || len(out["text"].(string)) != 100 || !strings.HasPrefix(out["text"].(string), "é") {
		t.Fatalf("truncate: %v %v", out, err)
	}
	if out, _ = chatCall(p, "chat.workspace.get", `{"thread_ref":"app:t1","id":"ws_big.txt"}`); len(out["text"].(string)) > 32<<10 {
		t.Fatal("default cap exceeded")
	}
	if out, _ = chatCall(p, "chat.workspace.get", `{"thread_ref":"app:t1","id":"ws_link"}`); out["url"] != link {
		t.Fatalf("link: %v", out)
	}
	for name, args := range map[string]string{
		"unshared":  `{"thread_ref":"app:t1","id":"ws_private.txt"}`,
		"binary":    `{"thread_ref":"app:t1","id":"ws_doc.pdf"}`,
		"missing":   `{"thread_ref":"app:t1","id":"nope"}`,
		"other":     `{"thread_ref":"app:t2","id":"ws_shared.md"}`,
		"tether":    `{"thread_ref":"tether:s1","id":"ws_shared.md"}`,
		"ghost":     `{"thread_ref":"app:ghost","id":"ws_shared.md"}`,
		"malformed": `{"thread_ref":"nope","id":"x"}`,
		"unknown":   `{"thread_ref":"app:t1","id":"ws_shared.md","extra":1}`,
	} {
		if _, err := chatCall(p, "chat.workspace.get", args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := chatCall(p, "chat.workspace.list", `{"thread_ref":"tether:s1"}`); err == nil {
		t.Error("tether list should be refused")
	}
}

func TestChatWorkspaceVerbsNeedTheirGrant(t *testing.T) {
	p, _ := chatFixture(t)
	p.principal.Scopes = []string{"verb:card.ask"}
	if _, err := chatCall(p, "chat.workspace.list", `{"thread_ref":"app:t1"}`); err == nil {
		t.Fatal("verb ran without a grant")
	}
	if len(p.Verbs()) != 0 {
		t.Fatalf("verbs listed without a grant: %v", p.Verbs())
	}
	p.principal.Scopes = []string{"verb:chat.workspace.get"}
	if v := p.Verbs(); len(v) != 1 || v[0].Name != "chat.workspace.get" || v[0].Mutates {
		t.Fatalf("verbs: %v", v)
	}
	p.chat = nil
	if len(p.Verbs()) != 0 {
		t.Fatal("verbs listed while the feature is off")
	}
}

func TestChatWorkspacePreset(t *testing.T) {
	g := agentaccess.ChatWorkspaceRead()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if !agentaccess.ChatWorkspaceVerb("chat.workspace.get") || !agentaccess.SupportedVerb("chat.workspace.list") {
		t.Fatal("verbs not registered")
	}
	// No existing preset gained them.
	for name, grants := range map[string]agentaccess.Grants{"app-agent-read": agentaccess.AppAgentRead(), "planner-read": agentaccess.PlannerRead(), "card-ask": agentaccess.CardAsk()} {
		for _, v := range grants.Verbs {
			if agentaccess.ChatWorkspaceVerb(v) {
				t.Errorf("%s widened with %s", name, v)
			}
		}
	}
}

func TestChatTopicAddVerb(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var published []string
	topics := &ChatTopics{
		Store: &chattopics.Store{DB: db},
		Thread: func(_ context.Context, ref string) error {
			if ref == "app:ghost" {
				return errors.New("none")
			}
			return nil
		},
		MessageSeq: func(_ context.Context, _ chatapi.ThreadRef, m string) (int64, error) {
			if m == "m1" {
				return 7, nil
			}
			return 0, errors.New("no")
		},
		Published: func(_ context.Context, thread, op, id string) { published = append(published, thread+" "+op) },
	}
	p := &scopedLocal{local: NewLocal(nil), topics: topics, principal: auth.Principal{Verified: true, ID: "agent", Scopes: []string{"verb:chat.topic.add"}}}
	out, err := chatCall(p, "chat.topic.add", `{"thread_ref":"app:t1","title":"Design","message_ref":"m1"}`)
	if err != nil || out["added_by"] != "agent" || out["seq"] != 7.0 || out["title"] != "Design" {
		t.Fatal(out, err)
	}
	if len(published) != 1 || published[0] != "app:t1 add" {
		t.Fatalf("published: %v", published)
	}
	for name, args := range map[string]string{
		"tether":  `{"thread_ref":"tether:s","title":"x","message_ref":"m1"}`,
		"ghost":   `{"thread_ref":"app:ghost","title":"x","message_ref":"m1"}`,
		"message": `{"thread_ref":"app:t1","title":"x","message_ref":"zzz"}`,
		"title":   `{"thread_ref":"app:t1","title":" ","message_ref":"m1"}`,
		"unknown": `{"thread_ref":"app:t1","title":"x","message_ref":"m1","added_by":"user"}`,
	} {
		if _, err := chatCall(p, "chat.topic.add", args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if v := p.Verbs(); len(v) != 1 || !v[0].Mutates {
		t.Fatalf("verbs: %v", v)
	}
	p.principal.Scopes = []string{"verb:chat.workspace.get"}
	if _, err := chatCall(p, "chat.topic.add", `{"thread_ref":"app:t1","title":"x","message_ref":"m1"}`); err == nil {
		t.Fatal("ran without its grant")
	}
	if err := agentaccess.ChatTopicAdd().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, g := range []agentaccess.Grants{agentaccess.AppAgentRead(), agentaccess.ChatWorkspaceRead(), agentaccess.CardAsk()} {
		for _, v := range g.Verbs {
			if agentaccess.ChatTopicVerb(v) {
				t.Errorf("a preset widened with %s", v)
			}
		}
	}
}

func TestChatPlanVerbs(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var notes int
	svc := &chatplan.Service{
		Store: &chatplan.Store{DB: db},
		Exists: func(_ context.Context, ref string) error {
			if ref == "app:ghost" {
				return errors.New("none")
			}
			return nil
		},
		Note: func(context.Context, string, chatplan.Plan) { notes++ },
	}
	p := &scopedLocal{local: NewLocal(nil), plan: &ChatPlan{Service: svc}, principal: auth.Principal{Verified: true, ID: "agent",
		Scopes: []string{"verb:chat.plan.get", "verb:chat.plan.write", "verb:chat.plan.step"}}}
	if _, err := chatCall(p, "chat.plan.get", `{"thread_ref":"app:t1"}`); err == nil {
		t.Fatal("get before any plan should be not found")
	}
	out, err := chatCall(p, "chat.plan.write", `{"thread_ref":"app:t1","title":"Ship","steps":[{"title":"One"},{"title":"Two","status":"doing"}]}`)
	if err != nil || out["revision"] != 1.0 || out["status"] != "proposed" || out["created_by"] != "agent" {
		t.Fatal(out, err)
	}
	if notes != 1 {
		t.Fatalf("plan message not posted: %d", notes)
	}
	steps := out["steps"].([]any)
	one := steps[0].(map[string]any)["id"].(string)
	// Writing over an existing plan without its revision is refused.
	if _, err := chatCall(p, "chat.plan.write", `{"thread_ref":"app:t1","steps":[{"title":"x"}]}`); err == nil {
		t.Fatal("blind overwrite accepted")
	}
	out, err = chatCall(p, "chat.plan.step", `{"thread_ref":"app:t1","step_id":"`+one+`","status":"done","expected_revision":1}`)
	if err != nil || out["status"] != "done" || out["plan_revision"] != 2.0 {
		t.Fatal(out, err)
	}
	if _, err = chatCall(p, "chat.plan.step", `{"thread_ref":"app:t1","step_id":"`+one+`","status":"todo","expected_revision":1}`); err == nil {
		t.Fatal("stale revision accepted")
	}
	got, err := chatCall(p, "chat.plan.get", `{"thread_ref":"app:t1"}`)
	if err != nil || got["revision"] != 2.0 {
		t.Fatal(got, err)
	}
	for name, args := range map[string]string{
		"tether":  `{"thread_ref":"tether:s","steps":[]}`,
		"ghost":   `{"thread_ref":"app:ghost","steps":[]}`,
		"project": `{"thread_ref":"app:t1","scope":"project","steps":[]}`,
		"status":  `{"thread_ref":"app:t2","steps":[{"title":"a","status":"nope"}]}`,
		"task":    `{"thread_ref":"app:t2","steps":[{"title":"a","task_ref":"CW-1"}]}`,
	} {
		if _, err := chatCall(p, "chat.plan.write", args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if v := p.Verbs(); len(v) != 3 {
		t.Fatalf("verbs: %v", v)
	}
	p.principal.Scopes = []string{"verb:chat.plan.get"}
	if _, err := chatCall(p, "chat.plan.write", `{"thread_ref":"app:t3","steps":[]}`); err == nil {
		t.Fatal("write ran with a read-only grant")
	}
	if err := agentaccess.ChatPlan().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, g := range []agentaccess.Grants{agentaccess.AppAgentRead(), agentaccess.ChatWorkspaceRead(), agentaccess.ChatTopicAdd(), agentaccess.CardAsk()} {
		for _, v := range g.Verbs {
			if agentaccess.ChatPlanVerb(v) {
				t.Errorf("a preset widened with %s", v)
			}
		}
	}
}

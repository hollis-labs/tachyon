package dataverbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/chatactivity"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatcards"
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
	p := &scopedLocal{local: NewLocal(nil), plan: &ChatPlan{Service: svc,
		Thread: func(_ context.Context, ref string) (ChatThread, error) {
			if ref == "app:ghost" {
				return ChatThread{}, errors.New("none")
			}
			if ref == "tether:other" {
				return ChatThread{Ref: ref, AgentID: "someone"}, nil
			}
			return ChatThread{Ref: ref, AgentID: "agent"}, nil
		},
		Threads: func(context.Context) ([]ChatThread, error) {
			return []ChatThread{{Ref: "app:t1", AgentID: "agent", LastAt: 1}, {Ref: "tether:s1", Address: "tether://agent/agent", LastAt: 5}, {Ref: "tether:other", AgentID: "someone", LastAt: 9}}, nil
		}}, principal: auth.Principal{Verified: true, ID: "agent",
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
	// A tether: thread, and the default thread (the caller's most recent: tether:s1), need no hand-supplied id.
	out, err = chatCall(p, "chat.plan.write", `{"thread_ref":"tether:s1","title":"T","steps":[{"title":"a"}]}`)
	if err != nil || out["revision"] != 1.0 {
		t.Fatal(out, err)
	}
	// Two open threads and no session id: the caller cannot be tied to one, so it is an error, not a guess.
	if _, err = chatCall(p, "chat.plan.get", `{}`); err == nil || !strings.Contains(err.Error(), "thread_ref is required") {
		t.Fatal("default thread guessed among several:", err)
	}
	// A direct (non-proxy) credential cannot select a thread by session id: the value is ignored, so
	// a forged X-Tether-Session-Id changes nothing.
	p.session = "s1"
	if _, err = chatCall(p, "chat.plan.get", `{}`); err == nil || !strings.Contains(err.Error(), "thread_ref is required") {
		t.Fatal("a direct caller's session id selected a thread:", err)
	}
	p.session = ""
	if _, err := chatCall(p, "chat.plan.get", `{"thread_ref":"tether:other"}`); err == nil {
		t.Fatal("read a thread the caller does not belong to")
	}
	for name, args := range map[string]string{
		"bad ref": `{"thread_ref":"nope:s","steps":[]}`,
		"foreign": `{"thread_ref":"tether:other","steps":[]}`,
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

func TestChatActivityAndPanelVerbs(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var events []string
	rec := &chatactivity.Recorder{Store: &chatactivity.Store{DB: db}, Publish: func(_ context.Context, thread, typ string, f map[string]any) {
		events = append(events, thread+" "+typ)
	}}
	act := &ChatActivity{Rec: rec, Thread: func(_ context.Context, ref string) error {
		if ref == "app:ghost" {
			return errors.New("none")
		}
		return nil
	}}
	p := &scopedLocal{local: NewLocal(nil), activity: act, principal: auth.Principal{Verified: true, ID: "agt_1", Scopes: []string{"verb:chat.activity.append", "verb:chat.panel.open"}}}
	out, err := chatCall(p, "chat.activity.append", `{"thread_ref":"app:t1","type":"task.closed","title":"T2 closed","summary":"merged","cite":[{"kind":"pr","ref":"hollis-labs/tachyon#176","label":"#176"}]}`)
	if err != nil || out["actor"] != "agent:agt_1" || out["source"] != "agent:agt_1" || out["type"] != "task.closed" {
		t.Fatal(out, err)
	}
	for name, args := range map[string]string{
		"card.answered is Tachyon's": `{"thread_ref":"app:t1","type":"card.answered","title":"x"}`,
		"user comment":               `{"thread_ref":"app:t1","type":"comment","title":"x"}`,
		"no title":                   `{"thread_ref":"app:t1","type":"note","title":" "}`,
		"actor spoof":                `{"thread_ref":"app:t1","type":"note","title":"x","actor":"user"}`,
		"source spoof":               `{"thread_ref":"app:t1","type":"note","title":"x","source":"tachyon"}`,
		"tether":                     `{"thread_ref":"tether:s","type":"note","title":"x"}`,
		"ghost":                      `{"thread_ref":"app:ghost","type":"note","title":"x"}`,
		"bad cite":                   `{"thread_ref":"app:t1","type":"note","title":"x","cite":[{"kind":"url","ref":"x"}]}`,
	} {
		if _, err := chatCall(p, "chat.activity.append", args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := chatCall(p, "chat.panel.open", `{"thread_ref":"app:t1","panel":"plan","ref":"pl_1","reason":"plan ready"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := chatCall(p, "chat.panel.open", `{"thread_ref":"app:t1","panel":"terminal"}`); err == nil {
		t.Error("a panel outside the allowlist opened")
	}
	if len(events) < 3 || events[0] != "app:t1 activity" {
		t.Fatalf("events: %v", events)
	}
	// 5 more panel opens fit in the budget of 6, the next one does not.
	for i := 0; i < 5; i++ {
		if _, err := chatCall(p, "chat.panel.open", `{"thread_ref":"app:t1","panel":"files"}`); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := chatCall(p, "chat.panel.open", `{"thread_ref":"app:t1","panel":"files"}`); err == nil {
		t.Error("the rate limit did not apply")
	}
	if v := p.Verbs(); len(v) != 2 {
		t.Fatalf("verbs: %v", v)
	}
	// Each preset grants its own verb only.
	p.principal.Scopes = []string{"verb:chat.panel.open"}
	if len(p.Verbs()) != 1 {
		t.Fatalf("verbs with the panel grant: %v", p.Verbs())
	}
	if _, err := chatCall(p, "chat.activity.append", `{"thread_ref":"app:t1","type":"note","title":"x"}`); err == nil {
		t.Fatal("ran without its grant")
	}
	for _, g := range []agentaccess.Grants{agentaccess.ChatActivityAppend(), agentaccess.ChatPanelOpen()} {
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []agentaccess.Grants{agentaccess.AppAgentRead(), agentaccess.ChatWorkspaceRead(), agentaccess.ChatTopicAdd(), agentaccess.ChatPlan(), agentaccess.CardAsk()} {
		for _, v := range g.Verbs {
			if agentaccess.ChatActivityVerb(v) {
				t.Errorf("a preset widened with %s", v)
			}
		}
	}
}

func TestChatPlanBinding(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &chatplan.Service{Store: &chatplan.Store{DB: db}, Exists: func(context.Context, string) error { return nil }, Note: func(context.Context, string, chatplan.Plan) {}}
	threads := map[string]ChatThread{
		"tether:mine":   {Ref: "tether:mine", AgentID: "msg://agent/agent-mux/taskops-manager", LastAt: 3},
		"tether:theirs": {Ref: "tether:theirs", AgentID: "someone-else", LastAt: 9},
	}
	plan := &ChatPlan{Service: svc,
		Thread: func(_ context.Context, ref string) (ChatThread, error) {
			if th, ok := threads[ref]; ok {
				return th, nil
			}
			return ChatThread{}, errors.New("none")
		},
		Threads: func(context.Context) ([]ChatThread, error) {
			return []ChatThread{threads["tether:mine"], threads["tether:theirs"]}, nil
		}}
	verbs := []string{"verb:chat.plan.get", "verb:chat.plan.write", "verb:chat.plan.step"}
	// A purpose-named credential (no name match) that is explicitly bound to the agent.
	bound := &scopedLocal{local: NewLocal(nil), plan: plan, principal: auth.Principal{Verified: true, ID: "id-1", Display: "task-ops-chat-plan",
		Scopes: append(append([]string{}, verbs...), "agent:taskops-manager")}}
	out, err := chatCall(bound, "chat.plan.write", `{"thread_ref":"tether:mine","title":"Ship","steps":[{"title":"One"}]}`)
	if err != nil || out["revision"] != 1.0 {
		t.Fatal("bound credential refused:", out, err)
	}
	// Its only open thread is the default; the foreign one is never reachable.
	if got, err := chatCall(bound, "chat.plan.get", `{}`); err != nil || got["title"] != "Ship" {
		t.Fatal("bound credential default thread:", got, err)
	}
	if _, err := chatCall(bound, "chat.plan.get", `{"thread_ref":"tether:theirs"}`); err == nil || !strings.Contains(err.Error(), "does not grant access") {
		t.Fatal("foreign thread reachable or wrong error:", err)
	}
	if _, err := chatCall(bound, "chat.plan.get", `{"thread_ref":"tether:ghost"}`); err == nil || !strings.Contains(err.Error(), "No such chat thread") {
		t.Fatal("missing thread should be not found:", err)
	}
	// The same purpose-named credential with no binding is refused (name equality does not match).
	unbound := &scopedLocal{local: NewLocal(nil), plan: plan, principal: auth.Principal{Verified: true, ID: "id-2", Display: "task-ops-chat-plan", Scopes: verbs}}
	if _, err := chatCall(unbound, "chat.plan.get", `{"thread_ref":"tether:mine"}`); err == nil {
		t.Fatal("unbound credential reached a thread")
	}
	if _, err := chatCall(unbound, "chat.plan.get", `{}`); err == nil || !strings.Contains(err.Error(), "thread_ref is required") {
		t.Fatal("unbound default thread:", err)
	}
	// A binding to a different agent does not reach this thread.
	other := &scopedLocal{local: NewLocal(nil), plan: plan, principal: auth.Principal{Verified: true, ID: "id-3", Display: "x", Scopes: append(append([]string{}, verbs...), "agent:another")}}
	if _, err := chatCall(other, "chat.plan.get", `{"thread_ref":"tether:mine"}`); err == nil {
		t.Fatal("foreign binding reached a thread")
	}
	// Name equality still works as a documented fallback.
	named := &scopedLocal{local: NewLocal(nil), plan: plan, principal: auth.Principal{Verified: true, ID: "id-4", Display: "taskops-manager", Scopes: verbs}}
	if _, err := chatCall(named, "chat.plan.get", `{"thread_ref":"tether:mine"}`); err != nil {
		t.Fatal("name fallback refused:", err)
	}
}

// The pinned Tether proxy acts for the session named in X-Tether-Session-Id; its agent comes from
// that session's thread, with no binding and no thread_ref.
func TestChatPlanThroughPinnedProxy(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &chatplan.Service{Store: &chatplan.Store{DB: db}, Exists: func(context.Context, string) error { return nil }, Note: func(context.Context, string, chatplan.Plan) {}}
	plan := &ChatPlan{Service: svc,
		Thread: func(_ context.Context, ref string) (ChatThread, error) {
			switch ref {
			case "tether:alice", "tether:bob":
				return ChatThread{Ref: ref, AgentID: strings.TrimPrefix(ref, "tether:")}, nil
			}
			return ChatThread{}, errors.New("none")
		},
		Threads: func(context.Context) ([]ChatThread, error) {
			return []ChatThread{{Ref: "tether:alice", AgentID: "alice"}, {Ref: "tether:bob", AgentID: "bob"}}, nil
		}}
	scopes := []string{"verb:chat.plan.get", "verb:chat.plan.write", "verb:chat.plan.step"}
	proxy := auth.Principal{Verified: true, ID: "proxy-id", Display: "tether-mcp-proxy", Scopes: append([]string{"proxy:tether"}, scopes...)}
	as := func(session string) *scopedLocal {
		return &scopedLocal{local: NewLocal(nil), plan: plan, principal: proxy, session: session}
	}
	// No binding, no thread_ref: the session's own thread.
	if out, err := chatCall(as("alice"), "chat.plan.write", `{"title":"A","steps":[{"title":"one"}]}`); err != nil || out["revision"] != 1.0 {
		t.Fatal("proxy could not write the session's own plan:", out, err)
	}
	if out, err := chatCall(as("alice"), "chat.plan.get", `{"thread_ref":"tether:alice"}`); err != nil || out["title"] != "A" {
		t.Fatal("explicit thread_ref equal to the session thread must work:", out, err)
	}
	// Cross-agent denial: alice's session cannot read or write bob's thread, by ref.
	for _, verb := range []string{"chat.plan.get", "chat.plan.write"} {
		if _, err := chatCall(as("alice"), verb, `{"thread_ref":"tether:bob","steps":[]}`); err == nil {
			t.Fatalf("%s: session alice reached bob's thread through the proxy", verb)
		}
	}
	if _, err := chatCall(as("bob"), "chat.plan.get", `{}`); err == nil {
		t.Fatal("bob has no plan yet; expected not found, and certainly not alice's plan")
	}
	// A session with no thread, and a call with no session id, are errors, never a guess.
	if _, err := chatCall(as("carol"), "chat.plan.get", `{}`); err == nil || !strings.Contains(err.Error(), "no thread") {
		t.Fatal("session without a thread resolved:", err)
	}
	if _, err := chatCall(as(""), "chat.plan.get", `{}`); err == nil || !strings.Contains(err.Error(), "X-Tether-Session-Id") {
		t.Fatal("proxy with no session id was served:", err)
	}
	if _, err := chatCall(as("../x"), "chat.plan.get", `{}`); err == nil {
		t.Fatal("malformed session id accepted")
	}
}

// X-Tether-Session-Id is honoured only for the pinned proxy.
func TestTrustedSessionHeaderOnlyFromProxy(t *testing.T) {
	direct := auth.Principal{Verified: true, ID: "agent", Scopes: []string{"verb:chat.plan.get"}}
	proxy := auth.Principal{Verified: true, ID: "px", Scopes: []string{"proxy:tether"}}
	if got := trustedSession(direct, "tether-s1"); got != "" {
		t.Fatalf("forged header from a direct caller was kept: %q", got)
	}
	if got := trustedSession(auth.Principal{ID: "px", Scopes: []string{"proxy:tether"}}, "s1"); got != "" {
		t.Fatalf("an unverified principal must not be trusted: %q", got)
	}
	if got := trustedSession(proxy, "  s1 "); got != "s1" {
		t.Fatalf("pinned proxy header = %q", got)
	}
}

// The pinned Tether proxy reaches workspace, topic, activity and panel verbs for its own session
// thread only (tether:<session>); thread_ref is optional and defaults to it.
func TestChatVerbsThroughPinnedProxy(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exists := func(_ context.Context, ref string) error {
		if ref == "tether:alice" || ref == "tether:bob" {
			return nil
		}
		return errors.New("none")
	}
	st := &chatworkspace.Store{DB: db, Dir: filepath.Join(t.TempDir(), "workspace")}
	addText(t, st, "tether:alice", "notes.txt", "text/plain", []byte("hello"), true)
	rec := &chatactivity.Recorder{Store: &chatactivity.Store{DB: db}, Publish: func(context.Context, string, string, map[string]any) {}}
	scopes := []string{"proxy:tether", "verb:chat.workspace.list", "verb:chat.workspace.get", "verb:chat.topic.add", "verb:chat.activity.append", "verb:chat.panel.open"}
	proxy := auth.Principal{Verified: true, ID: "proxy-id", Display: "tether-mcp-proxy", Scopes: scopes}
	as := func(session string) *scopedLocal {
		return &scopedLocal{local: NewLocal(nil), principal: proxy, session: session,
			chat:     &ChatWorkspace{Store: st, Thread: exists},
			topics:   &ChatTopics{Store: &chattopics.Store{DB: db}, Thread: exists},
			activity: &ChatActivity{Rec: rec, Thread: exists}}
	}
	calls := map[string]string{
		"chat.workspace.list":  `%s`,
		"chat.workspace.get":   `{%s"id":"ws_notes.txt"}`,
		"chat.topic.add":       `{%s"title":"t","message_ref":"m1"}`,
		"chat.activity.append": `{%s"type":"note","title":"x"}`,
		"chat.panel.open":      `{%s"panel":"plan"}`,
	}
	args := func(verb, ref string) string {
		in := ""
		if ref != "" {
			in = `"thread_ref":"` + ref + `",`
		}
		if verb == "chat.workspace.list" {
			return "{" + strings.TrimSuffix(in, ",") + "}"
		}
		return strings.Replace(calls[verb], "%s", in, 1)
	}
	for verb := range calls {
		for _, ref := range []string{"", "tether:alice"} {
			if _, err := chatCall(as("alice"), verb, args(verb, ref)); err != nil {
				t.Errorf("%s ref=%q: own session refused: %v", verb, ref, err)
			}
		}
		for _, ref := range []string{"tether:bob", "app:t1"} {
			if _, err := chatCall(as("alice"), verb, args(verb, ref)); err == nil {
				t.Errorf("%s: alice's session reached %s", verb, ref)
			}
		}
		if _, err := chatCall(as(""), verb, args(verb, "")); err == nil {
			t.Errorf("%s: served with no session id", verb)
		}
		if _, err := chatCall(as("carol"), verb, args(verb, "")); err == nil {
			t.Errorf("%s: session without a thread resolved", verb)
		}
	}
	// A direct credential still needs an app: thread_ref and cannot use a session id.
	direct := as("alice")
	direct.principal = auth.Principal{Verified: true, ID: "agent", Scopes: scopes[1:]}
	if _, err := chatCall(direct, "chat.panel.open", `{"panel":"plan"}`); err == nil {
		t.Error("direct credential without thread_ref was served")
	}
	if _, err := chatCall(direct, "chat.panel.open", `{"thread_ref":"tether:alice","panel":"plan"}`); err == nil {
		t.Error("direct credential reached a tether thread")
	}
}

// A replacement session carries its predecessor's plan, from the first plan verb, with no
// thread_ref and before it has a single turn. The continuation headers count only from the proxy.
func TestChatPlanCarryOverThroughProxy(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &chatplan.Service{Store: &chatplan.Store{DB: db}, Exists: func(context.Context, string) error { return nil }}
	known := func(ctx context.Context, ref string) (ChatThread, error) {
		for _, st := range svc.Store.Sessions(ctx) {
			if st.Ref == ref {
				return ChatThread{Ref: ref, AgentID: st.Agent}, nil
			}
		}
		return ChatThread{}, errors.New("none")
	}
	plan := &ChatPlan{Service: svc, Thread: known, Threads: func(context.Context) ([]ChatThread, error) { return nil, nil }}
	scopes := []string{"proxy:tether", "verb:chat.plan.get", "verb:chat.plan.write", "verb:chat.plan.step"}
	proxy := auth.Principal{Verified: true, ID: "proxy-id", Scopes: scopes}
	as := func(p auth.Principal, session, agent, continues string) (*scopedLocal, context.Context) {
		h := http.Header{}
		h.Set(LogicalAgentHeader, agent)
		h.Set(ContinuesHeader, continues)
		return &scopedLocal{local: NewLocal(nil), plan: plan, principal: p, session: trustedSession(p, session)}, withLineage(context.Background(), p, h)
	}
	call := func(p auth.Principal, session, agent, continues, verb, in string) (map[string]any, error) {
		sl, lctx := as(p, session, agent, continues)
		sl.recordLineage(lctx) // what the agent handler does for every proxied request
		out, err := invokeChatPlan(lctx, plan, verb, json.RawMessage(in), sl.principal, sl.session)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m, nil
	}
	// s1 writes a plan before any turn exists.
	if out, werr := call(proxy, "s1", "worker", "", "chat.plan.write", `{"title":"Ship","steps":[{"title":"one"}]}`); werr != nil || out["revision"] != 1.0 {
		t.Fatal("session without turns could not write:", out, werr)
	}
	// A forged continuation from a direct credential is ignored (and gets no thread of its own).
	direct := auth.Principal{Verified: true, ID: "direct", Scopes: scopes[1:]}
	if _, dctx := as(direct, "s2", "worker", "s1"); lineageOf(dctx) != (lineage{}) {
		t.Fatal("continuation headers were kept for a non-proxy caller")
	}
	// A different logical agent claiming to continue s1 gets nothing.
	if _, gerr := call(proxy, "s3", "intruder", "s1", "chat.plan.get", `{}`); gerr == nil {
		t.Fatal("another agent read the carried plan")
	}
	// A fresh session of the same agent starts clean.
	if _, ferr := call(proxy, "s4", "worker", "", "chat.plan.get", `{}`); ferr == nil {
		t.Fatal("fresh session got a plan")
	}
	// The replacement gets the plan on its first get, revision bumped once.
	out, rerr := call(proxy, "s2", "worker", "s1", "chat.plan.get", `{}`)
	if rerr != nil || out["title"] != "Ship" || out["revision"] != 2.0 {
		t.Fatal("replacement did not receive the plan:", out, rerr)
	}
	if _, oerr := call(proxy, "s1", "worker", "", "chat.plan.get", `{}`); oerr == nil {
		t.Fatal("the old session still holds a copy")
	}
}

func TestChatTopicReadVerbs(t *testing.T) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &chattopics.Store{DB: db}
	ctx := context.Background()
	if _, err := store.AddTopic(ctx, "app:t1", "Design", "m1", int64p(1), "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddBookmark(ctx, "app:t1", "m2", "important"); err != nil {
		t.Fatal(err)
	}
	topics := &ChatTopics{Store: store, Thread: func(_ context.Context, _ string) error { return nil },
		OpenApprovals: func(_ context.Context, thread string) ([]chatcards.OpenApproval, error) {
			return []chatcards.OpenApproval{{ItemID: "i1", Kind: "card.approval", State: "presented"}}, nil
		}}
	p := &scopedLocal{local: NewLocal(nil), topics: topics, principal: auth.Principal{Verified: true, ID: "agent", Scopes: []string{"verb:chat.topic.list", "verb:chat.bookmark.list", "verb:chat.approvals.list"}}}
	for verb, key := range map[string]string{"chat.topic.list": "topics", "chat.bookmark.list": "bookmarks", "chat.approvals.list": "approvals"} {
		out, err := chatCall(p, verb, `{"thread_ref":"app:t1"}`)
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if l, _ := out[key].([]any); len(l) != 1 {
			t.Errorf("%s: %v", verb, out)
		}
	}
	for _, v := range p.Verbs() {
		if v.Mutates {
			t.Errorf("%s is a read verb but Mutates", v.Name)
		}
	}
	if _, err := chatCall(p, "chat.topic.list", `{"thread_ref":"tether:s"}`); err == nil {
		t.Error("a direct credential read a tether: thread")
	}
	p.principal.Scopes = []string{"verb:chat.topic.add"}
	if _, err := chatCall(p, "chat.bookmark.list", `{"thread_ref":"app:t1"}`); err == nil {
		t.Error("ran without its grant")
	}
}

// Every chat and terminal verb says when to use it, and every input field says what it is for.
func TestChatVerbsAreDescribed(t *testing.T) {
	var all []Verb
	all = append(all, chatVerbs()...)
	all = append(all, chatTopicVerbs()...)
	all = append(all, chatPlanVerbs()...)
	all = append(all, chatActivityVerbs()...)
	all = append(all, terminalVerbs()...)
	var check func(path string, s any)
	check = func(path string, s any) {
		m, _ := s.(map[string]any)
		props, _ := m["properties"].(map[string]any)
		for name, p := range props {
			pm, _ := p.(map[string]any)
			if d, _ := pm["description"].(string); d == "" {
				t.Errorf("%s.%s has no description", path, name)
			}
			check(path+"."+name, p)
			if alts, ok := pm["anyOf"].([]any); ok {
				for _, a := range alts {
					if am, ok := a.(map[string]any); ok {
						if items, ok := am["items"]; ok {
							check(path+"."+name+"[]", items)
						}
					}
				}
			}
		}
	}
	for _, v := range all {
		if !strings.Contains(v.Description, "se when") && !strings.Contains(v.Description, "se after") && !strings.Contains(v.Description, "se before") {
			t.Errorf("%s: description has no use-when sentence", v.Name)
		}
		check(v.Name, v.InputSchema)
	}
}

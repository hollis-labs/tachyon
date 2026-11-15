package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatactivity"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatplan"
	"github.com/hollis-labs/tachyon/next/internal/chat/chattopics"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatworkspace"
)

// chatFix is the agent API with every chat family wired over one store. Threads app:t1 (agent pm)
// and tether:s1 (agent pm) exist; any other ref does not.
type chatFix struct {
	fixture
	h  http.Handler
	ws *chatworkspace.Store
}

var chatThreads = map[string]ChatThread{
	"app:t1":    {Ref: "app:t1", AgentID: "pm"},
	"tether:s1": {Ref: "tether:s1", AgentID: "pm"},
}

func chatThreadExists(_ context.Context, ref string) error {
	if _, ok := chatThreads[ref]; !ok {
		return errors.New("no such thread")
	}
	return nil
}

func newChatFix(t *testing.T) chatFix {
	t.Helper()
	f := setup(t)
	ws := &chatworkspace.Store{DB: f.db, Dir: filepath.Join(t.TempDir(), "workspace")}
	rec := &chatactivity.Recorder{Store: &chatactivity.Store{DB: f.db}}
	chat := Chat{
		Workspace: &ChatWorkspace{Store: ws, Thread: chatThreadExists},
		Topics: &ChatTopics{Store: &chattopics.Store{DB: f.db}, Thread: chatThreadExists, OpenApprovals: OpenApprovals(f.db),
			MessageSeq: func(context.Context, chatapi.ThreadRef, string) (int64, error) { return 1, nil }},
		Plan: &ChatPlan{Service: &chatplan.Service{Store: &chatplan.Store{DB: f.db}, Exists: chatThreadExists},
			Thread: func(_ context.Context, ref string) (ChatThread, error) {
				th, ok := chatThreads[ref]
				if !ok {
					return th, errors.New("no such thread")
				}
				return th, nil
			},
			Threads: func(context.Context) ([]ChatThread, error) {
				return []ChatThread{chatThreads["app:t1"], chatThreads["tether:s1"]}, nil
			}},
		Activity: &ChatActivity{Rec: rec, Thread: chatThreadExists},
	}
	return chatFix{fixture: f, h: NewAgentHTTP(f.local, f.acc, chat), ws: ws}
}

// keyFor issues a key for a named identity and returns its principal and identity id.
func (f chatFix) keyFor(t *testing.T, name string, verbs ...string) (libauth.Principal, string) {
	t.Helper()
	k, cred, err := f.acc.Issue(context.Background(), access.IssueInput{Name: name, Grants: access.Grants{Verbs: verbs}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.ids.Verify(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	return p, cred.IdentityID
}

// do calls a verb: reads as GET ?input=, writes as a POST body. It returns the status and the body.
func (f chatFix) do(t *testing.T, p libauth.Principal, verb string, input any, header http.Header) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(input)
	var req *http.Request
	if access.Mutates(verb) {
		req = httptest.NewRequest(http.MethodPost, AgentPrefix+"/providers/tachyon/verbs/"+verb, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(http.MethodGet, verbPath(AgentPrefix, verb, input), nil)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req = req.WithContext(libauth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if data, ok := out["data"].(map[string]any); ok && rec.Code == 200 {
		return rec.Code, data // the verb's result, out of the envelope
	}
	return rec.Code, out
}

func (f chatFix) addFile(t *testing.T, thread, name, mime, body string, shared bool) chatworkspace.Item {
	t.Helper()
	n := int64(len(body))
	it := chatworkspace.Item{ID: "ws_" + name, Type: "file", Name: name, Mime: &mime, SizeBytes: &n, Shared: shared, AddedBy: "user"}
	if err := os.MkdirAll(filepath.Dir(f.ws.Path(thread, it.ID)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.ws.Path(thread, it.ID), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := f.ws.Add(context.Background(), thread, it)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// refused checks a verb the key does not hold: not listed for it, and unknown when called (404, as
// the old build), with nothing written.
func (f chatFix) refused(t *testing.T, p libauth.Principal, verb string, input any) {
	t.Helper()
	if code, body := call(f.h, &p, AgentPrefix+"/providers"); code != 200 || strings.Contains(body, `"`+verb+`"`) {
		t.Errorf("%s listed for a key without it: %d", verb, code)
	}
	if code, out := f.do(t, p, verb, input, nil); code != 404 || errCode(out) != "not_found" {
		t.Errorf("%s without the grant: %d %v", verb, code, out)
	}
}

func errCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	s, _ := out["code"].(string)
	return s
}

// Workspace: only shared items are listed or read; a key without the verb is refused, and discovery
// lists only the granted verb.
func TestChatWorkspaceVerbsServed(t *testing.T) {
	f := newChatFix(t)
	f.addFile(t, "app:t1", "notes.md", "text/markdown", "hello agent", true)
	f.addFile(t, "app:t1", "secret.md", "text/markdown", "not shared", false)
	p, _ := f.keyFor(t, "reader", "chat.workspace.list", "chat.workspace.get")
	code, out := f.do(t, p, "chat.workspace.list", map[string]any{"thread_ref": "app:t1"}, nil)
	if items, _ := out["items"].([]any); code != 200 || len(items) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, out = f.do(t, p, "chat.workspace.get", map[string]any{"thread_ref": "app:t1", "id": "ws_notes.md"}, nil); code != 200 || out["text"] != "hello agent" {
		t.Fatalf("get: %d %v", code, out)
	}
	if code, _ = f.do(t, p, "chat.workspace.get", map[string]any{"thread_ref": "app:t1", "id": "ws_secret.md"}, nil); code != 404 {
		t.Fatalf("unshared item: %d", code)
	}
	if code, _ = f.do(t, p, "chat.workspace.list", map[string]any{"thread_ref": "app:ghost"}, nil); code != 404 {
		t.Fatalf("unknown thread: %d", code)
	}
	other, _ := f.keyFor(t, "other", "chat.topic.list")
	f.refused(t, other, "chat.workspace.list", map[string]any{"thread_ref": "app:t1"})
}

// Topics: an agent adds a topic and reads topics, bookmarks and open approvals; a key without the
// verb is refused.
func TestChatTopicVerbsServed(t *testing.T) {
	f := newChatFix(t)
	if _, _, err := (&chattopics.Store{DB: f.db}).AddBookmark(context.Background(), "app:t1", "m1", "look here"); err != nil {
		t.Fatal(err)
	}
	p, _ := f.keyFor(t, "topics", "chat.topic.add", "chat.topic.list", "chat.bookmark.list", "chat.approvals.list")
	code, out := f.do(t, p, "chat.topic.add", map[string]any{"thread_ref": "app:t1", "title": "Decided", "message_ref": "m1"}, nil)
	if code != 200 || out["added_by"] != "agent" {
		t.Fatalf("add: %d %v", code, out)
	}
	if code, out = f.do(t, p, "chat.topic.list", map[string]any{"thread_ref": "app:t1"}, nil); code != 200 || len(out["topics"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, out = f.do(t, p, "chat.bookmark.list", map[string]any{"thread_ref": "app:t1"}, nil); code != 200 || len(out["bookmarks"].([]any)) != 1 {
		t.Fatalf("bookmarks: %d %v", code, out)
	}
	if code, out = f.do(t, p, "chat.approvals.list", map[string]any{"thread_ref": "app:t1"}, nil); code != 200 || len(out["approvals"].([]any)) != 0 {
		t.Fatalf("approvals: %d %v", code, out)
	}
	reader, _ := f.keyFor(t, "reader", "chat.topic.list")
	f.refused(t, reader, "chat.topic.add", map[string]any{"thread_ref": "app:t1", "title": "X", "message_ref": "m1"})
}

// Plans: the thread's own agent (by name, or bound) writes, reads, lists, creates, steps and moves
// steps; another identity is refused the thread, and a key without the verb is refused.
func TestChatPlanVerbsServed(t *testing.T) {
	f := newChatFix(t)
	all := []string{"chat.plan.get", "chat.plan.write", "chat.plan.step", "chat.plan.list", "chat.plan.create", "chat.plan.move_step"}
	pm, _ := f.keyFor(t, "pm", all...)
	in := map[string]any{"thread_ref": "app:t1", "title": "Ship", "steps": []map[string]any{{"title": "one"}, {"title": "two"}}}
	code, out := f.do(t, pm, "chat.plan.write", in, nil)
	if code != 200 || out["revision"] == nil {
		t.Fatalf("write: %d %v", code, out)
	}
	first := out["id"].(string)
	steps := out["steps"].([]any)
	stepID := steps[0].(map[string]any)["id"].(string)
	rev := out["revision"]
	if code, out = f.do(t, pm, "chat.plan.step", map[string]any{"thread_ref": "app:t1", "step_id": stepID, "status": "done", "expected_revision": rev}, nil); code != 200 || out["status"] != "done" {
		t.Fatalf("step: %d %v", code, out)
	}
	if code, out = f.do(t, pm, "chat.plan.create", map[string]any{"thread_ref": "app:t1"}, nil); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	second, secondRev := out["id"].(string), out["revision"]
	code, out = f.do(t, pm, "chat.plan.get", map[string]any{"thread_ref": "app:t1", "plan_id": first}, nil)
	if code != 200 {
		t.Fatalf("get: %d %v", code, out)
	}
	if code, out = f.do(t, pm, "chat.plan.move_step", map[string]any{"thread_ref": "app:t1", "from_plan": first, "to_plan": second, "step_id": stepID,
		"from_expected_revision": out["revision"], "to_expected_revision": secondRev}, nil); code != 200 {
		t.Fatalf("move_step: %d %v", code, out)
	}
	if code, out = f.do(t, pm, "chat.plan.list", map[string]any{"thread_ref": "app:t1"}, nil); code != 200 || len(out["plans"].([]any)) != 2 {
		t.Fatalf("list: %d %v", code, out)
	}
	// Bound to the agent under another name: allowed. Neither bound nor named after it: refused.
	ops, opsID := f.keyFor(t, "task-ops", all...)
	if code, _ = f.do(t, ops, "chat.plan.list", map[string]any{"thread_ref": "app:t1"}, nil); code != 403 {
		t.Fatalf("unbound identity: %d", code)
	}
	if err := f.acc.Bind(context.Background(), opsID, []string{"pm"}); err != nil {
		t.Fatal(err)
	}
	if code, _ = f.do(t, ops, "chat.plan.list", map[string]any{"thread_ref": "app:t1"}, nil); code != 200 {
		t.Fatalf("bound identity: %d", code)
	}
	readOnly, _ := f.keyFor(t, "pm", "chat.plan.get")
	f.refused(t, readOnly, "chat.plan.write", in)
}

// Activity: an agent appends a note and asks for a panel, attributed to its identity; an operator
// type is refused; a key without the verb is refused.
func TestChatActivityVerbsServed(t *testing.T) {
	f := newChatFix(t)
	p, id := f.keyFor(t, "notes", "chat.activity.append", "chat.panel.open")
	code, out := f.do(t, p, "chat.activity.append", map[string]any{"thread_ref": "app:t1", "type": "note", "title": "Merged the fix"}, nil)
	if code != 200 || out["actor"] != "agent:"+id {
		t.Fatalf("append: %d %v", code, out)
	}
	if code, out = f.do(t, p, "chat.activity.append", map[string]any{"thread_ref": "app:t1", "type": "card.answered", "title": "x"}, nil); code != 400 {
		t.Fatalf("operator type: %d %v", code, out)
	}
	if code, out = f.do(t, p, "chat.panel.open", map[string]any{"thread_ref": "app:t1", "panel": "plan"}, nil); code != 200 || out["ok"] != true {
		t.Fatalf("panel: %d %v", code, out)
	}
	other, _ := f.keyFor(t, "other", "chat.panel.open")
	f.refused(t, other, "chat.activity.append", map[string]any{"thread_ref": "app:t1", "type": "note", "title": "x"})
}

// Through the pinned Tether proxy the session header picks the session's own thread, and any other
// thread is denied; from any other caller the header is ignored.
func TestChatVerbsSessionHeaderOnlyFromTheProxy(t *testing.T) {
	f := newChatFix(t)
	proxy, proxyID := f.keyFor(t, "tether-mcp-proxy", "chat.topic.list", "chat.plan.list")
	if err := f.acc.PinProxy(context.Background(), proxyID); err != nil {
		t.Fatal(err)
	}
	session := http.Header{SessionHeader: {"s1"}}
	if code, out := f.do(t, proxy, "chat.topic.list", map[string]any{}, session); code != 200 {
		t.Fatalf("proxy, own session: %d %v", code, out)
	}
	if code, _ := f.do(t, proxy, "chat.topic.list", map[string]any{"thread_ref": "app:t1"}, session); code != 403 {
		t.Fatalf("proxy, another thread: %d", code)
	}
	if code, _ := f.do(t, proxy, "chat.topic.list", map[string]any{}, nil); code != 400 {
		t.Fatalf("proxy without a session: %d", code)
	}
	direct, _ := f.keyFor(t, "direct", "chat.topic.list")
	if code, out := f.do(t, direct, "chat.topic.list", map[string]any{}, session); code != 400 || !strings.Contains(errMessage(out), "app chat thread") {
		t.Fatalf("direct with a forged session header: %d %v", code, out)
	}
}

func errMessage(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		s, _ := e["message"].(string)
		return s
	}
	s, _ := out["message"].(string)
	return s
}

// trustedSession itself, apart from the thread checks behind it: the header counts only for the
// pinned proxy, trimmed.
func TestTrustedSessionOnlyForTheProxy(t *testing.T) {
	proxy := access.Agent{Identity: access.Identity{ID: "px", Proxy: true}}
	direct := access.Agent{Identity: access.Identity{ID: "agent"}}
	if got := trustedSession(proxy, " s1 "); got != "s1" {
		t.Errorf("proxy: %q", got)
	}
	if got := trustedSession(direct, "s1"); got != "" {
		t.Errorf("a direct caller's header was trusted: %q", got)
	}
}

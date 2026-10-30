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

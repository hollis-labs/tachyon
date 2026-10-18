package agentclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/dataverbs"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestRemoteCLIAndMCPRevocation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := localstore.Open(ctx, filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := objects.New(db)
	_, err = s.Create(ctx, objects.InboxItem{CaptureFields: objects.CaptureFields{Common: objects.Common{Name: "Remote capture"}, Status: "new"}}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	access := &agentaccess.Store{DB: db}
	tok, secret, err := access.Issue(ctx, "", "Planner", agentaccess.PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "token")
	if err := tokenfile.Create(file, secret); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(dataverbs.NewAgentHTTP(s, access))
	defer host.Close()
	c, err := New(host.URL, file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Invoke(ctx, "planner.list", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(raw), "Remote capture") {
		t.Fatal("remote read", err, string(raw))
	}
	m, err := c.MCP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.CallTool(ctx, "tachyon_planner_list", map[string]any{"kind": "inbox_item"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), "Remote capture") {
		t.Fatal("MCP did not use remote service", string(encoded))
	}
	var out strings.Builder
	if err := Command(ctx, []string{"--url", host.URL, "--token-file", file, "--input", `{"kind":"inbox_item"}`, "planner.list"}, &out); err != nil || !strings.Contains(out.String(), "Remote capture") {
		t.Fatal("CLI", err, out.String())
	}
	if err := access.Revoke(ctx, tok.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CallTool(ctx, "tachyon_planner_list", map[string]any{}); err == nil {
		t.Fatal("MCP cached authority")
	}
	// A running MCP adapter picks up a replacement token file on its next call.
	_, replacement, err := access.Issue(ctx, tok.IdentityID, "Planner", agentaccess.PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokenfile.Write(file, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CallTool(ctx, "tachyon_planner_list", map[string]any{}); err != nil {
		t.Fatal("rotation not observed", err)
	}
}
func TestClientRefusesRedirectAndUnsafeOrigin(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?x=y"} {
		if _, err := New(u, "file"); err == nil {
			t.Fatal("unsafe origin", u)
		}
	}
	hit := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	file := filepath.Join(t.TempDir(), "token")
	secret, _ := tokenfile.Generate()
	if err := tokenfile.Create(file, secret); err != nil {
		t.Fatal(err)
	}
	c, _ := New(redirect.URL, file)
	if _, err := c.Verbs(context.Background()); err == nil || hit {
		t.Fatal("followed credential redirect")
	}
}

package dataverbs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	_ "github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/config"
	_ "github.com/hollis-labs/substrate/modules/health"
	_ "github.com/hollis-labs/substrate/modules/logging"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

func init() { store.Use() }

type fixture struct {
	acc   *access.Service
	ids   *identity.Service
	local *Local
	objs  *objects.Service
	db    *store.Store // the domain store (chat verbs)
}

func setup(t *testing.T) fixture {
	t.Helper()
	t.Setenv("SUBSTRATE_CONFIG", "")
	dir := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{
		"logging":  map[string]any{"level": "error"},
		"database": storetest.New(t).ModuleConfig(),
		"auth":     map[string]any{"mode": "enforce"},
		"identity": map[string]any{"key_file": filepath.Join(dir, "identity-signing.key")},
	})
	run, err := boot.StartWith(context.Background(), "tachyon", []string{"config", "logging", "health", "database", "auth", "identity"}, string(cfg), boot.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	ids := identity.From(run.App)
	if _, err := ids.SetupOwner(context.Background(), identity.OwnerInput{Username: "owner", DisplayName: "Owner", Password: "correct horse battery staple"}); err != nil {
		t.Fatal(err)
	}
	db := database.From(run.App)
	objs := objects.New(store.Wrap(db))
	return fixture{acc: access.New(db, ids, nil), ids: ids, local: NewLocal(objs), objs: objs, db: store.Wrap(db)}
}

// key issues a mapped key and returns the principal a request would carry.
func (f fixture) key(t *testing.T, g access.Grants) libauth.Principal {
	t.Helper()
	k, _, err := f.acc.Issue(context.Background(), access.IssueInput{Name: "agent", Grants: g, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.ids.Verify(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func call(h http.Handler, p *libauth.Principal, path string) (int, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if p != nil {
		req = req.WithContext(libauth.WithPrincipal(req.Context(), *p))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", h)
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func verbPath(prefix, verb string, input any) string {
	b, _ := json.Marshal(input)
	return prefix + "/providers/tachyon/verbs/" + verb + "?input=" + url.QueryEscape(string(b))
}

// The agent API: the verb must be granted, and the object's kind must be granted, for each of the
// four object reads; discovery lists exactly the served verbs the grant allows.
func TestAgentObjectReadsCheckVerbAndKind(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	task, err := f.objs.Create(ctx, objects.Task{Common: objects.Common{Name: "A task"}, Status: "todo"}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.objs.Create(ctx, objects.Plan{Common: objects.Common{Name: "A plan"}, Status: "draft"}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHTTP(f.local, f.acc, Chat{})
	reader := f.key(t, access.Grants{Verbs: []string{"objects.list", "objects.get", "objects.history"}, Kinds: []string{"task"}})

	if code, body := call(h, &reader, AgentPrefix+"/providers"); code != 200 || !strings.Contains(body, `"objects.get"`) || strings.Contains(body, "objects.lookup_source") {
		t.Fatalf("discovery: %d %s", code, body)
	}
	for _, c := range []struct {
		verb  string
		input any
		want  int
	}{
		{"objects.get", map[string]string{"id": task.ID}, 200},
		{"objects.history", map[string]string{"id": task.ID}, 200},
		{"objects.list", map[string]string{"kind": "task"}, 200},
		{"objects.get", map[string]string{"id": plan.ID}, 403},     // kind not granted
		{"objects.history", map[string]string{"id": plan.ID}, 403}, // kind not granted
		{"objects.list", map[string]string{"kind": "plan"}, 403},   // kind not granted
		{"objects.list", map[string]string{}, 403},                 // no kind named
		{"objects.lookup_source", map[string]string{"provider": "github", "instance": "https://api.github.com", "collection": "orgs/x/repos", "kind": "task", "external_id": "1"}, 404}, // verb not granted: not listed for this credential, so unknown (as the old build)
	} {
		if code, body := call(h, &reader, verbPath(AgentPrefix, c.verb, c.input)); code != c.want {
			t.Errorf("%s %v: %d %s, want %d", c.verb, c.input, code, body, c.want)
		}
	}
	// A key holding every scope but mapped to a narrow grant is still narrow.
	star, _, _ := f.ids.CreateKey(ctx, identity.KeyInput{Name: "star", Scopes: []string{"*"}})
	sp, _ := f.ids.Verify(ctx, star)
	if code, _ := call(h, &sp, verbPath(AgentPrefix, "objects.get", map[string]string{"id": task.ID})); code != 403 {
		t.Errorf("an unmapped * key: %d", code)
	}
}

// The console API answers signed-in people with the objects scope, never keys.
func TestConsoleObjectReads(t *testing.T) {
	f := setup(t)
	task, err := f.objs.Create(context.Background(), objects.Task{Common: objects.Common{Name: "A task"}, Status: "todo"}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	h := NewConsoleHTTP(f.local)
	path := verbPath(Prefix, "objects.get", map[string]string{"id": task.ID})
	owner := libauth.Principal{Verified: true, ID: "user:usr_o", Kind: "user", Scopes: []string{"*"}}
	reader := libauth.Principal{Verified: true, ID: "user:usr_r", Kind: "user", Scopes: []string{"tachyon.objects.read"}}
	other := libauth.Principal{Verified: true, ID: "user:usr_x", Kind: "user", Scopes: []string{"tachyon.planner.read"}}
	key := libauth.Principal{Verified: true, ID: "key:k_a", Kind: "service", Scopes: []string{"*"}}
	for name, c := range map[string]struct {
		p    libauth.Principal
		want int
	}{"owner": {owner, 200}, "objects reader": {reader, 200}, "no objects scope": {other, 403}, "a key with *": {key, 403}} {
		if code, body := call(h, &c.p, path); code != c.want {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if code, _ := call(h, &owner, verbPath(Prefix, "chat.plan.get", map[string]string{})); code != 404 {
		t.Errorf("an unserved verb: %d", code)
	}
}

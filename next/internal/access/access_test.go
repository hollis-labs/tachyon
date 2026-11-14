package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libstore "github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	_ "github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/config"
	_ "github.com/hollis-labs/substrate/modules/health"
	_ "github.com/hollis-labs/substrate/modules/logging"

	"github.com/hollis-labs/tachyon/next/internal/store"
)

func init() { store.Use() }

const password = "correct horse battery staple"

// env is a started database and identity module on a temp directory, with Tachyon's schema.
type env struct {
	st  *libstore.Store
	ids *identity.Service
	svc *Service
	now time.Time
}

func start(t *testing.T) *env {
	t.Helper()
	t.Setenv("SUBSTRATE_CONFIG", "")
	dir := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{
		"logging":  map[string]any{"level": "error"},
		"database": map[string]any{"sqlite": map[string]any{"path": filepath.Join(dir, "tachyon.db")}},
		"auth":     map[string]any{"mode": "enforce"},
		"identity": map[string]any{"key_file": filepath.Join(dir, "identity-signing.key")},
	})
	run, err := boot.StartWith(context.Background(), "tachyon", []string{"config", "logging", "health", "database", "auth", "identity"}, string(cfg), boot.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	e := &env{st: database.From(run.App), ids: identity.From(run.App), now: time.Now().UTC().Truncate(time.Second)}
	e.svc = New(e.st, e.ids, func() time.Time { return e.now })
	return e
}

func (e *env) owner(t *testing.T) {
	t.Helper()
	if _, err := e.ids.SetupOwner(context.Background(), identity.OwnerInput{Username: "owner", DisplayName: "Owner", Password: password}); err != nil {
		t.Fatal(err)
	}
}

// oldToken is one row of the old build's service_tokens, as migrate-from copies it.
type oldToken struct {
	identity, name string
	grants         string // the old JSON, kinds null where the old build wrote null
	expires        time.Duration
	revoked        bool
	disabled       bool
}

// fixture mirrors the grant shapes of the old build's tokens (generic names, no real data): a
// planner reader; a portfolio writer; an actions agent with kinds null; three active identities
// sharing one name (plus a revoked fourth); one identity with a revoked and an active token; an
// expired token; a disabled identity.
var fixture = []oldToken{
	{identity: "11111111-0000-4000-8000-000000000001", name: "planner-reader", grants: `{"verbs":["planner.list","objects.list","objects.get","objects.history","objects.lookup_source"],"kinds":["plan","task","epic","sprint","collection","workstream","inbox_item","idea","decision","risk","roadmap_item","strategic_priority"]}`, expires: 3 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000002", name: "portfolio-writer", grants: `{"verbs":["planner.list","objects.list","objects.get","objects.history","objects.lookup_source","planner.portfolio.list","planner.portfolio.get","planner.portfolio.schema","planner.portfolio.resolve","planner.portfolio.create","planner.portfolio.patch","planner.portfolio.action","planner.portfolio.relate","planner.portfolio.reorder"],"kinds":["strategic_priority","workstream","roadmap_item","idea","decision","risk","inbox_item"]}`, expires: 5 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000003", name: "actions-agent", grants: `{"verbs":["actions.catalog","actions.request","actions.await"],"kinds":null}`, expires: 9 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000004", name: "shared-proxy", grants: `{"verbs":["chat.plan.get","chat.plan.write","chat.plan.step"],"kinds":null}`, expires: 28 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000005", name: "shared-proxy", grants: `{"verbs":["chat.plan.get","chat.plan.write","chat.plan.step"],"kinds":null}`, expires: 88 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000006", name: "shared-proxy", grants: `{"verbs":["chat.plan.get","chat.plan.write","chat.plan.step","chat.workspace.list","chat.workspace.get","chat.topic.add","chat.activity.append","chat.panel.open"],"kinds":["plan","task","epic","sprint","collection","workstream","inbox_item","idea","decision","risk","roadmap_item","strategic_priority"]}`, expires: 88 * 24 * time.Hour, revoked: true},
	{identity: "11111111-0000-4000-8000-000000000007", name: "shared-proxy", grants: `{"verbs":["chat.plan.get","chat.plan.write","chat.plan.step","chat.workspace.list","chat.workspace.get","chat.topic.add","chat.activity.append","chat.panel.open"],"kinds":null}`, expires: 88 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000008", name: "app-agent", grants: `{"verbs":["planner.list","objects.list","objects.get","objects.history","objects.lookup_source","torque.task.get","torque.task.comments","torque.task.list","tesseract.recall","tesseract.get"],"kinds":["plan","task","epic","sprint","collection","workstream","inbox_item","idea","decision","risk","roadmap_item","strategic_priority"]}`, expires: 70 * 24 * time.Hour, revoked: true},
	{identity: "11111111-0000-4000-8000-000000000008", name: "app-agent", grants: `{"verbs":["planner.list","objects.list","objects.get","objects.history","objects.lookup_source","torque.task.get","torque.task.comments","torque.task.list","tesseract.recall","tesseract.get"],"kinds":["plan","task","epic","sprint","collection","workstream","inbox_item","idea","decision","risk","roadmap_item","strategic_priority"]}`, expires: 75 * 24 * time.Hour},
	{identity: "11111111-0000-4000-8000-000000000009", name: "expired-agent", grants: `{"verbs":["planner.list"],"kinds":["task"]}`, expires: -time.Hour},
	{identity: "11111111-0000-4000-8000-00000000000a", name: "disabled-agent", grants: `{"verbs":["planner.list"],"kinds":["task"]}`, expires: 10 * 24 * time.Hour, disabled: true},
}

// active are the fixture tokens reissue must turn into keys.
func active() []int {
	var out []int
	for i, f := range fixture {
		if !f.revoked && !f.disabled && f.expires > 0 {
			out = append(out, i)
		}
	}
	return out
}

func tokenID(i int) string { return fmt.Sprintf("22222222-0000-4000-8000-%012d", i) }

func (e *env) load(t *testing.T) {
	t.Helper()
	created := e.now.Add(-24 * time.Hour)
	err := e.st.WriteTx(context.Background(), func(ctx context.Context, tx libstore.Tx) error {
		seen := map[string]bool{}
		for i, f := range fixture {
			if !seen[f.identity] {
				seen[f.identity] = true
				var disabled any
				if f.disabled {
					disabled = created.Unix()
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO service_identities(id, name, created_at, disabled_at) VALUES (?, ?, ?, ?)`, f.identity, f.name, created.Unix(), disabled); err != nil {
					return err
				}
			}
			var revoked any
			if f.revoked {
				revoked = created.Unix()
			}
			expires := e.now.Add(f.expires)
			if !expires.After(created) {
				created2 := expires.Add(-time.Hour)
				_, err := tx.ExecContext(ctx, `INSERT INTO service_tokens(id, identity_id, token_hash, grants, created_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					tokenID(i), f.identity, fmt.Sprintf("hash-%d", i), f.grants, created2.Unix(), expires.Unix(), revoked)
				if err != nil {
					return err
				}
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO service_tokens(id, identity_id, token_hash, grants, created_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				tokenID(i), f.identity, fmt.Sprintf("hash-%d", i), f.grants, created.Unix(), expires.Unix(), revoked); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// oldAllowSet is the old build's semantics computed from the fixture's own JSON, independently of
// anything the reissue stored.
func oldAllowSet(t *testing.T, raw string) map[Pair]bool {
	t.Helper()
	var g Grants
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatal(err)
	}
	out := map[Pair]bool{}
	for _, v := range Verbs() {
		has := false
		for _, gv := range g.Verbs {
			has = has || gv == v
		}
		if !has {
			continue
		}
		out[Pair{v, ""}] = true
		for _, k := range Kinds() {
			for _, gk := range g.Kinds {
				if gk == k {
					out[Pair{v, k}] = true
				}
			}
		}
	}
	return out
}

func TestReissueBeforeOwnerRefuses(t *testing.T) {
	e := start(t)
	e.load(t)
	if _, err := e.svc.Reissue(context.Background()); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("err = %v", err)
	}
}

// Every active token becomes one key, keyed on its identity id, with the old allow-set of
// (verb, kind) exactly and an expiry no later than the old one.
func TestReissueKeepsEveryGrantExactly(t *testing.T) {
	e := start(t)
	e.load(t)
	e.owner(t)
	ctx := context.Background()
	done, err := e.svc.Reissue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := active()
	if len(done) != len(want) {
		t.Fatalf("%d keys, want %d", len(done), len(want))
	}
	byToken := map[string]Reissued{}
	for _, r := range done {
		byToken[r.OldTokenID] = r
	}
	identitiesSeen := map[string]bool{}
	for _, i := range want {
		f := fixture[i]
		r, ok := byToken[tokenID(i)]
		if !ok {
			t.Errorf("token %d (%s) not reissued", i, f.name)
			continue
		}
		if r.Identity.ID != f.identity || !r.AllowSetEqual || !r.ExpiryKept {
			t.Errorf("token %d: identity %s, equal %v, expiry kept %v", i, r.Identity.ID, r.AllowSetEqual, r.ExpiryKept)
		}
		identitiesSeen[r.Identity.ID] = true
		// Independently: verify the key through the identity module, resolve it like a request.
		p, verr := e.ids.Verify(ctx, r.Key)
		if verr != nil {
			t.Fatal(verr)
		}
		agent, rerr := e.svc.Resolve(ctx, p)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if got, wantSet := agent.AllowSet(), oldAllowSet(t, f.grants); !maps.Equal(got, wantSet) {
			t.Errorf("token %d (%s): %d pairs allowed, the old token allowed %d", i, f.name, len(got), len(wantSet))
		}
		if p.ExpiresAt.IsZero() || p.ExpiresAt.After(e.now.Add(f.expires)) {
			t.Errorf("token %d: key expires %v, old %v", i, p.ExpiresAt, e.now.Add(f.expires))
		}
		if agent.Identity.ID != f.identity {
			t.Errorf("token %d resolved to identity %s", i, agent.Identity.ID)
		}
	}
	// The three active identities sharing one name each got their own key.
	shared := 0
	for id := range identitiesSeen {
		for _, f := range fixture {
			if f.identity == id && f.name == "shared-proxy" {
				shared++
				break
			}
		}
	}
	if shared != 3 {
		t.Errorf("%d shared-name identities got keys, want 3", shared)
	}
	// Old tokens that were revoked, expired or disabled were not reissued; the reissued ones are
	// marked superseded.
	l, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	superseded := 0
	for _, il := range l.Identities {
		for _, ot := range il.OldTokens {
			if ot.SupersededBy != "" {
				superseded++
			}
		}
	}
	if superseded != len(want) {
		t.Errorf("%d old tokens superseded, want %d", superseded, len(want))
	}
}

func TestReissueRunsOnce(t *testing.T) {
	e := start(t)
	e.load(t)
	e.owner(t)
	if _, err := e.svc.Reissue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reissue(context.Background()); !errors.Is(err, ErrKeysExist) {
		t.Fatalf("second reissue: %v", err)
	}
	// Any identity key at all also refuses it.
	f := start(t)
	f.load(t)
	f.owner(t)
	if _, _, err := f.ids.CreateKey(context.Background(), identity.KeyInput{Name: "other", Scopes: []string{"tachyon.planner.read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reissue(context.Background()); !errors.Is(err, ErrKeysExist) {
		t.Fatalf("reissue with a key present: %v", err)
	}
}

func TestResolveRefusals(t *testing.T) {
	e := start(t)
	e.owner(t)
	ctx := context.Background()
	g, _ := Preset("planner-read")
	key, cred, err := e.svc.Issue(ctx, IssueInput{Name: "reader", Grants: g, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.ids.Verify(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := e.svc.Resolve(ctx, p); rerr != nil {
		t.Fatalf("a mapped key: %v", rerr)
	}
	// A token minted from the key carries the key's principal id.
	minted := libauth.Principal{Verified: true, ID: p.ID, Kind: "service", Scopes: p.Scopes, Parent: cred.ID, TokenID: "tok_x"}
	if _, rerr := e.svc.Resolve(ctx, minted); rerr != nil {
		t.Fatalf("a token minted from a mapped key: %v", rerr)
	}
	// A key with every scope but no mapping is refused.
	star, _, err := e.ids.CreateKey(ctx, identity.KeyInput{Name: "star", Scopes: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := e.ids.Verify(ctx, star)
	if _, rerr := e.svc.Resolve(ctx, sp); !errors.Is(rerr, ErrUnmapped) {
		t.Fatalf("an unmapped key: %v", rerr)
	}
	// A person is not an agent.
	if _, rerr := e.svc.Resolve(ctx, libauth.Principal{Verified: true, ID: "user:usr_x", Kind: "user", Scopes: []string{"*"}}); !errors.Is(rerr, ErrNotAgent) {
		t.Fatalf("a user: %v", rerr)
	}
	// Expired, revoked, disabled.
	e.now = e.now.Add(2 * time.Hour)
	if _, rerr := e.svc.Resolve(ctx, p); !errors.Is(rerr, ErrInactive) {
		t.Fatalf("expired: %v", rerr)
	}
	e.now = e.now.Add(-2 * time.Hour)
	if rerr := e.svc.Revoke(ctx, cred.ID); rerr != nil {
		t.Fatal(rerr)
	}
	if _, rerr := e.svc.Resolve(ctx, p); !errors.Is(rerr, ErrInactive) {
		t.Fatalf("revoked: %v", rerr)
	}
	key2, cred2, err := e.svc.Issue(ctx, IssueInput{Name: "reader", IdentityID: cred.IdentityID, Grants: g, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := e.ids.Verify(ctx, key2)
	if err := e.svc.Disable(ctx, cred2.IdentityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Resolve(ctx, p2); !errors.Is(err, ErrInactive) {
		t.Fatalf("disabled: %v", err)
	}
}

// A key's verb scope and the mapping's verbs and kinds must all agree.
func TestAllowsNeedsScopeVerbAndKind(t *testing.T) {
	a := Agent{
		Credential: Credential{Grants: Grants{Verbs: []string{"planner.list", "objects.get"}, Kinds: []string{"task"}}},
		principal:  libauth.Principal{Verified: true, Kind: "service", Scopes: []string{ScopeFor("planner.list")}},
	}
	for _, c := range []struct {
		verb, kind string
		want       bool
	}{
		{"planner.list", "", true}, {"planner.list", "task", true}, {"planner.list", "plan", false},
		{"objects.get", "task", false}, // granted in the mapping, but the key lacks the scope
		{"actions.request", "", false},
	} {
		if got := a.Allows(c.verb, c.kind); got != c.want {
			t.Errorf("Allows(%s, %q) = %v", c.verb, c.kind, got)
		}
	}
	// A key holding "*" gets nothing the mapping does not grant.
	a.principal.Scopes = []string{"*"}
	if a.Allows("actions.request", "") || !a.Allows("objects.get", "task") {
		t.Error("the mapping must still bound a * key")
	}
}

func TestIssueRefusesWildcardsAndOperator(t *testing.T) {
	e := start(t)
	e.owner(t)
	for _, verbs := range [][]string{{"*"}, {"operator"}, {"tachyon.operator"}, {"planner.*"}, {"unknown.verb"}} {
		_, _, err := e.svc.Issue(context.Background(), IssueInput{Name: "x", Grants: Grants{Verbs: verbs, Kinds: []string{"task"}}, TTL: time.Hour})
		if err == nil {
			t.Errorf("verbs %v accepted", verbs)
		}
	}
	if _, _, err := e.svc.Issue(context.Background(), IssueInput{Name: "x", Grants: Grants{Verbs: []string{"planner.list"}}, TTL: time.Hour}); err == nil {
		t.Error("an object verb without kinds was accepted")
	}
}

// The agent route answers mapped keys only, and discovery lists exactly the granted verbs.
func TestRequireAgentAndDiscovery(t *testing.T) {
	e := start(t)
	e.owner(t)
	ctx := context.Background()
	g, _ := Preset("actions-request")
	key, _, err := e.svc.Issue(ctx, IssueInput{Name: "actions", Grants: g, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	star, _, _ := e.ids.CreateKey(ctx, identity.KeyInput{Name: "star", Scopes: []string{"*"}})
	h := RequireAgent(e.svc, Discovery())
	call := func(p libauth.Principal) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DiscoveryPattern, nil)
		h.ServeHTTP(rec, req.WithContext(libauth.WithPrincipal(req.Context(), p)))
		return rec.Code, rec.Body.String()
	}
	p, _ := e.ids.Verify(ctx, key)
	code, body := call(p)
	if code != 200 || !strings.Contains(body, `"actions.request"`) || strings.Contains(body, "planner.list") {
		t.Fatalf("mapped key: %d %s", code, body)
	}
	sp, _ := e.ids.Verify(ctx, star)
	if code, body := call(sp); code != 403 || !strings.Contains(body, "credential_not_mapped") {
		t.Fatalf("unmapped * key: %d %s", code, body)
	}
	if code, body := call(libauth.Principal{Verified: true, ID: "user:usr_x", Kind: "user", Scopes: []string{"*"}}); code != 403 || !strings.Contains(body, "agent_required") {
		t.Fatalf("owner session: %d %s", code, body)
	}
}

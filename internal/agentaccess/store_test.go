package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestCredentialLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{DB: db}
	first, secret, err := s.Issue(ctx, "", "Planner", PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Verify(ctx, secret)
	if err != nil || !p.Verified || p.ID != first.IdentityID || p.TokenID != first.ID || !Allows(p, "verb", "planner.list") || Allows(p, "kind", "agent") {
		t.Fatal("principal/grants", p, err)
	}
	tokens, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tokens)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), auth.HashKey(secret)) {
		t.Fatal("credential material exposed")
	}
	second, secret2, err := s.Issue(ctx, first.IdentityID, "Planner", PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(ctx, secret); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("revoked accepted", err)
	}
	if _, err = s.Verify(ctx, secret2); err != nil {
		t.Fatal("rotation revoked other token", err)
	}
	if err = s.Revoke(ctx, second.IdentityID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(ctx, secret2); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("disabled identity accepted", err)
	}
	if _, _, err = s.Issue(ctx, first.IdentityID, "Planner", PlannerRead(), time.Hour); err == nil {
		t.Fatal("disabled identity reactivated")
	}
	expired, secret3, err := s.Issue(ctx, "", "Expired", PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Writer.Submit(ctx, "expire", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE service_tokens SET created_at=?,expires_at=? WHERE id=?", time.Now().Add(-2*time.Hour).Unix(), time.Now().Add(-time.Hour).Unix(), expired.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(ctx, secret3); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatal("expired accepted", err)
	}
	for _, g := range []Grants{{Verbs: []string{"objects.create"}, Kinds: []string{"plan"}}, {Verbs: []string{"planner.list"}, Kinds: []string{"*"}}, {}} {
		if _, _, err := s.Issue(ctx, "", "bad", g, time.Hour); err == nil {
			t.Fatal("invalid grant accepted")
		}
	}
}

// Existing planner-read credentials keep their exact grants; the Torque verbs are
// opt-in through their own presets and are never part of PlannerRead.
func TestTorqueVerbsAreOptInPresets(t *testing.T) {
	for _, v := range []string{"torque.task.get", "torque.task.comments", "torque.task.list"} {
		if !SupportedVerb(v) || !TorqueVerb(v) || ReadVerb(v) {
			t.Fatalf("%s: supported=%v torque=%v readVerb=%v", v, SupportedVerb(v), TorqueVerb(v), ReadVerb(v))
		}
		if contains(PlannerRead().Verbs, v) || contains(PortfolioRead().Verbs, v) || contains(PortfolioWrite().Verbs, v) {
			t.Fatalf("%s leaked into an existing preset", v)
		}
		if !contains(TorqueRead().Verbs, v) || !contains(PlannerTorqueRead().Verbs, v) {
			t.Fatalf("%s missing from its own preset", v)
		}
	}
	for _, g := range []Grants{TorqueRead(), PlannerTorqueRead()} {
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"task", "plan", "epic", "sprint"} {
			if !contains(g.Kinds, k) {
				t.Fatalf("kind %s missing", k)
			}
		}
	}
	if !contains(PlannerTorqueRead().Verbs, "planner.list") || !contains(PlannerTorqueRead().Verbs, "objects.get") {
		t.Fatal("planner-torque-read lost the planner verbs")
	}
}

// The Tesseract verbs are opt-in like the Torque ones, and need no object kinds.
func TestTesseractVerbsAreOptInPresets(t *testing.T) {
	for _, v := range []string{"tesseract.recall", "tesseract.get"} {
		if !SupportedVerb(v) || !TesseractVerb(v) || ReadVerb(v) {
			t.Fatalf("%s: supported=%v tesseract=%v readVerb=%v", v, SupportedVerb(v), TesseractVerb(v), ReadVerb(v))
		}
		for name, g := range map[string]Grants{"planner-read": PlannerRead(), "planner-torque-read": PlannerTorqueRead(), "torque-read": TorqueRead(), "portfolio-write": PortfolioWrite()} {
			if contains(g.Verbs, v) {
				t.Fatalf("%s leaked into %s", v, name)
			}
		}
		if !contains(TesseractRead().Verbs, v) || !contains(AppAgentRead().Verbs, v) {
			t.Fatalf("%s missing from its presets", v)
		}
	}
	if err := TesseractRead().Validate(); err != nil {
		t.Fatalf("a Tesseract-only credential needs no kinds: %v", err)
	}
	if err := (Grants{Verbs: []string{"planner.list"}}).Validate(); err == nil {
		t.Fatal("a planner verb without kinds must still be refused")
	}
	g := AppAgentRead()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"planner.list", "objects.get", "torque.task.get", "torque.task.comments", "torque.task.list", "tesseract.recall", "tesseract.get"} {
		if !contains(g.Verbs, v) {
			t.Fatalf("app-agent-read lacks %s", v)
		}
	}
}

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"

	"github.com/hollis-labs/tachyon/internal/agentsserve"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsreg"
)

func groupStore(t *testing.T) (*localstore.Store, *settingsreg.Registry) {
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := &settingsreg.Registry{DB: db}
	for _, g := range []settingsreg.Group{generalGroup(), chatGroup(), agentsGroup(), transcriptsGroup()} {
		if err := reg.Register(g); err != nil {
			t.Fatalf("%s: %v", g.ID, err)
		}
	}
	return db, reg
}

// The registry's Storage and the legacy row must agree both ways.
func TestAgentsGroupSharesTheLegacyRow(t *testing.T) {
	db, reg := groupStore(t)
	g, _ := reg.Group("agents")
	ctx := context.Background()
	st, err := g.Storage.Load(ctx, db.Reader)
	if err != nil || len(st.Over) != 0 {
		t.Fatalf("empty store: %+v %v", st, err)
	}
	st.Over["stale_after_s"] = sint(1200)
	st.Over["nudge_presets"] = sstr("Run the tests\n\nStop")
	if err := db.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error { return g.Storage.Save(ctx, tx, st) }); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Reader.QueryRow(`SELECT value FROM app_settings WHERE key=?`, agentsserve.SettingsKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var legacy agentsserve.Settings
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil || legacy.StaleAfterS != 1200 || len(legacy.NudgePresets) != 2 || legacy.HandoffTimeoutS != agentsserve.DefaultSettings().HandoffTimeoutS {
		t.Fatalf("legacy row %s: %+v %v", raw, legacy, err)
	}
	back, _ := g.Storage.Load(ctx, db.Reader)
	if v, _ := scalarInt(back.Over["stale_after_s"]); v != 1200 {
		t.Fatalf("round trip: %+v", back.Over)
	}
	// A reset (no overrides) removes the row, so the legacy reader falls back to its defaults.
	if err := db.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
		return g.Storage.Save(ctx, tx, settingsreg.Stored{Over: map[string]admin.Scalar{}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Reader.QueryRow(`SELECT value FROM app_settings WHERE key=?`, agentsserve.SettingsKey).Scan(&raw); err != sql.ErrNoRows {
		t.Fatalf("row kept after reset: %v", err)
	}
}

func TestChatAndGeneralMapLegacyRows(t *testing.T) {
	db, reg := groupStore(t)
	ctx := context.Background()
	for id, c := range map[string]struct{ key, val, want string }{
		"chat":    {"scratchpad_placement", "aside", `{"scratchpad_placement":"aside"}`},
		"general": {"elevation_ttl", "7d", "7d"},
	} {
		g, _ := reg.Group(id)
		if err := db.Writer.Submit(ctx, "t", func(ctx context.Context, tx *sql.Tx) error {
			return g.Storage.Save(ctx, tx, settingsreg.Stored{Over: map[string]admin.Scalar{c.key: sstr(c.val)}})
		}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		st, _ := g.Storage.Load(ctx, db.Reader)
		if got, _ := scalarString(st.Over[c.key]); got != c.val {
			t.Errorf("%s round trip: %q", id, got)
		}
	}
	var raw string
	_ = db.Reader.QueryRow(`SELECT value FROM app_settings WHERE key='chat.settings'`).Scan(&raw)
	if raw != `{"scratchpad_placement":"aside"}` {
		t.Errorf("chat row %s", raw)
	}
	if g, _ := reg.Group("transcripts"); !g.ReadOnly {
		t.Error("transcripts must be read-only")
	}
}

package alertstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// PrefsKey is the app_settings key of the single prefs row (one operator, so app = user).
const PrefsKey = "alerts.prefs.v1"

// PrefsStore is where the prefs live. It is a small interface so the settings registry (ST1/ST2) can
// take the setting over without touching the routes.
type PrefsStore interface {
	// Get returns the saved prefs, or nil when none were ever saved or migrated.
	Get(ctx context.Context) (*alertsapi.Prefs, error)
	Put(ctx context.Context, p alertsapi.Prefs) error
}

// SettingsPrefs keeps the prefs in app_settings.
type SettingsPrefs struct{ DB *localstore.Store }

func (s SettingsPrefs) Get(ctx context.Context) (*alertsapi.Prefs, error) {
	var raw string
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, PrefsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p alertsapi.Prefs
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil, nil
	}
	if p.MutedSources == nil {
		p.MutedSources = []string{}
	}
	return &p, nil
}

func (s SettingsPrefs) Put(ctx context.Context, p alertsapi.Prefs) error {
	b, _ := json.Marshal(p)
	return s.DB.Writer.Submit(ctx, "alert_prefs", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, PrefsKey, string(b))
		return err
	})
}

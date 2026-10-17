// Package apicache stores successful safe API reads in Tachyon's app database.
package apicache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

type Policy struct {
	Enabled           bool `json:"enabled"`
	FreshSeconds      int  `json:"fresh_seconds"`
	RevalidateSeconds int  `json:"stale_while_revalidate_seconds"`
	StaleSeconds      int  `json:"stale_if_error_seconds"`
	TimeoutSeconds    int  `json:"timeout_seconds"`
}

type Settings struct {
	MaxEntries       int               `json:"max_entries"`
	MaxBytes         int64             `json:"max_bytes"`
	RetentionSeconds int               `json:"retention_seconds"`
	Policies         map[string]Policy `json:"policies"`
}

func Defaults() Settings {
	return Settings{MaxEntries: 1000, MaxBytes: 64 << 20, RetentionSeconds: 7 * 86400, Policies: map[string]Policy{
		"approvals":  {true, 3, 60, 300, 15},
		"chat-turns": {true, 3, 60, 300, 15},
		"chat":       {true, 5, 60, 300, 15},
		"activity":   {true, 5, 60, 300, 10}, "planner": {true, 15, 3600, 86400, 15},
		"catalog": {true, 60, 3600, 86400, 15}, "torque": {true, 15, 300, 3600, 15}, "forge": {true, 30, 3600, 3600, 20},
	}}
}

func (s Settings) Validate() error {
	if s.MaxEntries < 1 || s.MaxEntries > 100000 || s.MaxBytes < 1024 || s.MaxBytes > 1<<30 || s.RetentionSeconds < 1 || s.RetentionSeconds > 30*86400 {
		return errors.New("invalid cache size or retention policy")
	}
	for k, p := range s.Policies {
		if _, ok := Defaults().Policies[k]; !ok {
			return fmt.Errorf("unknown cache policy %q", k)
		}
		if p.FreshSeconds < 0 || p.RevalidateSeconds < 0 || p.StaleSeconds < 0 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 60 || p.FreshSeconds+max(p.StaleSeconds, p.RevalidateSeconds) > s.RetentionSeconds {
			return fmt.Errorf("invalid cache policy %q: durations must be nonnegative, timeout 1–60s, freshness + stale allowance within retention", k)
		}
	}
	return nil
}

// LoadSettings keeps effective policy in SQLite. An optional operator-owned
// JSON file overlays it at startup; unknown fields and invalid knobs fail loudly.
func LoadSettings(ctx context.Context, db *localstore.Store, path string) (Settings, error) {
	cfg := Defaults()
	var saved string
	err := db.Reader.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key='api_cache.policy'").Scan(&saved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return cfg, err
	}
	if saved != "" {
		var old struct {
			MaxEntries       int                        `json:"max_entries"`
			MaxBytes         int64                      `json:"max_bytes"`
			RetentionSeconds int                        `json:"retention_seconds"`
			Policies         map[string]json.RawMessage `json:"policies"`
		}
		if err = json.Unmarshal([]byte(saved), &old); err != nil {
			return cfg, err
		}
		cfg.MaxEntries, cfg.MaxBytes, cfg.RetentionSeconds = old.MaxEntries, old.MaxBytes, old.RetentionSeconds
		for k, b := range old.Policies {
			p := cfg.Policies[k]
			if err = json.Unmarshal(b, &p); err != nil {
				return cfg, err
			}
			cfg.Policies[k] = p
		}
	}
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return cfg, e
		}
		// Decode policy entries individually so omitted fields retain their defaults.
		var overlay struct {
			MaxEntries       *int                       `json:"max_entries"`
			MaxBytes         *int64                     `json:"max_bytes"`
			RetentionSeconds *int                       `json:"retention_seconds"`
			Policies         map[string]json.RawMessage `json:"policies"`
		}
		if e = decodeStrict(b, &overlay); e != nil {
			return cfg, e
		}
		if overlay.MaxEntries != nil {
			cfg.MaxEntries = *overlay.MaxEntries
		}
		if overlay.MaxBytes != nil {
			cfg.MaxBytes = *overlay.MaxBytes
		}
		if overlay.RetentionSeconds != nil {
			cfg.RetentionSeconds = *overlay.RetentionSeconds
		}
		for name, b := range overlay.Policies {
			p, ok := cfg.Policies[name]
			if !ok {
				return cfg, fmt.Errorf("unknown cache policy %q", name)
			}
			if e = decodeStrict(b, &p); e != nil {
				return cfg, e
			}
			cfg.Policies[name] = p
		}
	}
	if err = cfg.Validate(); err != nil {
		return cfg, err
	}
	b, _ := json.Marshal(cfg)
	err = db.Writer.Submit(ctx, "cache_policy", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO app_settings(key,value) VALUES('api_cache.policy',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(b))
		return e
	})
	return cfg, err
}

func decodeStrict(b []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

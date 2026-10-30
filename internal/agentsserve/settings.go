package agentsserve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// SettingsKey is the app_settings row.
const SettingsKey = "agents.settings"

// Settings are the operator-editable Agents settings. The handoff fields are used by restart (A6);
// stale_after_s is read by every sessions request.
type Settings struct {
	StaleAfterS     int      `json:"stale_after_s"`
	HandoffPrompt   string   `json:"handoff_prompt"`
	HandoffTimeoutS int      `json:"handoff_timeout_s"`
	NudgePresets    []string `json:"nudge_presets"`
}

// DefaultSettings are used until the operator saves.
func DefaultSettings() Settings {
	return Settings{StaleAfterS: 900, HandoffPrompt: "Write a short handoff of your current work to Tesseract, then reply 'done'.", HandoffTimeoutS: 300,
		NudgePresets: []string{"Run the tests", "Summarise where you are", "Stop and wait for me"}}
}

// Validate is the rule set the PUT enforces.
func (s Settings) Validate() error {
	switch {
	case s.StaleAfterS < 60 || s.StaleAfterS > 7*24*3600:
		return errors.New("stale_after_s is 60 to 604800.")
	case s.HandoffTimeoutS < 10 || s.HandoffTimeoutS > 1800:
		return errors.New("handoff_timeout_s is 10 to 1800.")
	case strings.TrimSpace(s.HandoffPrompt) == "" || utf8.RuneCountInString(s.HandoffPrompt) > 4000:
		return errors.New("handoff_prompt is 1 to 4000 characters.")
	case len(s.NudgePresets) > 12:
		return errors.New("at most 12 nudge_presets.")
	}
	for _, p := range s.NudgePresets {
		if strings.TrimSpace(p) == "" || utf8.RuneCountInString(p) > 200 {
			return errors.New("each nudge preset is 1 to 200 characters.")
		}
	}
	return nil
}

func (s *Server) settings(ctx context.Context) Settings {
	out := DefaultSettings()
	var raw string
	if err := s.DB.Reader.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key=?`, SettingsKey).Scan(&raw); err == nil {
		var saved Settings
		if json.Unmarshal([]byte(raw), &saved) == nil && saved.Validate() == nil {
			out = saved
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out
	}
	return out
}

func (s *Server) staleAfter(ctx context.Context) time.Duration {
	return time.Duration(s.settings(ctx).StaleAfterS) * time.Second
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, s.settings(r.Context()))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if !decodeBody(w, r, 32<<10, &in) {
		return
	}
	if in.NudgePresets == nil {
		in.NudgePresets = []string{}
	}
	if err := in.Validate(); err != nil {
		fail(w, 400, "invalid", err.Error())
		return
	}
	b, _ := json.Marshal(in)
	if err := s.DB.Writer.Submit(r.Context(), "agents_settings", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SettingsKey, string(b))
		return err
	}); err != nil {
		fail(w, 500, "internal", "The request could not be completed.")
		return
	}
	reply(w, 200, in)
}

package chatworkspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const settingsKey = "chat.settings"

// Settings are the user's chat preferences. Placement is a client concern and never
// appears in thread data.
type Settings struct {
	ScratchpadPlacement string `json:"scratchpad_placement"`
	// TerminalCloseOnComplete is the default for new terminal run cards' "Close when done" box.
	TerminalCloseOnComplete bool `json:"terminal_close_on_complete"`
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	out := Settings{ScratchpadPlacement: "drawer"}
	var raw string
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key=?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out.ScratchpadPlacement != "drawer" && out.ScratchpadPlacement != "aside" {
		out.ScratchpadPlacement = "drawer"
	}
	return out, nil
}

func (s *Store) SaveSettings(ctx context.Context, in Settings) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.DB.Writer.Submit(ctx, "chat_settings", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingsKey, string(b))
		return err
	})
}

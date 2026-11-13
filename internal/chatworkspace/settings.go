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
	// ContinueNewSessions is the default for a new Tether session's Continue toggle (its plan
	// follows to the session that continues it). On unless the user turned it off.
	ContinueNewSessions bool `json:"continue_new_sessions"`
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	out := Settings{ScratchpadPlacement: "drawer", ContinueNewSessions: true}
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

// SettingsPatch is a partial update: a nil field keeps its stored value.
type SettingsPatch struct {
	ScratchpadPlacement     *string
	TerminalCloseOnComplete *bool
	ContinueNewSessions     *bool
}

// UpdateSettings applies patch to the stored row in one write transaction and returns the
// result. It reads the row inside the transaction, so a concurrent save (the Settings registry
// writes the same row) is never lost, and it only touches the keys the patch names: a field
// the caller left out stays exactly as stored, including staying unset (the default).
func (s *Store) UpdateSettings(ctx context.Context, patch SettingsPatch) (Settings, error) {
	var out Settings
	err := s.DB.Writer.Submit(ctx, "chat_settings", func(ctx context.Context, tx *sql.Tx) error {
		row := map[string]any{}
		var raw string
		switch err := tx.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key=?`, settingsKey).Scan(&raw); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			if json.Unmarshal([]byte(raw), &row) != nil {
				row = map[string]any{} // an unreadable row reads as no overrides
			}
		}
		if patch.ScratchpadPlacement != nil {
			row["scratchpad_placement"] = *patch.ScratchpadPlacement
		}
		if patch.TerminalCloseOnComplete != nil {
			row["terminal_close_on_complete"] = *patch.TerminalCloseOnComplete
		}
		if patch.ContinueNewSessions != nil {
			if *patch.ContinueNewSessions {
				delete(row, "continue_new_sessions") // on is the default: not stored as an override
			} else {
				row["continue_new_sessions"] = false
			}
		}
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingsKey, string(b)); err != nil {
			return err
		}
		out = Settings{ScratchpadPlacement: "drawer", ContinueNewSessions: true}
		_ = json.Unmarshal(b, &out)
		if out.ScratchpadPlacement != "drawer" && out.ScratchpadPlacement != "aside" {
			out.ScratchpadPlacement = "drawer"
		}
		return nil
	})
	return out, err
}

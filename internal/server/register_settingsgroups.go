package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"

	"github.com/hollis-labs/tachyon/internal/agentsserve"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsreg"
)

func init() { registerStart(startSettingsGroups) }

// startSettingsGroups registers the first real groups in the settings registry (ST2). Each group's Storage maps
// the legacy app_settings row onto the registry fields, so the registry and the legacy routes (which stay as
// compatibility readers and writers) always agree. Not registered here: cache policy (nested per scope), Alerts
// (waits on its store).
func startSettingsGroups(_ context.Context, s *Server, db *localstore.Store) {
	reg := settingsRegistryOn(s, db)
	for _, g := range []settingsreg.Group{generalGroup(), chatGroup(), agentsGroup(), transcriptsGroup()} {
		if err := reg.Register(g); err != nil {
			slog.Warn("settings group not registered", "group", g.ID, "err", err)
		}
	}
}

func sstr(v string) admin.Scalar    { x, _ := admin.String(v); return x }
func sint(v int64) admin.Scalar     { x, _ := admin.Integer(v); return x }
func strPtr(v string) *admin.Scalar { x := sstr(v); return &x }
func intPtr(v int64) *admin.Scalar  { x := sint(v); return &x }
func fl(v float64) *float64         { return &v }
func ip(v int) *int                 { return &v }

func enumOf(v ...string) []admin.Scalar {
	out := make([]admin.Scalar, len(v))
	for i := range v {
		out[i] = sstr(v[i])
	}
	return out
}

func scalarString(v admin.Scalar) (string, bool) { s, ok := v.Value().(string); return s, ok }
func scalarInt(v admin.Scalar) (int, bool) {
	f, ok := v.Value().(float64)
	return int(f), ok
}

// rowStorage keeps one legacy app_settings row: decode turns the stored text into the override values, encode
// turns the overrides back into the legacy text. An empty override set removes the row, which is the reset.
type rowStorage struct {
	key    string
	decode func(raw string) map[string]admin.Scalar
	encode func(over map[string]admin.Scalar) (string, error)
}

func (r rowStorage) Load(ctx context.Context, q settingsreg.Querier) (settingsreg.Stored, error) {
	st := settingsreg.Stored{Over: map[string]admin.Scalar{}, Pending: map[string]bool{}}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, r.key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	for k, v := range r.decode(raw) {
		st.Over[k] = v
	}
	return st, nil
}

func (r rowStorage) Save(ctx context.Context, tx *sql.Tx, st settingsreg.Stored) error {
	if len(st.Over) == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key = ?`, r.key)
		return err
	}
	raw, err := r.encode(st.Over)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, r.key, raw)
	return err
}

func chatGroup() settingsreg.Group {
	return settingsreg.Group{ID: "chat", Label: "Chat",
		Fields: []admin.Field{{Key: "scratchpad_placement", Type: admin.StringType, Title: "Scratchpad placement",
			Default: strPtr("drawer"), Enum: enumOf("drawer", "aside"), Editable: true}},
		Storage: rowStorage{key: "chat.settings",
			decode: func(raw string) map[string]admin.Scalar {
				var v struct {
					P string `json:"scratchpad_placement"`
				}
				if json.Unmarshal([]byte(raw), &v) != nil || (v.P != "drawer" && v.P != "aside") {
					return nil
				}
				return map[string]admin.Scalar{"scratchpad_placement": sstr(v.P)}
			},
			encode: func(o map[string]admin.Scalar) (string, error) {
				p, _ := scalarString(o["scratchpad_placement"])
				b, err := json.Marshal(map[string]string{"scratchpad_placement": p})
				return string(b), err
			}}}
}

func agentsGroup() settingsreg.Group {
	d := agentsserve.DefaultSettings()
	return settingsreg.Group{ID: "agents", Label: "Agents",
		Fields: []admin.Field{
			{Key: "stale_after_s", Type: admin.IntegerType, Title: "Mark a session stale after (seconds)", Default: intPtr(int64(d.StaleAfterS)), Minimum: fl(60), Maximum: fl(7 * 24 * 3600), Editable: true},
			{Key: "handoff_prompt", Type: admin.StringType, Title: "Handoff prompt", Default: strPtr(d.HandoffPrompt), MaxLength: ip(4000), Editable: true},
			{Key: "handoff_timeout_s", Type: admin.IntegerType, Title: "Handoff timeout (seconds)", Default: intPtr(int64(d.HandoffTimeoutS)), Minimum: fl(10), Maximum: fl(1800), Editable: true},
			{Key: "nudge_presets", Type: admin.StringType, Title: "Nudge presets", Description: "One preset per line, up to 12, each up to 200 characters.", Default: strPtr(strings.Join(d.NudgePresets, "\n")), MaxLength: ip(2600), Editable: true},
		},
		Validate: func(_ context.Context, v map[string]admin.ResolvedValue) (admin.Validation, error) {
			var out admin.Validation
			out.Valid = true
			if p, ok := v["nudge_presets"]; ok && p.Present {
				if txt, _ := scalarString(p.Value); len(presetLines(txt)) > 12 || !presetsOK(presetLines(txt)) {
					out.Valid = false
					out.Errors = append(out.Errors, admin.ValidationError{Path: "/nudge_presets", Code: "invalid", Message: "Use at most 12 presets of 1 to 200 characters, one per line."})
				}
			}
			return out, nil
		},
		Storage: rowStorage{key: agentsserve.SettingsKey,
			decode: func(raw string) map[string]admin.Scalar {
				var s agentsserve.Settings
				if json.Unmarshal([]byte(raw), &s) != nil || s.Validate() != nil {
					return nil
				}
				return map[string]admin.Scalar{"stale_after_s": sint(int64(s.StaleAfterS)), "handoff_prompt": sstr(s.HandoffPrompt),
					"handoff_timeout_s": sint(int64(s.HandoffTimeoutS)), "nudge_presets": sstr(strings.Join(s.NudgePresets, "\n"))}
			},
			encode: func(o map[string]admin.Scalar) (string, error) {
				s := agentsserve.DefaultSettings() // keys without an override keep the built-in value
				if v, ok := o["stale_after_s"]; ok {
					s.StaleAfterS, _ = scalarInt(v)
				}
				if v, ok := o["handoff_prompt"]; ok {
					s.HandoffPrompt, _ = scalarString(v)
				}
				if v, ok := o["handoff_timeout_s"]; ok {
					s.HandoffTimeoutS, _ = scalarInt(v)
				}
				if v, ok := o["nudge_presets"]; ok {
					txt, _ := scalarString(v)
					s.NudgePresets = presetLines(txt)
				}
				if err := s.Validate(); err != nil {
					return "", err
				}
				b, err := json.Marshal(s)
				return string(b), err
			}}}
}

func presetLines(txt string) []string {
	out := []string{}
	for _, l := range strings.Split(txt, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func presetsOK(lines []string) bool {
	for _, l := range lines {
		if len([]rune(l)) > 200 {
			return false
		}
	}
	return true
}

// generalGroup holds the elevation lifetime now (Tangent applies it at its next restart); the cache policy
// fields join it once the per-scope policy mapping is designed.
func generalGroup() settingsreg.Group {
	return settingsreg.Group{ID: "general", Label: "General",
		Fields: []admin.Field{{Key: "elevation_ttl", Type: admin.StringType, Title: "Operator elevation lasts",
			Description: "always, never, or a number of days like 7d. Tangent applies it at its next restart.",
			Default:     strPtr(approvals.DefaultElevationTTL), Pattern: `^(always|never|[1-9][0-9]{0,2}d)$`, Editable: true,
			RestartRequired: true, ApplyTarget: "tangent"}},
		Operator: map[string]struct{}{"elevation_ttl": {}},
		Storage: rowStorage{key: approvals.ElevationTTLKey,
			decode: func(raw string) map[string]admin.Scalar {
				v, err := approvals.ParseElevationTTL(raw)
				if err != nil {
					return nil
				}
				return map[string]admin.Scalar{"elevation_ttl": sstr(v)}
			},
			encode: func(o map[string]admin.Scalar) (string, error) {
				raw, _ := scalarString(o["elevation_ttl"])
				return approvals.ParseElevationTTL(raw)
			}}}
}

// transcriptsGroup shows the environment-set limit read-only (cmd/tachyon reads it once at start).
func transcriptsGroup() settingsreg.Group {
	const env = "TACHYON_TRANSCRIPT_MAX_SESSIONS"
	return settingsreg.Group{ID: "transcripts", Label: "Transcripts", ReadOnly: true,
		Precedence: []admin.SourceKind{admin.EnvSource, admin.DefaultSource},
		Fields: []admin.Field{{Key: "max_sessions", Type: admin.IntegerType, Title: "Sessions kept", Default: intPtr(5000),
			Minimum: fl(200), Maximum: fl(20000), ReadOnlyReason: "Set by an environment variable; change it where Tachyon is started."}},
		Layers: func(context.Context) map[string]map[admin.SourceKind]settingsreg.Layer {
			n, err := strconv.Atoi(os.Getenv(env))
			if err != nil {
				return nil
			}
			return map[string]map[admin.SourceKind]settingsreg.Layer{"max_sessions": {admin.EnvSource: {Value: sint(int64(n)), Label: env}}}
		}}
}

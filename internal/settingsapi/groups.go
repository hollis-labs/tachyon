package settingsapi

import (
	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
)

// AppID is the admin app id every group scopes to (the library supports app scope only).
const AppID = "tachyon"

// Spec is one group as the stub declares it: the library Group fields plus the seed layers and the fields that
// need an operator-token session to write.
type Spec struct {
	ID, Label  string
	Fields     []admin.Field
	ReadOnly   bool                // no update/reset capability
	Precedence []admin.SourceKind  // highest first
	Seed       map[string]Seed     // initial layer values
	Operator   map[string]struct{} // keys that need operator-token to write
}

// Seed is the initial value of one key per layer.
type Seed struct {
	File, Env, Default any
	EnvName, FileName  string
}

func sc(v string) *admin.Scalar { s, _ := admin.String(v); return &s }
func ic(v int64) *admin.Scalar  { s, _ := admin.Integer(v); return &s }
func bc(v bool) *admin.Scalar   { s := admin.Boolean(v); return &s }
func f64(v float64) *float64    { return &v }
func n(v int) *int              { return &v }

func str(v string) admin.Scalar { return *sc(v) }
func vals(v ...string) []admin.Scalar {
	out := make([]admin.Scalar, len(v))
	for i := range v {
		out[i] = str(v[i])
	}
	return out
}

const envReason = "Set by an environment variable; change it where Tachyon is started."

// Specs returns the initial groups. ST2 registers the real ones from the same shapes; this is the contract the
// GUI renders. Alerts is declared scope user by the UI and served app-wide: it applies to everyone using this Tachyon.
func Specs() []Spec {
	std := []admin.SourceKind{admin.OverrideSource, admin.EnvSource, admin.FileSource, admin.DefaultSource}
	return []Spec{
		{ID: GroupGeneral, Label: "General", Precedence: std,
			Fields: []admin.Field{
				{Key: "cache_fresh_s", Type: admin.IntegerType, Title: "Fresh for (seconds)", Description: "How long a cached API answer counts as fresh.", Default: ic(30), Minimum: f64(0), Maximum: f64(3600), Editable: true},
				{Key: "cache_swr_s", Type: admin.IntegerType, Title: "Serve stale while revalidating (seconds)", Default: ic(300), Minimum: f64(0), Maximum: f64(86400), Editable: true},
				{Key: "cache_stale_if_error_s", Type: admin.IntegerType, Title: "Serve stale on error (seconds)", Default: ic(3600), Minimum: f64(0), Maximum: f64(86400), Editable: true},
				{Key: "cache_timeout_ms", Type: admin.IntegerType, Title: "Upstream timeout (ms)", Default: ic(5000), Minimum: f64(100), Maximum: f64(60000), ReadOnlyReason: envReason},
				{Key: "snapshot_first", Type: admin.BooleanType, Title: "Show the last snapshot first", Default: bc(true), Editable: true},
				{Key: "elevation_ttl", Type: admin.StringType, Title: "Operator elevation lasts", Description: "always, never, or a number of days like 7d. Tangent applies it at its next restart.",
					Default: sc("never"), Pattern: `^(always|never|[1-9][0-9]{0,2}d)$`, Editable: true, RestartRequired: true, ApplyTarget: "tangent"},
			},
			Seed: map[string]Seed{
				"cache_fresh_s":    {File: 45, FileName: "TACHYON_CACHE_POLICY_FILE"},
				"cache_timeout_ms": {Env: 8000, EnvName: "TACHYON_CACHE_TIMEOUT_MS"},
			},
			Operator: map[string]struct{}{"elevation_ttl": {}}},
		{ID: GroupChat, Label: "Chat", Precedence: std,
			Fields: []admin.Field{{Key: "scratchpad_placement", Type: admin.StringType, Title: "Scratchpad placement", Default: sc("side"), Enum: vals("side", "bottom"), Editable: true}}},
		{ID: GroupAgents, Label: "Agents", Precedence: std,
			Fields: []admin.Field{
				{Key: "stale_after_s", Type: admin.IntegerType, Title: "Mark a session stale after (seconds)", Default: ic(900), Minimum: f64(10), Maximum: f64(86400), Editable: true},
				{Key: "handoff_prompt", Type: admin.StringType, Title: "Handoff prompt", Default: sc("Summarise where you are and what is next."), MaxLength: n(2000), Editable: true},
				{Key: "handoff_timeout_s", Type: admin.IntegerType, Title: "Handoff timeout (seconds)", Default: ic(120), Minimum: f64(5), Maximum: f64(3600), Editable: true},
				{Key: "nudge_presets", Type: admin.StringType, Title: "Nudge presets", Description: "One preset per line.", Default: sc("Continue\nPlease summarise"), MaxLength: n(4000), Editable: true},
			}},
		{ID: GroupTranscripts, Label: "Transcripts", ReadOnly: true, Precedence: []admin.SourceKind{admin.EnvSource, admin.DefaultSource},
			Fields: []admin.Field{{Key: "max_sessions", Type: admin.IntegerType, Title: "Sessions kept", Default: ic(200), Minimum: f64(1), ReadOnlyReason: envReason}},
			Seed:   map[string]Seed{"max_sessions": {Env: 500, EnvName: "TACHYON_TRANSCRIPT_MAX_SESSIONS"}}},
		{ID: GroupAlerts, Label: "Alerts", Precedence: std,
			Fields: []admin.Field{
				{Key: "notify_level", Type: admin.StringType, Title: "Notify me about", Default: sc("warnings"), Enum: vals("blockers", "warnings", "everything"), Editable: true},
				{Key: "toasts", Type: admin.BooleanType, Title: "Show pop-up notices", Default: bc(true), Editable: true},
				{Key: "quiet_hours_enabled", Type: admin.BooleanType, Title: "Quiet hours", Default: bc(false), Editable: true},
				{Key: "quiet_hours_start", Type: admin.StringType, Title: "Quiet from", Default: sc("22:00"), Pattern: `^([01][0-9]|2[0-3]):[0-5][0-9]$`, Editable: true},
				{Key: "quiet_hours_end", Type: admin.StringType, Title: "Quiet until", Default: sc("07:00"), Pattern: `^([01][0-9]|2[0-3]):[0-5][0-9]$`, Editable: true},
				{Key: "tz", Type: admin.StringType, Title: "Time zone", Description: "An IANA zone like Europe/London.", Default: sc("UTC"), MaxLength: n(64), Editable: true},
				{Key: "muted_sources", Type: admin.StringType, Title: "Muted sources", Description: "Comma-separated source ids.", Default: sc(""), MaxLength: n(500), Editable: true},
				{Key: "coalesce_window_sec", Type: admin.IntegerType, Title: "Group repeats within (seconds)", Default: ic(60), Minimum: f64(0), Maximum: f64(3600), Editable: true},
			}},
	}
}

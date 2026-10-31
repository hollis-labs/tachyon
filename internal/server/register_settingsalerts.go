package server

import (
	"encoding/json"
	"strings"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/settingsreg"
)

func boolScalar(v bool) admin.Scalar { return admin.Boolean(v) }
func boolPtr(v bool) *admin.Scalar   { s := boolScalar(v); return &s }

func scalarBool(v admin.Scalar) (bool, bool) { b, ok := v.Value().(bool); return b, ok }

// alertsGroup is the Settings > Alerts group. It maps the flat fields onto the same app_settings row the
// /alerts/prefs routes keep (alerts.prefs.v1), so both always agree. App-wide, like every settings group.
func alertsGroup() settingsreg.Group {
	d := alertsapi.DefaultPrefs("UTC")
	hhmm := `^([01][0-9]|2[0-3]):[0-5][0-9]$`
	return settingsreg.Group{ID: "alerts", Label: "Alerts",
		Fields: []admin.Field{
			{Key: "notify_level", Type: admin.StringType, Title: "Notify me about", Default: strPtr(d.NotifyLevel), Enum: enumOf(alertsapi.NotifyLevels...), Editable: true},
			{Key: "toasts", Type: admin.BooleanType, Title: "Show pop-up notices", Default: boolPtr(d.Toasts), Editable: true},
			{Key: "quiet", Type: admin.BooleanType, Title: "Quiet (no pop-ups)", Default: boolPtr(d.Quiet), Editable: true},
			{Key: "quiet_hours_enabled", Type: admin.BooleanType, Title: "Quiet hours", Default: boolPtr(d.QuietHours.Enabled), Editable: true},
			{Key: "quiet_hours_start", Type: admin.StringType, Title: "Quiet from", Default: strPtr(d.QuietHours.Start), Pattern: hhmm, Editable: true},
			{Key: "quiet_hours_end", Type: admin.StringType, Title: "Quiet until", Default: strPtr(d.QuietHours.End), Pattern: hhmm, Editable: true},
			{Key: "tz", Type: admin.StringType, Title: "Time zone", Description: "An IANA zone like Europe/London.", Default: strPtr(d.QuietHours.TZ), MaxLength: ip(64), Editable: true},
			{Key: "muted_sources", Type: admin.StringType, Title: "Muted sources", Description: "Comma-separated source ids.", Default: strPtr(""), MaxLength: ip(500), Editable: true},
			{Key: "coalesce_window_sec", Type: admin.IntegerType, Title: "Group repeats within (seconds)", Default: intPtr(int64(d.CoalesceWindowS)), Minimum: fl(0), Maximum: fl(float64(alertsapi.CoalesceMaxSec)), Editable: true},
		},
		Storage: rowStorage{key: alertstore.PrefsKey,
			decode: func(raw string) map[string]admin.Scalar {
				var p alertsapi.Prefs
				if json.Unmarshal([]byte(raw), &p) != nil || alertsapi.ValidPrefs(p) != "" {
					return nil
				}
				return map[string]admin.Scalar{"notify_level": sstr(p.NotifyLevel), "toasts": boolScalar(p.Toasts), "quiet": boolScalar(p.Quiet),
					"quiet_hours_enabled": boolScalar(p.QuietHours.Enabled), "quiet_hours_start": sstr(p.QuietHours.Start), "quiet_hours_end": sstr(p.QuietHours.End),
					"tz": sstr(p.QuietHours.TZ), "muted_sources": sstr(strings.Join(p.MutedSources, ",")), "coalesce_window_sec": sint(int64(p.CoalesceWindowS))}
			},
			encode: func(o map[string]admin.Scalar) (string, error) {
				p := alertsapi.DefaultPrefs("UTC") // keys without an override keep the built-in value
				if v, ok := o["notify_level"]; ok {
					p.NotifyLevel, _ = scalarString(v)
				}
				if v, ok := o["toasts"]; ok {
					p.Toasts, _ = scalarBool(v)
				}
				if v, ok := o["quiet"]; ok {
					p.Quiet, _ = scalarBool(v)
				}
				if v, ok := o["quiet_hours_enabled"]; ok {
					p.QuietHours.Enabled, _ = scalarBool(v)
				}
				if v, ok := o["quiet_hours_start"]; ok {
					p.QuietHours.Start, _ = scalarString(v)
				}
				if v, ok := o["quiet_hours_end"]; ok {
					p.QuietHours.End, _ = scalarString(v)
				}
				if v, ok := o["tz"]; ok {
					p.QuietHours.TZ, _ = scalarString(v)
				}
				if v, ok := o["muted_sources"]; ok {
					txt, _ := scalarString(v)
					p.MutedSources = []string{}
					for _, s := range strings.Split(txt, ",") {
						if s = strings.TrimSpace(s); s != "" {
							p.MutedSources = append(p.MutedSources, s)
						}
					}
				}
				if v, ok := o["coalesce_window_sec"]; ok {
					p.CoalesceWindowS, _ = scalarInt(v)
				}
				if msg := alertsapi.ValidPrefs(p); msg != "" {
					return "", errString(msg)
				}
				b, err := json.Marshal(p)
				return string(b), err
			}}}
}

type errString string

func (e errString) Error() string { return string(e) }

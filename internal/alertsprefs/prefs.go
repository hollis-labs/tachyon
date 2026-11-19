// Package alertsprefs holds the Alerts preference semantics (AL6) as pure functions: notify level, quiet,
// quiet hours in the operator's one time zone, muted sources, the critical exception, and whether an event
// toasts. The store (AL1) persists alertsapi.Prefs; AL3/AL4 call ToastFor when they emit a frame.
// One operator, one shared state: nothing here is per device. Design: design_tachyon_alerts_backend_20261030
// sections 5 and 6.
package alertsprefs

import (
	"strings"
	"time"
	_ "time/tzdata" // zone names must resolve on hosts without a zoneinfo database

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// MinSeverity is the lowest severity a notify level lets through: blockers = error, warnings = warning,
// everything = debug. An unknown level is treated as warnings (the default).
func MinSeverity(level string) string {
	switch level {
	case "blockers":
		return "error"
	case "everything":
		return "debug"
	}
	return "warning"
}

func sevIdx(s string) int {
	for i, v := range alertsapi.Severities {
		if v == s {
			return i
		}
	}
	return -1
}

// Validate is alertsapi.ValidPrefs plus a real zone lookup. It returns the first problem or "".
func Validate(p alertsapi.Prefs) string {
	if msg := alertsapi.ValidPrefs(p); msg != "" {
		return msg
	}
	if _, err := time.LoadLocation(p.QuietHours.TZ); err != nil {
		return "quiet_hours.tz is not a known IANA time zone."
	}
	return ""
}

// Normalize trims muted sources, drops empty and case-insensitive duplicates, and never returns nil lists.
func Normalize(p alertsapi.Prefs) alertsapi.Prefs {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range p.MutedSources {
		s = strings.TrimSpace(s)
		if k := strings.ToLower(s); s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	p.MutedSources = out
	p.QuietHours.TZ = strings.TrimSpace(p.QuietHours.TZ)
	return p
}

// MigrateOnce imports the old localStorage prefs only when the server has none. existing is nil until prefs
// were saved or migrated. It reports whether the legacy value was applied; the call is idempotent.
func MigrateOnce(existing *alertsapi.Prefs, legacy alertsapi.LegacyPrefs, tz string) (alertsapi.Prefs, bool) {
	if existing != nil {
		return *existing, false
	}
	return Normalize(alertsapi.MigratePrefs(legacy, tz)), true
}

// InQuietHours reports whether now falls in the quiet window, read in the saved zone (not the device's). A
// window that wraps midnight (22:00-07:00) is supported; start == end is an empty window. Disabled, or an
// unknown zone, is never quiet (fail open: an unreadable setting must not silence alerts).
func InQuietHours(q alertsapi.QuietHours, now time.Time) bool {
	if !q.Enabled {
		return false
	}
	loc, err := time.LoadLocation(q.TZ)
	if err != nil {
		return false
	}
	start, ok1 := minutes(q.Start)
	end, ok2 := minutes(q.End)
	if !ok1 || !ok2 || start == end {
		return false
	}
	l := now.In(loc)
	cur := l.Hour()*60 + l.Minute()
	if start < end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end
}

func minutes(hhmm string) (int, bool) {
	if len(hhmm) != 5 || hhmm[2] != ':' {
		return 0, false
	}
	h := int(hhmm[0]-'0')*10 + int(hhmm[1]-'0')
	m := int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
	return h*60 + m, h < 24 && m < 60
}

// Muted reports whether source matches a muted entry (case-insensitive substring, as the old UI did).
func Muted(p alertsapi.Prefs, source string) bool {
	src := strings.ToLower(source)
	for _, m := range p.MutedSources {
		if m != "" && strings.Contains(src, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// Change says why a frame is being emitted.
type Change string

const (
	Created   Change = "created"   // first event of a new alert
	Coalesced Change = "coalesced" // a repeat folded into an open alert; severity unchanged
	Raised    Change = "raised"    // a repeat that raised the severity (also created unread)
	Woke      Change = "woke"      // snooze or defer ended
	Escalated Change = "escalated" // unread past the rule deadline
	Settled   Change = "settled"   // state changes (read, dismiss, settle): never toast
)

// ToastFor decides the `toast` flag for an alert frame.
//   - Critical always toasts: it passes the master switch, Quiet, quiet hours, muting and the notify level.
//     (Decision for review: the master "toasts" switch does not silence critical.)
//   - Otherwise the master switch must be on, Quiet and quiet hours off, the source not muted, and the
//     severity at or above the notify level.
//   - A coalesced repeat never toasts; a raise, a wake and an escalation do; state changes never do.
//
// The alert is still stored and counted either way; the flag only drives the toast.
func ToastFor(p alertsapi.Prefs, ch Change, severity, source string, now time.Time) bool {
	switch ch {
	case Coalesced, Settled:
		return false
	case Created, Raised, Woke, Escalated:
	default:
		return false
	}
	if severity == "critical" {
		return true
	}
	if !p.Toasts || p.Quiet || InQuietHours(p.QuietHours, now) || Muted(p, source) {
		return false
	}
	return sevIdx(severity) >= sevIdx(MinSeverity(p.NotifyLevel))
}

// BellCounts reports whether an alert counts toward the bell badge under the notify level. Everything is
// stored; the badge counts only what the level lets through (critical always counts).
func BellCounts(p alertsapi.Prefs, severity string) bool {
	return severity == "critical" || sevIdx(severity) >= sevIdx(MinSeverity(p.NotifyLevel))
}

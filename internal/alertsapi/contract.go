// Package alertsapi is the contract for the Alerts backend: durable, server-side alerts with stable
// ids, shared operator state, a server-owned rule pack, prefs, and one SSE stream. The stub in this
// package serves fixtures and keeps in-memory state for GUI development (TACHYON_ALERTS_STUBS=1);
// later slices (AL1..AL6) replace its routes one by one. Design: tesseract key
// design_tachyon_alerts_backend_20261030; UI rules: flow_tachyon_alerts_addendum_cannot_feed_20261030.
package alertsapi

import (
	"regexp"
	"strings"
)

// Prefix is the alerts route prefix. StatePrefix serves the existing StateStore client calls
// (GET /console/state, PUT /console/state/{key}); they are compatibility routes until AL8 lands.
const (
	Prefix      = "/api/plugins/console/v1/alerts"
	StatePrefix = "/api/plugins/console/v1/console/state"
)

// Route is "METHOD path"; Path is relative to Base. Writes are POST, PUT.
type Route struct {
	Pattern string
	Base    string
}

// Routes lists every route the stub serves. Each is mounted by explicit pattern: the /api/plugins/
// catch-all is unguarded. More specific literals ("/counts", "/ops/bulk") win over "/{id}" in ServeMux.
var Routes = []Route{
	{"GET ", Prefix}, {"GET /counts", Prefix}, {"GET /{id}", Prefix}, {"POST /{id}/ops", Prefix},
	{"POST /ops/bulk", Prefix}, {"GET /prefs", Prefix}, {"PUT /prefs", Prefix}, {"POST /prefs/migrate", Prefix},
	{"GET /stream", Prefix}, {"GET /rules", Prefix}, {"GET /sources", Prefix}, {"GET /openapi.yaml", Prefix},
	{"GET ", StatePrefix}, {"PUT /{key}", StatePrefix},
}

// Severities in ascending order (same as the console event envelope).
var Severities = []string{"debug", "info", "notice", "warning", "error", "critical"}

// Alert status. active alerts are the tray; the rest are History (30 days).
var Statuses = []string{"active", "dismissed", "snoozed", "deferred", "settled"}

// Ops accepted by POST /{id}/ops; bulk accepts BulkOps only.
var (
	Ops     = []string{"read", "unread", "dismiss", "snooze", "defer", "settle", "reopen"}
	BulkOps = []string{"read", "dismiss"}
)

// SnoozePresets for op snooze (or until_ms).
var SnoozePresets = []string{"15m", "1h", "4h", "tomorrow"}

// Icons mirror the UI's AlertIconKey.
var Icons = []string{"info", "task", "agent", "message", "approval", "pr", "warning", "error"}

// NotifyLevels: blockers = error and above, warnings = warning and above, everything = debug and above.
var NotifyLevels = []string{"blockers", "warnings", "everything"}

// SSE event types on GET /stream. Every alert.* frame carries toast (server-computed).
var StreamEvents = []string{"alert.created", "alert.updated", "alert.state", "alert.woke", "alert.escalated",
	"alert.cleared", "alert.removed", "counts", "prefs", "resync"}

// Error codes in {code,message}.
const (
	CodeInvalid         = "invalid"
	CodeNotFound        = "not_found"
	CodeVersionConflict = "version_conflict"
	CodeSeqConflict     = "seq_conflict"
	CodeForbidden       = "forbidden"
	CodeRateLimited     = "rate_limited"
	CodeUnavailable     = "unavailable"
)

// Limits the real build must enforce too.
const (
	TitleMax        = 120
	SummaryMax      = 300
	NoteMax         = 500
	TimelineMax     = 50
	MutedSourcesMax = 50
	MutedSourceMax  = 64
	CoalesceMaxSec  = 3600
	ListLimitMax    = 200
	ListLimitDef    = 50
)

type Subject struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Primary struct {
	Label string `json:"label"`
	// Href is a same-origin path or an http(s) URL; the server validates it (SafeHref).
	Href string `json:"href"`
}

// UntilRef defers an alert until a normalized event arrives for the subject.
type UntilRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

type Defer struct {
	UntilMS        *int64    `json:"until_ms,omitempty"`
	UntilRefChange *UntilRef `json:"until_ref_change,omitempty"`
}

// Alert is the projection used by lists, counts-adjacent events and ops responses.
type Alert struct {
	AlertID      string    `json:"alert_id"`
	Seq          int64     `json:"seq"`
	Kind         string    `json:"kind"`
	RuleID       string    `json:"rule_id"`
	Severity     string    `json:"severity"`
	SeverityBase string    `json:"severity_base"`
	NeedsYou     bool      `json:"needs_you"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Source       string    `json:"source"`
	Icon         string    `json:"icon"`
	Status       string    `json:"status"`
	Read         bool      `json:"read"`
	Count        int       `json:"count"`
	CoalesceKey  string    `json:"coalesce_key"`
	FirstAtMS    int64     `json:"first_at_ms"`
	LastAtMS     int64     `json:"last_at_ms"`
	SnoozeUntil  *int64    `json:"snooze_until_ms"`
	WakeAtMS     *int64    `json:"wake_at_ms"`
	Defer        *Defer    `json:"defer"`
	Note         string    `json:"note"`
	Resolution   string    `json:"resolution"`
	ResolvedBy   string    `json:"resolved_by"`
	Primary      *Primary  `json:"primary"`
	Subjects     []Subject `json:"subjects"`
	EscalateAtMS *int64    `json:"escalate_at_ms"`
	EscalatedAt  *int64    `json:"escalated_at_ms"`
	Version      int64     `json:"version"`
	CreatedMS    int64     `json:"created_ms"`
	UpdatedMS    int64     `json:"updated_ms"`
	// Display fields, all optional and omitted when unknown (see Decorate). Title, Source and Subjects remain the fallback.
	DisplayTitle string       `json:"display_title,omitempty"`
	SubjectView  *SubjectView `json:"subject,omitempty"`
	Links        []Link       `json:"links,omitempty"`
	SourceLabel  string       `json:"source_label,omitempty"`
}

// Counts drive the bell badge.
type Counts struct {
	Unread     int            `json:"unread"`
	Active     int            `json:"active"`
	NeedsYou   int            `json:"needs_you"`
	BySeverity map[string]int `json:"by_severity"`
	Seq        int64          `json:"seq"`
}

// Prefs is one row of app settings (key alerts.prefs.v1); one operator, so app = user.
type Prefs struct {
	NotifyLevel     string     `json:"notify_level"`
	Toasts          bool       `json:"toasts"`
	Quiet           bool       `json:"quiet"`
	QuietHours      QuietHours `json:"quiet_hours"`
	MutedSources    []string   `json:"muted_sources"`
	CoalesceWindowS int        `json:"coalesce_window_sec"`
}

type QuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
	// TZ is an IANA name (one timezone for the operator, not per device).
	TZ string `json:"tz"`
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ValidPrefs reports the first problem with a Prefs value, or "".
func ValidPrefs(p Prefs) string {
	switch {
	case !in(p.NotifyLevel, NotifyLevels):
		return "notify_level must be blockers, warnings or everything."
	case p.CoalesceWindowS < 0 || p.CoalesceWindowS > CoalesceMaxSec:
		return "coalesce_window_sec must be 0..3600."
	case len(p.MutedSources) > MutedSourcesMax:
		return "too many muted_sources."
	case !hhmm.MatchString(p.QuietHours.Start) || !hhmm.MatchString(p.QuietHours.End):
		return "quiet_hours start and end must be HH:MM."
	case strings.TrimSpace(p.QuietHours.TZ) == "" || len(p.QuietHours.TZ) > 64:
		return "quiet_hours.tz must be an IANA time zone name."
	}
	for _, s := range p.MutedSources {
		if s == "" || len(s) > MutedSourceMax {
			return "muted_sources entries must be 1..64 characters."
		}
	}
	return ""
}

// LegacyPrefs is the old localStorage value (console.alerts.prefs.v1), accepted once by POST /prefs/migrate.
type LegacyPrefs struct {
	Quiet             bool     `json:"quiet"`
	ToastMinSeverity  string   `json:"toastMinSeverity"`
	BellMinSeverity   string   `json:"bellMinSeverity"`
	QuietHoursEnabled bool     `json:"quietHoursEnabled"`
	QuietHoursStart   string   `json:"quietHoursStart"`
	QuietHoursEnd     string   `json:"quietHoursEnd"`
	MutedSources      []string `json:"mutedSources"`
	CoalesceWindowSec int      `json:"coalesceWindowSec"`
}

// NotifyLevelFor maps a legacy minimum severity to a notify level: error and above is blockers,
// warning is warnings, anything lower is everything.
func NotifyLevelFor(minSeverity string) string {
	switch idx(minSeverity) {
	case 4, 5:
		return "blockers"
	case 3:
		return "warnings"
	}
	return "everything"
}

// MigratePrefs maps the legacy value onto Prefs. The toast threshold decides notify_level (the
// golden has one "Notify me about" control); the bell threshold is dropped. tz is the caller's
// zone. An invalid legacy value falls back to the default for that field.
func MigratePrefs(l LegacyPrefs, tz string) Prefs {
	p := DefaultPrefs(tz)
	p.NotifyLevel = NotifyLevelFor(l.ToastMinSeverity)
	p.Quiet = l.Quiet
	p.QuietHours.Enabled = l.QuietHoursEnabled
	if hhmm.MatchString(l.QuietHoursStart) {
		p.QuietHours.Start = l.QuietHoursStart
	}
	if hhmm.MatchString(l.QuietHoursEnd) {
		p.QuietHours.End = l.QuietHoursEnd
	}
	for _, s := range l.MutedSources {
		if s != "" && len(s) <= MutedSourceMax && len(p.MutedSources) < MutedSourcesMax {
			p.MutedSources = append(p.MutedSources, s)
		}
	}
	if l.CoalesceWindowSec >= 0 && l.CoalesceWindowSec <= CoalesceMaxSec {
		p.CoalesceWindowS = l.CoalesceWindowSec
	}
	return p
}

func DefaultPrefs(tz string) Prefs {
	if tz == "" {
		tz = "UTC"
	}
	return Prefs{NotifyLevel: "warnings", Toasts: true, QuietHours: QuietHours{Start: "22:00", End: "07:00", TZ: tz},
		MutedSources: []string{}, CoalesceWindowS: 120}
}

// SafeHref reports whether href may be rendered as a link: a same-origin path or an http(s) URL.
func SafeHref(href string) bool {
	if strings.HasPrefix(href, "/") {
		return !strings.HasPrefix(href, "//") && !strings.ContainsAny(href, "\r\n\\")
	}
	return (strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://")) && !strings.ContainsAny(href, " \r\n")
}

func idx(sev string) int {
	for i, s := range Severities {
		if s == sev {
			return i
		}
	}
	return -1
}

func in(s string, set []string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

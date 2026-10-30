package alertsprefs

import (
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

func base() alertsapi.Prefs { return alertsapi.DefaultPrefs("UTC") }

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestMinSeverityPerLevel(t *testing.T) {
	for level, want := range map[string]string{"blockers": "error", "warnings": "warning", "everything": "debug", "": "warning", "x": "warning"} {
		if got := MinSeverity(level); got != want {
			t.Errorf("%q -> %s, want %s", level, got, want)
		}
	}
}

func TestQuietHours(t *testing.T) {
	q := alertsapi.QuietHours{Enabled: true, Start: "22:00", End: "07:00", TZ: "Europe/Berlin"}
	for ts, want := range map[string]bool{
		"2026-10-30T21:30:00Z": true,  // 22:30 in Berlin (CET after 25 Oct), inside the wrap
		"2026-10-30T05:59:00Z": true,  // 06:59
		"2026-10-30T06:00:00Z": false, // 07:00 sharp is outside
		"2026-10-30T12:00:00Z": false,
		"2026-10-30T20:59:00Z": false, // 21:59 local
		"2026-10-30T21:00:00Z": true,  // 22:00 local
	} {
		if got := InQuietHours(q, at(ts)); got != want {
			t.Errorf("%s: %v, want %v", ts, got, want)
		}
	}
	day := alertsapi.QuietHours{Enabled: true, Start: "09:00", End: "17:00", TZ: "UTC"}
	if !InQuietHours(day, at("2026-10-30T09:00:00Z")) || InQuietHours(day, at("2026-10-30T17:00:00Z")) {
		t.Error("same-day window is [start, end)")
	}
	for _, bad := range []alertsapi.QuietHours{
		{Enabled: false, Start: "00:00", End: "23:59", TZ: "UTC"},
		{Enabled: true, Start: "08:00", End: "08:00", TZ: "UTC"},
		{Enabled: true, Start: "25:00", End: "07:00", TZ: "UTC"},
		{Enabled: true, Start: "22:00", End: "07:00", TZ: "Mars/Olympus"},
	} {
		if InQuietHours(bad, at("2026-10-30T03:00:00Z")) {
			t.Errorf("%+v must fail open", bad)
		}
	}
}

func TestToastFor(t *testing.T) {
	noon := at("2026-10-30T12:00:00Z")
	mk := func(f func(*alertsapi.Prefs)) alertsapi.Prefs { p := base(); f(&p); return p }
	cases := []struct {
		name string
		p    alertsapi.Prefs
		ch   Change
		sev  string
		src  string
		now  time.Time
		want bool
	}{
		{"warning at default level toasts", base(), Created, "warning", "agent", noon, true},
		{"notice below default level", base(), Created, "notice", "agent", noon, false},
		{"blockers wants error", mk(func(p *alertsapi.Prefs) { p.NotifyLevel = "blockers" }), Created, "warning", "agent", noon, false},
		{"blockers toasts error", mk(func(p *alertsapi.Prefs) { p.NotifyLevel = "blockers" }), Created, "error", "agent", noon, true},
		{"everything toasts debug", mk(func(p *alertsapi.Prefs) { p.NotifyLevel = "everything" }), Created, "debug", "agent", noon, true},
		{"quiet silences error", mk(func(p *alertsapi.Prefs) { p.Quiet = true }), Created, "error", "agent", noon, false},
		{"quiet passes critical", mk(func(p *alertsapi.Prefs) { p.Quiet = true }), Created, "critical", "agent", noon, true},
		{"master off silences error", mk(func(p *alertsapi.Prefs) { p.Toasts = false }), Created, "error", "agent", noon, false},
		{"master off still passes critical", mk(func(p *alertsapi.Prefs) { p.Toasts = false }), Created, "critical", "agent", noon, true},
		{"muted source", mk(func(p *alertsapi.Prefs) { p.MutedSources = []string{"TORQ"} }), Created, "error", "torque", noon, false},
		{"muted source passes critical", mk(func(p *alertsapi.Prefs) { p.MutedSources = []string{"torque"} }), Created, "critical", "Torque", noon, true},
		{"quiet hours silence", mk(func(p *alertsapi.Prefs) {
			p.QuietHours = alertsapi.QuietHours{Enabled: true, Start: "22:00", End: "07:00", TZ: "UTC"}
		}), Created, "error", "a", at("2026-10-30T23:00:00Z"), false},
		{"quiet hours pass critical", mk(func(p *alertsapi.Prefs) {
			p.QuietHours = alertsapi.QuietHours{Enabled: true, Start: "22:00", End: "07:00", TZ: "UTC"}
		}), Created, "critical", "a", at("2026-10-30T23:00:00Z"), true},
		{"coalesced repeat never toasts", base(), Coalesced, "error", "a", noon, false},
		{"coalesced critical repeat never toasts", base(), Coalesced, "critical", "a", noon, false},
		{"raised toasts", base(), Raised, "error", "a", noon, true},
		{"wake toasts", base(), Woke, "warning", "a", noon, true},
		{"escalation re-toasts", base(), Escalated, "error", "a", noon, true},
		{"state changes never toast", base(), Settled, "critical", "a", noon, false},
		{"unknown change never toasts", base(), Change("?"), "critical", "a", noon, false},
	}
	for _, c := range cases {
		if got := ToastFor(c.p, c.ch, c.sev, c.src, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBellCounts(t *testing.T) {
	p := base()
	if BellCounts(p, "notice") || !BellCounts(p, "warning") || !BellCounts(p, "critical") {
		t.Error("warnings level")
	}
	p.NotifyLevel = "blockers"
	if BellCounts(p, "warning") || !BellCounts(p, "error") || !BellCounts(p, "critical") {
		t.Error("blockers level")
	}
}

func TestValidateAndNormalize(t *testing.T) {
	p := base()
	p.QuietHours.TZ = "Mars/Olympus"
	if Validate(p) == "" {
		t.Error("unknown zone must fail")
	}
	p.QuietHours.TZ = "Asia/Kolkata"
	if m := Validate(p); m != "" {
		t.Errorf("valid prefs: %s", m)
	}
	p.MutedSources = []string{" Torque ", "torque", "", "Agents"}
	p.QuietHours.TZ = " UTC "
	n := Normalize(p)
	if len(n.MutedSources) != 2 || n.MutedSources[0] != "Torque" || n.QuietHours.TZ != "UTC" {
		t.Errorf("%+v", n)
	}
	if Normalize(alertsapi.Prefs{}).MutedSources == nil {
		t.Error("muted_sources must be [] not null")
	}
}

func TestMigrateOnce(t *testing.T) {
	legacy := alertsapi.LegacyPrefs{Quiet: true, ToastMinSeverity: "error", MutedSources: []string{"torque", "Torque"}, CoalesceWindowSec: 300}
	got, applied := MigrateOnce(nil, legacy, "Europe/Berlin")
	if !applied || got.NotifyLevel != "blockers" || !got.Quiet || got.CoalesceWindowS != 300 || len(got.MutedSources) != 1 || got.QuietHours.TZ != "Europe/Berlin" {
		t.Errorf("%+v applied=%v", got, applied)
	}
	again, applied := MigrateOnce(&got, alertsapi.LegacyPrefs{ToastMinSeverity: "debug"}, "UTC")
	if applied || again.NotifyLevel != "blockers" {
		t.Errorf("second call must not apply: %+v", again)
	}
}

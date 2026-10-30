// Package alertsource is the Alerts ingest side (AL2): normalized events, dedupe keys, supervised source
// adapters with resume cursors, and per-source rate limits. It stores nothing: the alert store (AL1) supplies
// the CursorStore and Sink. Design: design_tachyon_alerts_backend_20261030, sections 2 and 9.
package alertsource

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
)

// Event is the normalized event every adapter produces.
type Event struct {
	Source        string
	SourceEventID string // empty when the source has no stable id (Torque)
	Kind          string
	AtMS          int64
	Subjects      []alertsapi.Subject
	CorrelationID string
	CoalesceKey   string
	SeverityHint  string
	Payload       map[string]any // sanitized by the adapter
}

// Sink receives events that passed dedupe-independent checks; the store dedupes on DedupeKey (UNIQUE).
type Sink interface {
	Ingest(Event) error
	// Suppressed reports events dropped by the rate limit, so the store can raise "N events dropped".
	Suppressed(source string, n int)
}

// CursorStore persists the resume cursor per source (table alert_source_cursor in AL1).
type CursorStore interface {
	Cursor(source string) (string, error)
	SetCursor(source, cursor string) error
}

// DedupeKey is the idempotency key for the store's UNIQUE alert_events.dedupe_key. Sources with stable ids use
// source|id. Others (Torque) hash type|subject|revision, falling back to a one-minute bucket, so a reconnect
// replaying the same change is ignored.
func DedupeKey(e Event) string {
	if e.SourceEventID != "" {
		return e.Source + "|" + e.SourceEventID
	}
	subj := ""
	if len(e.Subjects) > 0 {
		subj = e.Subjects[0].Kind + ":" + e.Subjects[0].Ref
	}
	rev := ""
	for _, k := range []string{"revision", "updated_at"} {
		if v, ok := e.Payload[k]; ok {
			rev = fmt.Sprint(v)
			break
		}
	}
	if rev == "" {
		rev = "m" + strconv.FormatInt(e.AtMS/60000, 10)
	}
	sum := sha256.Sum256([]byte(e.Kind + "|" + subj + "|" + rev))
	return e.Source + "|h:" + hex.EncodeToString(sum[:12])
}

// RuleEvent maps a normalized event onto the console envelope the rule evaluator takes
// (subject kind -> type, ref -> id). Severity falls back to "info" when the source gave no hint.
func (e Event) RuleEvent() alertsrules.Event {
	sev := e.SeverityHint
	if sev == "" {
		sev = "info"
	}
	out := alertsrules.Event{ID: DedupeKey(e), TS: e.AtMS, Kind: e.Kind, Source: alertsrules.Source{ID: e.Source}, Severity: sev, Payload: e.Payload}
	for _, s := range e.Subjects {
		out.Subjects = append(out.Subjects, alertsrules.Subject{Type: s.Kind, ID: s.Ref, Label: s.Label, URL: s.URL})
	}
	return out
}

// Clean makes untrusted text safe to store: one line, no control characters, at most max runes.
func Clean(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.Join(strings.Fields(s), " ") {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if n++; n > max {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

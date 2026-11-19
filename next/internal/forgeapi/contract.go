// Package forgeapi is the contract of Tachyon's Pull requests backend (the Repos nav): the route
// prefixes, the pending inbox's reasons, the limits and the identifiers (repo parts, head shas, PR keys)
// the forge packages share. Ported from the old build's internal/forgeapi (slice B11a, the read side);
// the write-side vocabulary (merge, review, assign) comes with B11b. The fixture stub is not ported.
package forgeapi

import (
	"fmt"
	"regexp"
)

// Prefix is the forge route family; StatePrefix the shared item-state store the console UI calls.
const (
	Prefix      = "/api/plugins/console/v1/forge"
	StatePrefix = "/api/plugins/console/v1/console/state"
)

// Reasons a PR is in the pending inbox, in the order they are computed.
var Reasons = []string{"checks_failed", "changes_requested", "conflicting", "review_requested", "mentioned", "unassigned", "stale"}

// TriageStatuses are the shared item state's statuses (the console UI's StateStore vocabulary).
var TriageStatuses = []string{"unread", "read", "dismissed", "snoozed", "settled"}

// StreamEvents are the SSE event types on GET /stream. Ids are a replayable sequence; a gap yields resync.
var StreamEvents = []string{"pr.updated", "pr.merged", "triage.changed", "status", "resync"}

// FeedKinds are the alert-feed event kinds the poller produces in-process (not SSE).
var FeedKinds = []string{"pr.review_requested", "pr.checks_failed", "pr.changes_requested", "pr.conflicting", "pr.stale", "pr.merged"}

// Limits (design section 7).
const (
	MaxPendingDefault  = 100
	PendingPage        = 25
	DiffTruncateBytes  = 200 << 10
	StaleDaysDefault   = 7
	PollSecondsDefault = 60
	RateFloorDefault   = 200
)

var (
	ownerName  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	headSHA    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	prKeyShape = regexp.MustCompile(`^github:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)
)

// ValidRepoPart reports whether an owner or repo name is safe to use in a path.
func ValidRepoPart(s string) bool {
	return s != "" && len(s) <= 100 && s != "." && s != ".." && ownerName.MatchString(s)
}

// ValidHeadSHA reports a full 40-hex commit id.
func ValidHeadSHA(s string) bool { return headSHA.MatchString(s) }

// PRKey is the stable id of a pull request: github:owner/name#number.
func PRKey(owner, name string, number int) string {
	return fmt.Sprintf("github:%s/%s#%d", owner, name, number)
}

// ParsePRKey splits a PR key.
func ParsePRKey(k string) (owner, name string, number int, ok bool) {
	m := prKeyShape.FindStringSubmatch(k)
	if m == nil || !ValidRepoPart(m[1]) || !ValidRepoPart(m[2]) {
		return "", "", 0, false
	}
	if _, err := fmt.Sscanf(m[3], "%d", &number); err != nil || number < 1 {
		return "", "", 0, false
	}
	return m[1], m[2], number, true
}

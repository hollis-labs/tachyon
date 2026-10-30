// Package transcriptsapi is the contract for the Transcripts backend extension
// (tool-call cards, content search, in-session find, freshness) and a fixture-backed
// stub of it for GUI development (TACHYON_TRANSCRIPTS_STUBS=1). The real bridge is
// internal/transcripts; TR1 implements Stage 1 of these routes there. Nothing here
// talks to Fragments Engine.
package transcriptsapi

import "regexp"

// Prefix is the same prefix the real bridge serves; with the stub flag on the stub
// replaces the bridge for the whole prefix.
const Prefix = "/api/plugins/console/v1/transcripts"

// Fixture session ids (sha256 of the label, 64 hex like real ids).
const (
	SessionClaude  = "d3cf2597d1b39167aa6e3a423879aa980691c155eacd3a50c3882646b3234fb7" // 60 turns, tool targets, 2 errors
	SessionCodex   = "e11020b390a5d4d8a7992e8c29a213dfe2639b4d72afdce16ee74d1c86a4f039" // heuristic errors, no targets
	SessionChatGPT = "fa6b92ee82e23741c5f415097de72ca70a1f2e5b4e60457990fe8b8b07c61c32" // export: no tool calls
	SessionUndated = "7e171628852d846518f2a1cec22690ba1dcc48903e2fbec2ffc842ba2df460a2" // empty start time; summary only
	SessionUnknown = "0000000000000000000000000000000000000000000000000000000000000000" // valid id, 404 not_found
)

// Routes are GET-only and relative to Prefix.
var Routes = []string{
	"GET /snapshot", "GET /status", "GET /search", "GET /stats", "GET /openapi.yaml",
	"GET /sessions/{id}", "GET /sessions/{id}/find", "GET /sessions/{id}/errors",
}

var (
	Modes          = []string{"text", "summary", "semantic", "hybrid"}
	SourceStates   = []string{"live", "cached", "stale", "unavailable"}
	HighlightForms = []string{"brackets", "ranges"}
	ToolStatuses   = []string{"ok", "error", "none"}
	NoticeCodes    = []string{"semantic_unavailable", "no_vectors", "model_mismatch", "dim_mismatch", "summary_vectors_only"}
	ErrorCodes     = []string{"configuration", "authentication", "not_found", "rate_limited", "remote", "unreachable", "invalid_response", "invalid_args", "read_only", "forbidden"}
	Filters        = []string{"since", "until", "tool", "model", "kind", "project", "has_error", "tag"}
	// Stage1 routes work on today's Fragments Engine; the rest need FE changes (F2-F4).
	Stage2 = []string{"/sessions/{id}/find", "/sessions/{id}/errors", "/stats", "turn_from_seq"}
)

const (
	MaxQuery        = 256
	MaxSearchLimit  = 50
	DefaultTurns    = 50
	MaxTurns        = 200
	MaxTurnOffset   = 1000000
	MaxResponseSize = 16 << 20
	SemanticMax     = 100
)

var sessionID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ValidSessionID reports a 64-hex session id.
func ValidSessionID(s string) bool { return sessionID.MatchString(s) }

// Guard: the real routes go through the assurance interface with requirement
// transcripts.read = browser-session (reserved operator mapping for search/find and tool
// target/excerpt fields). Agents (Authorization header) are refused. TR1 builds it.
const GuardRequirement = "transcripts.read"

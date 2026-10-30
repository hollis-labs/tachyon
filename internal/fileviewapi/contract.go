// Package fileviewapi is the contract for Tachyon's read-only file viewer
// backend: stat, content, sandboxed HTML frames, caster:// resolution, roots,
// settings and audit, plus a fixture-backed stub the viewer GUI builds against
// (TACHYON_FILEVIEW_STUBS=1). Real slices replace the stub route by route; the
// shapes in openapi.yaml are the final ones.
//
// Files are on the Tachyon HOST, never the browser machine. Nothing here writes
// a file.
package fileviewapi

import (
	"crypto/sha256"
	"encoding/hex"
)

// Prefix is where file viewer routes mount. It must stay inside the operator
// session cookie's path (approvals.CookiePath).
const Prefix = "/api/plugins/console/v1/files"

// FramesPrefix is where one-time frame documents are served. These GETs are
// the only unguarded routes: the 256-bit id is the capability, the iframe is
// opaque-origin and carries no cookie.
const FramesPrefix = Prefix + "/frames/"

// Kinds a stat can report (null for a directory or a denied path).
const (
	KindHTML     = "html"
	KindMarkdown = "markdown"
	KindText     = "text"
	KindJSON     = "json"
	KindYAML     = "yaml"
	KindTOML     = "toml"
	KindCSV      = "csv"
	KindCode     = "code"
	KindImage    = "image"
	KindBinary   = "binary"
)

// Deny reasons. A path outside every root is always reported as
// not_found_or_hidden so the API cannot be used to probe the disk.
const (
	DenyOutsideRoots   = "outside_roots" // never returned for a host path; kept for the audit log
	DenySecretPattern  = "secret_pattern"
	DenySymlinkEscape  = "symlink_escape"
	DenyTooLarge       = "too_large"
	DenyNotFoundHidden = "not_found_or_hidden"
)

// Entry types.
const (
	TypeFile  = "file"
	TypeDir   = "dir" // reported, never listed in v1
	TypeOther = "other"
)

// Audit kinds, sources and outcomes.
const (
	AuditStat   = "stat"
	AuditOpen   = "open"
	AuditFrame  = "frame"
	AuditCaster = "caster"
)

// Limits. Caps are the defaults; fileview.caps can change them.
const (
	MaxStatPaths    = 100
	MaxPathBytes    = 4096
	TextBytes       = 2 << 20  // inline text; the rest via Range
	ImageBytes      = 25 << 20 // inline image
	HTMLBytes       = 2 << 20  // rendered HTML; larger shows source with a banner
	FrameTTLSeconds = 5 * 60   // a frame is single use and lives 5 minutes
	MaxSettingsBody = 16 << 10
	MaxRoots        = 32
	DefaultAuditMax = 200
)

// FrameCSP is the Content-Security-Policy a frame document is served with.
// sandbox has no allow-same-origin: the document gets an opaque origin.
const FrameCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox; frame-ancestors 'self'"

// SVGCSP is served with image/svg+xml content; the UI must use <img> only.
const SVGCSP = "sandbox; default-src 'none'"

// Stub frame ids (sha256 of a label, so they look like real ids). The stub
// stores nothing: StubFrameID always serves, StubGoneFrameID answers 410.
var (
	StubFrameID     = frameID("fileview-stub-frame-1")
	StubGoneFrameID = frameID("fileview-stub-frame-gone")
)

func frameID(label string) string { s := sha256.Sum256([]byte(label)); return hex.EncodeToString(s[:]) }

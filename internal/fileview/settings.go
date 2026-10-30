// Package fileview is the real backend of the read-only file viewer (V1: settings and the roots registry).
// Shapes are those of internal/fileviewapi. Files are on the Tachyon host. Design:
// design_tachyon_file_viewer_backend_20261030, sections 3 and 5.
package fileview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// app_settings keys. Each is its own row holding a JSON value.
const (
	KeyRoots      = "fileview.roots"
	KeyDenyExtra  = "fileview.deny_extra"
	KeyCaps       = "fileview.caps"
	KeyCasterNS   = "fileview.caster_namespaces"
	KeyRunScripts = "fileview.run_scripts_allowed"
	KeyAllowUser  = "fileview.allow_user_roots"
	KeyRetention  = "fileview.audit_retention_days"
	KeyUserRoots  = "fileview.user_roots"
)

// SecretDirs are directory names (or relative paths) a root can never be, contain or sit above.
var SecretDirs = []string{".ssh", ".gnupg", ".aws", ".kube", ".config/gcloud"}

// BuiltinDeny are basename globs that are always denied. Settings can add to the deny list, never remove these;
// the path guard (V2) enforces them.
var BuiltinDeny = []string{".env*", "id_*", "*.pem", "*.key", "*.p12", "*.pfx", "credentials*", "*secret*", "*token*", ".netrc", ".npmrc", ".pgpass", "*.kdbx"}

type Caps struct {
	Text  int64 `json:"text_bytes"`
	Image int64 `json:"image_bytes"`
	HTML  int64 `json:"html_bytes"`
}

type App struct {
	Roots            []string `json:"roots"`
	DenyExtra        []string `json:"deny_extra"`
	Caps             Caps     `json:"caps"`
	CasterNamespaces []string `json:"caster_namespaces"`
	RunScripts       bool     `json:"run_scripts_allowed"`
	AllowUserRoots   bool     `json:"allow_user_roots"`
	AuditRetention   int      `json:"audit_retention_days"`
}

type User struct {
	Roots []string `json:"roots"`
}

type Settings struct {
	App  App  `json:"app"`
	User User `json:"user"`
}

// Update is a PUT body: only the fields present change.
type Update struct {
	App *struct {
		Roots            *[]string `json:"roots"`
		DenyExtra        *[]string `json:"deny_extra"`
		Caps             *Caps     `json:"caps"`
		CasterNamespaces *[]string `json:"caster_namespaces"`
		RunScripts       *bool     `json:"run_scripts_allowed"`
		AllowUserRoots   *bool     `json:"allow_user_roots"`
		AuditRetention   *int      `json:"audit_retention_days"`
	} `json:"app"`
	User *struct {
		Roots *[]string `json:"roots"`
	} `json:"user"`
}

// Error is a validation failure with the API code and the HTTP status it maps to.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func bad(code, format string, a ...any) *Error {
	return &Error{Status: 400, Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Service reads and writes the viewer settings. Home is the service user's home directory (never the browser's).
type Service struct {
	DB   *localstore.Store
	Home string
}

func defaults() Settings {
	return Settings{App: App{
		Roots: []string{}, DenyExtra: []string{},
		Caps:             Caps{Text: fileviewapi.TextBytes, Image: fileviewapi.ImageBytes, HTML: fileviewapi.HTMLBytes},
		CasterNamespaces: []string{"designer", "scratch"}, AuditRetention: 30,
	}, User: User{Roots: []string{}}}
}

// Load returns the stored settings over the defaults. A stored value that no longer validates is ignored for
// that key (it falls back to the default), so a bad row can never widen access.
func (s *Service) Load(ctx context.Context) (Settings, error) {
	out := defaults()
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT key,value FROM app_settings WHERE key LIKE 'fileview.%'`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return out, err
		}
		switch k {
		case KeyRoots:
			setJSON(v, &out.App.Roots, func(r []string) bool { return s.checkRoots(r) == nil })
		case KeyDenyExtra:
			setJSON(v, &out.App.DenyExtra, func(r []string) bool { return checkDeny(r) == nil })
		case KeyCaps:
			setJSON(v, &out.App.Caps, func(c Caps) bool { return checkCaps(c) == nil })
		case KeyCasterNS:
			setJSON(v, &out.App.CasterNamespaces, func(r []string) bool { return checkNamespaces(r) == nil })
		case KeyRunScripts:
			setJSON(v, &out.App.RunScripts, nil)
		case KeyAllowUser:
			setJSON(v, &out.App.AllowUserRoots, nil)
		case KeyRetention:
			setJSON(v, &out.App.AuditRetention, func(n int) bool { return n >= 1 && n <= 365 })
		case KeyUserRoots:
			setJSON(v, &out.User.Roots, func(r []string) bool { return s.checkRoots(r) == nil })
		}
	}
	if out.App.Roots == nil {
		out.App.Roots = []string{}
	}
	if out.App.DenyExtra == nil {
		out.App.DenyExtra = []string{}
	}
	if out.User.Roots == nil {
		out.User.Roots = []string{}
	}
	return out, rows.Err()
}

func setJSON[T any](raw string, dst *T, ok func(T) bool) {
	var v T
	if json.Unmarshal([]byte(raw), &v) == nil && (ok == nil || ok(v)) {
		*dst = v
	}
}

// Apply validates u against the stored settings, writes the changed keys in one transaction and returns the
// merged result. A request cannot unlock personal roots for itself: user roots need allow_user_roots to be
// true in the STORED settings.
func (s *Service) Apply(ctx context.Context, u Update) (Settings, error) {
	if u.App == nil && u.User == nil {
		return Settings{}, bad("invalid", "Send app and/or user settings.")
	}
	cur, err := s.Load(ctx)
	if err != nil {
		return Settings{}, err
	}
	next := cur
	writes := map[string]any{}
	if a := u.App; a != nil {
		if a.Roots != nil {
			if err := s.checkRoots(*a.Roots); err != nil {
				return Settings{}, err
			}
			next.App.Roots = cleanList(*a.Roots)
			writes[KeyRoots] = next.App.Roots
		}
		if a.DenyExtra != nil {
			if err := checkDeny(*a.DenyExtra); err != nil {
				return Settings{}, err
			}
			next.App.DenyExtra = slices.Clone(*a.DenyExtra)
			writes[KeyDenyExtra] = next.App.DenyExtra
		}
		if a.Caps != nil {
			if err := checkCaps(*a.Caps); err != nil {
				return Settings{}, err
			}
			next.App.Caps = *a.Caps
			writes[KeyCaps] = next.App.Caps
		}
		if a.CasterNamespaces != nil {
			if err := checkNamespaces(*a.CasterNamespaces); err != nil {
				return Settings{}, err
			}
			next.App.CasterNamespaces = slices.Clone(*a.CasterNamespaces)
			writes[KeyCasterNS] = next.App.CasterNamespaces
		}
		if a.RunScripts != nil {
			next.App.RunScripts = *a.RunScripts
			writes[KeyRunScripts] = *a.RunScripts
		}
		if a.AllowUserRoots != nil {
			next.App.AllowUserRoots = *a.AllowUserRoots
			writes[KeyAllowUser] = *a.AllowUserRoots
		}
		if a.AuditRetention != nil {
			if *a.AuditRetention < 1 || *a.AuditRetention > 365 {
				return Settings{}, bad("invalid", "audit_retention_days is 1 to 365.")
			}
			next.App.AuditRetention = *a.AuditRetention
			writes[KeyRetention] = *a.AuditRetention
		}
	}
	if us := u.User; us != nil && us.Roots != nil {
		if !cur.App.AllowUserRoots && len(*us.Roots) > 0 {
			return Settings{}, &Error{Status: 403, Code: "user_roots_not_allowed", Msg: "The app settings do not allow personal roots."}
		}
		if err := s.checkRoots(*us.Roots); err != nil {
			return Settings{}, err
		}
		next.User.Roots = cleanList(*us.Roots)
		writes[KeyUserRoots] = next.User.Roots
	}
	if len(next.App.Roots)+len(next.User.Roots) > fileviewapi.MaxRoots {
		return Settings{}, bad("invalid", "roots: at most %d in total.", fileviewapi.MaxRoots)
	}
	if len(writes) == 0 {
		return next, nil
	}
	err = s.DB.Writer.Submit(ctx, "fileview_settings", func(ctx context.Context, tx *sql.Tx) error {
		for k, v := range writes {
			b, _ := json.Marshal(v)
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, string(b)); err != nil {
				return err
			}
		}
		return nil
	})
	return next, err
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, r := range in {
		c := path.Clean(r)
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// checkRoots enforces what a root may be: an existing directory, absolute, not /, not the home directory or
// any ancestor of it, not inside a secret directory (also after symlinks) and not a parent of one.
func (s *Service) checkRoots(roots []string) error {
	if len(roots) > fileviewapi.MaxRoots {
		return bad("invalid", "roots: at most %d.", fileviewapi.MaxRoots)
	}
	home := filepath.Clean(s.Home)
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	for _, r := range roots {
		if r == "" || len(r) > fileviewapi.MaxPathBytes || strings.ContainsRune(r, 0) {
			return bad("invalid", "roots: each is 1 to %d characters.", fileviewapi.MaxPathBytes)
		}
		if !filepath.IsAbs(r) {
			return bad("invalid_root", "A root must be an absolute path.")
		}
		clean := filepath.Clean(r)
		if hasSecretSegment(clean) {
			return bad("invalid_root", "A root cannot be or contain a secret directory.")
		}
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return bad("invalid_root", "A root must be an existing directory.")
		}
		if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
			return bad("invalid_root", "A root must be an existing directory.")
		}
		if hasSecretSegment(resolved) {
			return bad("invalid_root", "A root cannot be or contain a secret directory.")
		}
		if isAncestorOrSame(resolved, home) {
			return bad("invalid_root", "A root cannot be /, the home directory or a parent of it.")
		}
		for _, sd := range SecretDirs {
			if isAncestorOrSame(resolved, filepath.Join(home, filepath.FromSlash(sd))) {
				return bad("invalid_root", "A root cannot be the parent of a secret directory.")
			}
		}
	}
	return nil
}

func hasSecretSegment(p string) bool {
	slashed := "/" + strings.Trim(filepath.ToSlash(p), "/") + "/"
	for _, sd := range SecretDirs {
		if strings.Contains(slashed, "/"+sd+"/") {
			return true
		}
	}
	return false
}

// isAncestorOrSame reports whether a is p or a directory above p, separator-aware.
func isAncestorOrSame(a, p string) bool {
	rel, err := filepath.Rel(a, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkDeny(patterns []string) error {
	if len(patterns) > 64 {
		return bad("invalid", "deny_extra: at most 64 patterns.")
	}
	for _, p := range patterns {
		if p == "" || len(p) > 128 || strings.ContainsAny(p, "\x00/") {
			return bad("invalid", "deny_extra: each pattern is a file name glob of at most 128 characters.")
		}
		if _, err := path.Match(p, "x"); err != nil {
			return bad("invalid", "deny_extra: %q is not a valid glob.", p)
		}
	}
	return nil
}

func checkCaps(c Caps) error {
	if c.Text < 1<<10 || c.Text > 64<<20 || c.Image < 1<<10 || c.Image > 256<<20 || c.HTML < 1<<10 || c.HTML > 16<<20 {
		return bad("invalid", "caps are out of range.")
	}
	return nil
}

var errNS = errors.New("caster_namespaces must be lower-case slugs")

func checkNamespaces(ns []string) error {
	if len(ns) > 32 {
		return bad("invalid", "caster_namespaces: at most 32.")
	}
	for _, s := range ns {
		if s == "" || len(s) > 64 {
			return bad("invalid", "%s.", errNS)
		}
		for i, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_' || c == '.')) {
				return bad("invalid", "%s.", errNS)
			}
		}
	}
	return nil
}

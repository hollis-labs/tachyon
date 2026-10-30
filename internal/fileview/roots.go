package fileview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Root is one entry of GET /roots. The API never exposes a resolved absolute path of another user's tree:
// path_display is ~-relative when under the service user's home.
type Root struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	PathDisplay string `json:"path_display"`
	Source      string `json:"source"` // default | app | user
	Writable    bool   `json:"writable"`
	OwnerOnly   bool   `json:"owner_only,omitempty"`
}

type RootsView struct {
	Roots []Root `json:"roots"`
	Caps  Caps   `json:"caps"`
}

// EffectiveRoot is a root as the path guard (V2) uses it: the symlink-resolved absolute path and whether only
// files owned by the service user may be served from it (world-writable roots such as /tmp).
type EffectiveRoot struct {
	Root
	Path string
}

type rootSpec struct{ label, path, source string }

// DefaultRoots are the built-in roots for home: Downloads, dev, /tmp and the agent-os workspaces. Missing
// directories are skipped. Design F-V1 asks Chrispian to confirm this list.
func DefaultRoots(home string) []rootSpec {
	return []rootSpec{
		{"Downloads", filepath.Join(home, "Downloads"), "default"},
		{"dev", filepath.Join(home, "dev"), "default"},
		{"tmp", "/tmp", "default"},
		{"Workspaces", filepath.Join(home, "dev", "agent-os", "workspaces"), "default"},
	}
}

// Effective resolves the default, app and user roots (user roots only while allow_user_roots is true). A root
// that no longer exists, or now resolves into a secret directory or above home, is dropped, never served.
func (s *Service) Effective(ctx context.Context) ([]EffectiveRoot, Caps, error) {
	set, err := s.Load(ctx)
	if err != nil {
		return nil, Caps{}, err
	}
	specs := DefaultRoots(s.Home)
	for _, p := range set.App.Roots {
		specs = append(specs, rootSpec{filepath.Base(p), p, "app"})
	}
	if set.App.AllowUserRoots {
		for _, p := range set.User.Roots {
			specs = append(specs, rootSpec{filepath.Base(p), p, "user"})
		}
	}
	var out []EffectiveRoot
	seen, ids := map[string]bool{}, map[string]int{}
	for _, sp := range specs {
		resolved, err := filepath.EvalSymlinks(sp.path)
		if err != nil || seen[resolved] {
			continue
		}
		fi, err := os.Stat(resolved)
		if err != nil || !fi.IsDir() || hasSecretSegment(resolved) {
			continue
		}
		if sp.source != "default" && s.checkRoots([]string{sp.path}) != nil {
			continue
		}
		seen[resolved] = true
		id := slug(sp.label)
		if ids[id]++; ids[id] > 1 {
			id = id + "-" + string(rune('0'+ids[id]))
		}
		out = append(out, EffectiveRoot{
			Root: Root{ID: id, Label: sp.label, PathDisplay: s.display(sp.path), Source: sp.source, OwnerOnly: fi.Mode().Perm()&0o002 != 0},
			Path: resolved,
		})
	}
	return out, set.App.Caps, nil
}

func (s *Service) display(p string) string {
	home := filepath.Clean(s.Home)
	if rel, err := filepath.Rel(home, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if rel == "." {
			return "~"
		}
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "root"
}

// View is GET /roots.
func (s *Service) View(ctx context.Context) (RootsView, error) {
	eff, caps, err := s.Effective(ctx)
	if err != nil {
		return RootsView{}, err
	}
	v := RootsView{Roots: make([]Root, 0, len(eff)), Caps: caps}
	for _, e := range eff {
		v.Roots = append(v.Roots, e.Root)
	}
	return v, nil
}

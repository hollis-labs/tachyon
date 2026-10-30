package fileview

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

// Denial is why the guard refused a path. Reason is one of the fileviewapi.Deny* codes, or DenyInvalid for a
// malformed path. A path outside every root is DenyNotFoundHidden so a caller cannot probe the host.
type Denial struct{ Reason string }

func (d *Denial) Error() string { return d.Reason }

// DenyInvalid is a malformed request path (NUL, relative, over-long); nothing was looked up.
const DenyInvalid = "invalid_path"

// DenyNotFile is a path that resolves inside a root but is not a regular file (directory, device, socket).
const DenyNotFile = "not_a_file"

// Resolved is a request the guard accepted: the symlink-resolved path, the root it sits in and its
// path relative to that root. Info is the lstat of the resolved path at check time.
type Resolved struct {
	Path, Rel string
	Root      EffectiveRoot
	Info      os.FileInfo
}

// Guard decides whether a host path may be read (design section 3). Every later step (stat, content, frame)
// goes through Resolve or Open; nothing else reads the host disk.
type Guard struct {
	Svc *Service
	// UID is the service user's uid, compared with the owner of files under owner-only roots such as /tmp.
	// Zero means os.Getuid().
	UID int
}

func (g *Guard) uid() int {
	if g.UID != 0 {
		return g.UID
	}
	return os.Getuid()
}

// expand turns a request into a cleaned absolute path: "~" and "~/x" use the service user's home, anything
// else must already be absolute.
func (g *Guard) expand(p string) (string, bool) {
	if p == "" || len(p) > fileviewapi.MaxPathBytes || strings.ContainsRune(p, 0) {
		return "", false
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(g.Svc.Home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		return "", false
	}
	return filepath.Clean(p), true
}

// Resolve runs the checks in order: shape, EvalSymlinks, roots (on the resolved path), secret patterns on
// the resolved and the requested path, and the owner check under owner-only roots.
func (g *Guard) Resolve(ctx context.Context, requested string) (Resolved, *Denial) {
	clean, ok := g.expand(requested)
	if !ok {
		return Resolved{}, &Denial{DenyInvalid}
	}
	roots, _, err := g.Svc.Effective(ctx)
	if err != nil {
		return Resolved{}, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	set, err := g.Svc.Load(ctx)
	if err != nil {
		return Resolved{}, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return Resolved{}, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	var root *EffectiveRoot
	for i := range roots {
		if isAncestorOrSame(roots[i].Path, resolved) {
			root = &roots[i]
			break
		}
	}
	if root == nil {
		// A link that starts inside a root and ends outside is told apart from a path that was never inside.
		for i := range roots {
			if isAncestorOrSame(roots[i].Path, clean) {
				return Resolved{}, &Denial{fileviewapi.DenySymlinkEscape}
			}
		}
		return Resolved{}, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	deny := append(append([]string{}, BuiltinDeny...), set.App.DenyExtra...)
	if secretPath(resolved, deny) || secretPath(clean, deny) {
		return Resolved{}, &Denial{fileviewapi.DenySecretPattern}
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return Resolved{}, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	if root.OwnerOnly && !ownedBy(info, g.uid()) {
		return Resolved{}, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	rel, _ := filepath.Rel(root.Path, resolved)
	return Resolved{Path: resolved, Rel: filepath.ToSlash(rel), Root: *root, Info: info}, nil
}

// Open resolves and opens a regular file read-only. Callers close the file.
func (g *Guard) Open(ctx context.Context, requested string) (*os.File, Resolved, *Denial) {
	res, den := g.Resolve(ctx, requested)
	if den != nil {
		return nil, Resolved{}, den
	}
	f, den := g.OpenResolved(res)
	return f, res, den
}

// OpenResolved opens an accepted path. The descriptor is checked after the open: it must be a regular file and
// the same file the lstat in Resolve saw, so a swap between check and open is refused.
func (g *Guard) OpenResolved(res Resolved) (*os.File, *Denial) {
	if !res.Info.Mode().IsRegular() {
		return nil, &Denial{DenyNotFile}
	}
	f, err := os.OpenFile(res.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(res.Info, fi) || (res.Root.OwnerOnly && !ownedBy(fi, g.uid())) {
		f.Close()
		return nil, &Denial{fileviewapi.DenyNotFoundHidden}
	}
	return f, nil
}

func ownedBy(fi os.FileInfo, uid int) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// secretPath reports whether p names a secret: a basename matching a deny glob (case-insensitive; a public key
// "id_*.pub" is allowed), any segment that is a secret directory, or a file named config inside .git.
func secretPath(p string, deny []string) bool {
	if hasSecretSegment(p) {
		return true
	}
	slash := filepath.ToSlash(p)
	base := strings.ToLower(path.Base(slash))
	if strings.HasSuffix(base, ".pub") && strings.HasPrefix(base, "id_") {
		return false
	}
	for _, g := range deny {
		if ok, _ := path.Match(strings.ToLower(g), base); ok {
			return true
		}
	}
	return base == "config" && strings.Contains("/"+slash, "/.git/")
}

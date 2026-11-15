package migratefrom

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/tachyon/next/internal/chat/chatworkspace"
)

// The old build's file-backed stores (slice D2). Its data directory held one store of durable user
// data outside the databases: the chat workspace files, <data dir>/workspace/<hash>/<item id>, each
// one named by a chat_workspace_items row (type file or image) that records its SHA-256. The source directory of
// migrate-from may hold a copy of it (cp -a <old data dir>/workspace <source>/workspace); it is
// copied into tachyon.workspace_dir. docs/conversion-notes.md lists every old path and why the rest
// is not copied.

// WorkspaceDir is the old chat workspace directory's name, in the old data directory and in the
// migrate-from source.
const WorkspaceDir = "workspace"

// uploadTemp is the prefix of an upload the old build was still receiving; never a stored item.
const uploadTemp = ".upload-"

// maxListed bounds the files named in an error.
const maxListed = 20

// FilesReport is the file-store step of a run.
type FilesReport struct {
	Store string `json:"store"`
	// Source is the store's directory in the migrate-from source; empty when the source has none.
	Source string `json:"source"`
	Target string `json:"target"`
	// ReferencedRows are the chat_workspace_items rows with bytes (file, image), each naming one file.
	ReferencedRows int `json:"referenced_rows"`
	// Files are the source's files, each with its digest and what happened to it.
	Files        []StoredFile `json:"files"`
	Copied       int          `json:"copied"`
	AlreadyEqual int          `json:"already_equal"`
	Unreferenced int          `json:"unreferenced"`
	Skipped      []string     `json:"skipped_partial_uploads,omitempty"`
	Bytes        int64        `json:"bytes"`
	// Equal is every target file's digest equal to its source file's.
	Equal bool `json:"equal"`
}

// StoredFile is one file of the store.
type StoredFile struct {
	Path       string `json:"path"` // relative to the store
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Referenced bool   `json:"referenced"`
	Action     string `json:"action"` // copied or equal
}

// itemRow is one chat_workspace_items file row.
type itemRow struct {
	rel, sha string
	size     int64
}

// CopyWorkspace copies the chat workspace files from sourceDir (the migrate-from source's
// workspace/) into targetDir (tachyon.workspace_dir), proving each copy by SHA-256. q reads the
// migrated chat_workspace_items rows. It checks everything before it writes anything: every file row
// (file or image) must have its file in the source, with the size and digest the row recorded; the source holds only
// regular files and directories (an old partial upload is skipped and named); a file already in the
// target must be byte-identical. Then each file is written with mode 0600 in directories with mode
// 0700, through a temporary file and a rename, and read back. A second run over a complete target
// copies nothing and reports every file equal.
func CopyWorkspace(ctx context.Context, q rowQuerier, sourceDir, targetDir string) (FilesReport, error) {
	return workspace(ctx, q, sourceDir, targetDir, true)
}

// CheckWorkspace is CopyWorkspace's checks without any write: migrate-from runs it on the source
// database before it copies a row.
func CheckWorkspace(ctx context.Context, q rowQuerier, sourceDir, targetDir string) (FilesReport, error) {
	return workspace(ctx, q, sourceDir, targetDir, false)
}

// rowQuerier reads rows: the libs store's reader, or a source database opened read-only.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func workspace(ctx context.Context, q rowQuerier, sourceDir, targetDir string, write bool) (FilesReport, error) {
	r := FilesReport{Store: WorkspaceDir, Target: targetDir, Files: []StoredFile{}}
	if targetDir == "" {
		return r, errors.New("the workspace target directory (tachyon.workspace_dir) is not set")
	}
	rows, err := workspaceRows(ctx, q)
	if err != nil {
		return r, err
	}
	r.ReferencedRows = len(rows)
	present := map[string]StoredFile{}
	switch fi, statErr := os.Lstat(sourceDir); {
	case errors.Is(statErr, fs.ErrNotExist):
		// No workspace in the source: allowed only when no row names a file.
	case statErr != nil:
		return r, statErr
	case !fi.IsDir():
		return r, fmt.Errorf("source %s is not a directory", sourceDir)
	default:
		r.Source = sourceDir
		if present, r.Skipped, err = scanStore(sourceDir); err != nil {
			return r, err
		}
	}
	// Every row's file is in the source, as the row recorded it.
	var missing, wrong []string
	referenced := map[string]bool{}
	for _, row := range rows {
		referenced[row.rel] = true
		f, ok := present[row.rel]
		switch {
		case !ok:
			missing = append(missing, row.rel)
		case row.sha != "" && !strings.EqualFold(row.sha, f.SHA256):
			wrong = append(wrong, row.rel+" (sha256 differs from its row)")
		case row.size >= 0 && row.size != f.Size:
			wrong = append(wrong, fmt.Sprintf("%s (%d bytes, its row says %d)", row.rel, f.Size, row.size))
		}
	}
	if len(missing) > 0 {
		return r, fmt.Errorf("%d chat workspace file(s) named by a chat_workspace_items row are missing from the source %s (copy <old data dir>/workspace there with cp -a); nothing was copied: %s",
			len(missing), sourceOrNone(sourceDir, r.Source), listed(missing))
	}
	if len(wrong) > 0 {
		return r, fmt.Errorf("%d chat workspace file(s) do not match their rows; nothing was copied: %s", len(wrong), listed(wrong))
	}
	// A file already in the target is the same file, or the run stops before writing.
	names := make([]string, 0, len(present))
	for rel := range present {
		names = append(names, rel)
	}
	sort.Strings(names)
	var differs []string
	for _, rel := range names {
		sum, size, statErr := fileDigest(filepath.Join(targetDir, rel))
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return r, statErr
		}
		if sum != present[rel].SHA256 || size != present[rel].Size {
			differs = append(differs, rel)
		}
	}
	if len(differs) > 0 {
		return r, fmt.Errorf("%d file(s) already in the workspace target %s differ from the source; nothing was copied: %s", len(differs), targetDir, listed(differs))
	}
	if !write {
		return r, nil
	}
	if err = privateDir(targetDir); err != nil {
		return r, err
	}
	r.Equal = true
	for _, rel := range names {
		f := present[rel]
		f.Referenced = referenced[rel]
		if !f.Referenced {
			r.Unreferenced++
		}
		dst := filepath.Join(targetDir, rel)
		if _, statErr := os.Lstat(dst); statErr == nil {
			f.Action = "equal"
			r.AlreadyEqual++
		} else {
			if err = copyFile(filepath.Join(sourceDir, rel), dst); err != nil {
				return r, fmt.Errorf("copy %s: %w", rel, err)
			}
			f.Action = "copied"
			r.Copied++
		}
		sum, size, digestErr := fileDigest(dst)
		if digestErr != nil {
			return r, digestErr
		}
		if sum != f.SHA256 || size != f.Size {
			r.Equal = false
			return r, fmt.Errorf("copy %s: the target's sha256 differs from the source's", rel)
		}
		r.Bytes += f.Size
		r.Files = append(r.Files, f)
	}
	return r, ctx.Err()
}

// workspaceRows are the chat_workspace_items rows that have bytes on disk (file and image), with each file's path in the store
// (chatworkspace.Store.Path: the same derivation the old build used).
func workspaceRows(ctx context.Context, q rowQuerier) ([]itemRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT thread_ref, id, COALESCE(sha256,''), COALESCE(size_bytes,-1) FROM chat_workspace_items WHERE type IN ('file','image') ORDER BY thread_ref, id`)
	if err != nil {
		return nil, fmt.Errorf("read chat_workspace_items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []itemRow
	layout := chatworkspace.Store{}
	for rows.Next() {
		var thread, id string
		var row itemRow
		if err := rows.Scan(&thread, &id, &row.sha, &row.size); err != nil {
			return nil, err
		}
		row.rel = filepath.ToSlash(layout.Path(thread, id))
		out = append(out, row)
	}
	return out, rows.Err()
}

// scanStore digests every regular file under dir. It refuses anything else (a symbolic link, a
// device) and skips an old partial upload.
func scanStore(dir string) (map[string]StoredFile, []string, error) {
	out := map[string]StoredFile{}
	var skipped, refused []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return nil
		case strings.HasPrefix(d.Name(), uploadTemp):
			skipped = append(skipped, rel)
			return nil
		case !d.Type().IsRegular():
			refused = append(refused, rel)
			return nil
		}
		sum, size, err := fileDigest(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = StoredFile{Path: filepath.ToSlash(rel), Size: size, SHA256: sum}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(refused) > 0 {
		return nil, nil, fmt.Errorf("the workspace source holds %d entry(ies) that are not regular files (a link or a device); nothing was copied: %s", len(refused), listed(refused))
	}
	sort.Strings(skipped)
	return out, skipped, nil
}

// copyFile writes src to dst (mode 0600, directory 0700) through a temporary file in dst's directory,
// synced before the rename, so a crash never leaves a partial file under the final name.
func copyFile(src, dst string) (err error) {
	if err = privateDir(filepath.Dir(dst)); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // a file of the workspace copy the operator named as the source
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".migrate-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// privateDir creates dir (and its parents) and sets it to mode 0700.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // a directory: owner rwx, nothing for others
}

// fileDigest is a file's SHA-256 (hex) and size.
func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // a workspace file in the source or the target
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func listed(names []string) string {
	if len(names) > maxListed {
		return strings.Join(names[:maxListed], ", ") + fmt.Sprintf(" and %d more", len(names)-maxListed)
	}
	return strings.Join(names, ", ")
}

func sourceOrNone(want, found string) string {
	if found == "" {
		return want + " (not present)"
	}
	return found
}

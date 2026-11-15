package migratefrom

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/chat/chatworkspace"
)

// wsFile is one chat workspace file in a fixture: its row and its bytes.
type wsFile struct {
	thread, id, typ string
	body            string
	row             bool // a chat_workspace_items row names it
	onDisk          bool // its bytes are in the source's workspace/
}

func (f wsFile) rel() string { return filepath.ToSlash((&chatworkspace.Store{}).Path(f.thread, f.id)) }

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// workspaceFixture is the fixture source plus chat workspace rows and files.
func workspaceFixture(t *testing.T, files []wsFile) string {
	t.Helper()
	dir := fixture(t, func(src, _ *sql.DB) {
		for _, f := range files {
			if f.row {
				execAll(t, src, fmt.Sprintf(`INSERT INTO chat_workspace_items(id, thread_ref, type, name, mime, size_bytes, sha256, added_by, created_ns, updated_ns)
				 VALUES ('%s', '%s', '%s', 'n', 'text/plain', %d, '%s', 'user', 1, 1)`, f.id, f.thread, f.typ, len(f.body), digest(f.body)))
			}
		}
	})
	for _, f := range files {
		if f.onDisk {
			p := filepath.Join(dir, WorkspaceDir, f.rel())
			// Looser modes than the target gets, so the test sees them tightened.
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // see above
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil { //nolint:gosec // see above
				t.Fatal(err)
			}
		}
	}
	return dir
}

var someFiles = []wsFile{
	{thread: "app:t1", id: "w1", typ: "file", body: "first file", row: true, onDisk: true},
	{thread: "app:t1", id: "w2", typ: "image", body: "\x89PNG image bytes", row: true, onDisk: true},
	{thread: "tether:s1", id: "w3", typ: "file", body: "another thread", row: true, onDisk: true},
	// A file no row names (its row was deleted while the bytes stayed): copied, and reported.
	{thread: "tether:s1", id: "orphan", body: "kept anyway", onDisk: true},
}

// The chat workspace files are copied into the workspace target, each proved by SHA-256 against its
// source and its row, with private modes; a partial upload is skipped; the source is untouched.
func TestWorkspaceFilesAreCopiedAndProved(t *testing.T) {
	dir := workspaceFixture(t, someFiles)
	partial := filepath.Join(dir, WorkspaceDir, filepath.Dir(someFiles[0].rel()), ".upload-123")
	if err := os.WriteFile(partial, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := options(dir)
	o.WorkspaceTarget = filepath.Join(t.TempDir(), "data", "workspace")
	r, err := Run(context.Background(), target(t), o)
	if err != nil {
		t.Fatal(err)
	}
	f := r.Files
	if f == nil || !f.Equal || f.Copied != 4 || f.AlreadyEqual != 0 || f.ReferencedRows != 3 || f.Unreferenced != 1 || len(f.Skipped) != 1 {
		t.Fatalf("files report: %+v", f)
	}
	var bytes int64
	for _, sf := range someFiles {
		bytes += int64(len(sf.body))
		got, err := os.ReadFile(filepath.Join(o.WorkspaceTarget, sf.rel()))
		if err != nil || string(got) != sf.body {
			t.Errorf("%s: %q %v", sf.rel(), got, err)
		}
		fi, _ := os.Stat(filepath.Join(o.WorkspaceTarget, sf.rel()))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o", sf.rel(), fi.Mode().Perm())
		}
		di, _ := os.Stat(filepath.Dir(filepath.Join(o.WorkspaceTarget, sf.rel())))
		if di.Mode().Perm() != 0o700 {
			t.Errorf("%s directory mode %o", sf.rel(), di.Mode().Perm())
		}
	}
	if f.Bytes != bytes {
		t.Errorf("bytes %d, want %d", f.Bytes, bytes)
	}
	for _, sf := range f.Files {
		if sf.SHA256 == "" || sf.Action != "copied" {
			t.Errorf("file entry %+v", sf)
		}
	}
	if _, err := os.Stat(filepath.Join(o.WorkspaceTarget, filepath.Dir(someFiles[0].rel()), ".upload-123")); err == nil {
		t.Error("a partial upload was copied")
	}
	// The source still holds exactly what it held.
	if b, _ := os.ReadFile(filepath.Join(dir, WorkspaceDir, someFiles[0].rel())); string(b) != someFiles[0].body { //nolint:gosec // this test's fixture
		t.Error("the source changed")
	}
}

// A rerun of the file step over a complete target copies nothing and reports every file equal; it
// runs only on a target an earlier migration's successful report vouches for, and keeps that report.
func TestFilesOnlyRerunIsANoOp(t *testing.T) {
	dir := workspaceFixture(t, someFiles)
	st := target(t)
	data := t.TempDir()
	o := options(dir)
	o.WorkspaceTarget = filepath.Join(data, "workspace")
	o.Report = filepath.Join(data, ReportFile)
	if _, err := Run(context.Background(), st, o); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(o.Report)
	again := o
	again.FilesOnly, again.Migrated, again.Report = true, o.Report, filepath.Join(data, FilesReportFile)
	r, err := Run(context.Background(), st, again)
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Files; f == nil || !f.Equal || f.Copied != 0 || f.AlreadyEqual != 4 || !r.FilesOnly {
		t.Fatalf("rerun: %+v", r.Files)
	}
	if after, _ := os.ReadFile(o.Report); string(after) != string(before) {
		t.Error("the migration's report was rewritten")
	}
	// A file in the target that differs from the source stops the step before it writes.
	changed := filepath.Join(o.WorkspaceTarget, someFiles[0].rel())
	if err := os.WriteFile(changed, []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), st, again); err == nil || !strings.Contains(err.Error(), "differ from the source") {
		t.Fatalf("a differing target file: %v", err)
	}
	if b, _ := os.ReadFile(changed); string(b) != "edited" { //nolint:gosec // this test's target
		t.Error("a differing target file was overwritten")
	}
	// Without a successful migration report, the file step alone is refused.
	none := again
	none.Migrated = filepath.Join(t.TempDir(), ReportFile)
	if _, err := Run(context.Background(), st, none); err == nil || !strings.Contains(err.Error(), "only on a target migrate-from has migrated") {
		t.Fatalf("files-only without a migration: %v", err)
	}
}

// Every row's file must be in the source, as its row recorded it, before a single row is copied.
func TestWorkspaceRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		files []wsFile
		edit  func(t *testing.T, dir string)
		want  string
	}{
		"missing file": {
			files: []wsFile{someFiles[0], {thread: "app:t1", id: "gone", typ: "file", body: "x", row: true}},
			want:  "missing from the source",
		},
		"no workspace at all": {
			files: []wsFile{{thread: "app:t1", id: "w1", typ: "file", body: "x", row: true}},
			want:  "(not present)",
		},
		"digest differs": {
			files: []wsFile{someFiles[0]},
			edit: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, WorkspaceDir, someFiles[0].rel()), []byte("first FILE"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "sha256 differs",
		},
		"a link": {
			files: []wsFile{someFiles[0]},
			edit: func(t *testing.T, dir string) {
				if err := os.Symlink("/etc/hostname", filepath.Join(dir, WorkspaceDir, "link")); err != nil {
					t.Fatal(err)
				}
			},
			want: "not regular files",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := workspaceFixture(t, c.files)
			if c.edit != nil {
				c.edit(t, dir)
			}
			st := target(t)
			o := options(dir)
			o.WorkspaceTarget = filepath.Join(t.TempDir(), "workspace")
			_, err := Run(context.Background(), st, o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			// Refused before the copy: no row reached the target, no file was written.
			var n int
			if e := st.Reader().QueryRowContext(context.Background(), `SELECT count(*) FROM app_settings`).Scan(&n); e != nil || n != 0 {
				t.Errorf("target rows after a refusal: %d %v", n, e)
			}
			if _, e := os.Stat(o.WorkspaceTarget); e == nil {
				t.Error("the workspace target was created")
			}
		})
	}
}

// No workspace in the source and no row naming a file: the step reports nothing to copy.
func TestNoWorkspaceFilesIsFine(t *testing.T) {
	o := options(fixture(t, nil))
	o.WorkspaceTarget = filepath.Join(t.TempDir(), "workspace")
	r, err := Run(context.Background(), target(t), o)
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Files; f == nil || len(f.Files) != 0 || f.ReferencedRows != 0 || f.Source != "" || !f.Equal {
		t.Fatalf("files report: %+v", r.Files)
	}
}

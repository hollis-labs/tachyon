package appdirs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHonoursTheOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "data")
	d, err := Resolve(func(k string) string {
		if k == DataDirEnv {
			return want
		}
		return ""
	})
	if err != nil || d.Data != want {
		t.Fatalf("Resolve = %q, %v; want %q", d.Data, err, want)
	}
	if got := d.Anchor("tachyon.db"); got != filepath.Join(want, "tachyon.db") {
		t.Fatalf("relative path anchored to %s", got)
	}
	if got := d.Anchor("/abs/elsewhere/x.db"); got != "/abs/elsewhere/x.db" {
		t.Fatalf("absolute path changed to %s", got)
	}
}

func TestResolveDefaultsToTheXDGDataHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	d, err := Resolve(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(xdg, d.Data); err != nil || rel == "." || rel[0] == '.' {
		t.Fatalf("data dir %q is not under XDG_DATA_HOME %q", d.Data, xdg)
	}
	if _, err := os.Stat(d.Data); err == nil {
		t.Fatal("Resolve must not create anything")
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestPrepareDirAndEnsurePrivateCreatePrivateFiles(t *testing.T) {
	d := Dirs{Data: filepath.Join(t.TempDir(), "a", "data")}
	if err := d.PrepareDir(); err != nil {
		t.Fatal(err)
	}
	db := d.Anchor("tachyon.db")
	if err := EnsurePrivate(db); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, d.Data); m != 0o700 {
		t.Errorf("directory mode %o, want 700", m)
	}
	if m := mode(t, db); m != 0o600 {
		t.Errorf("database mode %o, want 600", m)
	}
}

func TestPrepareTightensExistingLooseModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil { //nolint:gosec // the test makes it loose on purpose
		t.Fatal(err)
	}
	d := Dirs{Data: dir}
	db := d.Anchor("tachyon.db")
	if err := os.WriteFile(db, []byte("keep"), 0o644); err != nil { //nolint:gosec // the test makes it loose on purpose
		t.Fatal(err)
	}
	if err := d.PrepareDir(); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivate(db); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("directory mode %o, want 700", m)
	}
	if m := mode(t, db); m != 0o600 {
		t.Errorf("database mode %o, want 600", m)
	}
	if b, _ := os.ReadFile(db); string(b) != "keep" { //nolint:gosec // test path
		t.Errorf("content changed: %q", b)
	}
}

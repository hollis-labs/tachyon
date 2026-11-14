package appdirs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points every default location at a temp directory, so a test that forgets its own
// override cannot touch the live data directory of a Tachyon on this host.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "tachyon-test-home-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		DataDirEnv:      filepath.Join(root, "data"),
		"XDG_DATA_HOME": filepath.Join(root, "xdg-data"),
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// With no TACHYON_DATA_DIR the default comes from XDG_DATA_HOME, which TestMain moved.
func TestNoOverrideStaysInsideTheTempRoot(t *testing.T) {
	d, err := Resolve(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Data, os.TempDir()) {
		t.Fatalf("default data directory %s is outside %s", d.Data, os.TempDir())
	}
}

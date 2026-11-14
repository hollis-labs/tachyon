package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/appdirs"
)

// TestMain points every default location at a temp directory, so no test can touch the live data
// directory of a Tachyon on this host, even one that forgets to set TACHYON_DATA_DIR itself.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "tachyon-test-home-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		appdirs.DataDirEnv: filepath.Join(root, "data"),
		"XDG_DATA_HOME":    filepath.Join(root, "xdg-data"),
		"XDG_CONFIG_HOME":  filepath.Join(root, "xdg-config"),
		"XDG_STATE_HOME":   filepath.Join(root, "xdg-state"),
		"XDG_CACHE_HOME":   filepath.Join(root, "xdg-cache"),
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// A run with no override at all must never resolve to the real location: whatever the process
// environment says, the data directory of this test binary is under the temp root.
func TestDefaultDataDirIsNotTheLiveOne(t *testing.T) {
	for name, getenv := range map[string]func(string) string{
		"process environment": os.Getenv,
		"no override": func(k string) string {
			if k == appdirs.DataDirEnv {
				return ""
			}
			return os.Getenv(k)
		},
	} {
		dirs, err := appdirs.Resolve(getenv)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(dirs.Data, os.TempDir()) {
			t.Errorf("%s: data directory %s is outside %s; a test would write to a live location", name, dirs.Data, os.TempDir())
		}
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dirs.Data, filepath.Join(home, ".local")) {
			t.Errorf("%s: data directory %s is under the real home", name, dirs.Data)
		}
	}
}

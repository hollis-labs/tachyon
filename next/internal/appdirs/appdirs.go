// Package appdirs decides where Tachyon keeps its files: the database, the identity signing key and
// the cache directory, under one data directory. That is XDG_DATA_HOME/tachyon-next (through the
// shared apppaths layer), or TACHYON_DATA_DIR when the operator sets it. The manifest names the
// files; relative names are anchored in the data directory.
//
// The default is deliberately not XDG_DATA_HOME/tachyon: that is the data directory of the running
// old build, which this build must never create, tighten or write into (port plan section 8: the
// migration copies into a new, empty directory). Promotion (slice P1) decides the final default.
package appdirs

import (
	"fmt"
	"os"
	"path/filepath"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
)

const (
	appName = "tachyon-next"
	// DataDirEnv overrides the whole data directory. It is the one setting read from the
	// environment (adopting-substrate section 6).
	DataDirEnv = "TACHYON_DATA_DIR"
)

// Dirs are the resolved directories.
type Dirs struct{ Data string }

// Resolve returns the data directory. getenv is os.Getenv outside tests. Nothing is created.
func Resolve(getenv func(string) string) (Dirs, error) {
	if v := getenv(DataDirEnv); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return Dirs{}, fmt.Errorf("resolve %s: %w", DataDirEnv, err)
		}
		return Dirs{Data: abs}, nil
	}
	layout, err := paths.Resolve(appName, paths.WithoutMaterialize())
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve application directories: %w", err)
	}
	return Dirs{Data: layout.DataDir()}, nil
}

// PrepareDir creates the data directory (0700) when missing and tightens it when it exists.
func (d Dirs) PrepareDir() error { return PrivateDir(d.Data) }

// PrivateDir creates dir (0700) when missing and tightens it when it exists.
func PrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // 0700 on a directory is the point: owner-only
}

// EnsurePrivate creates the file at path (0600) when missing and tightens it when it exists, so
// the database is never readable by other users. The signing key creates itself private.
func EnsurePrivate(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is the configured database file
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Anchor returns path unchanged when it is absolute, and otherwise joins it to the data directory.
func (d Dirs) Anchor(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(d.Data, path)
}

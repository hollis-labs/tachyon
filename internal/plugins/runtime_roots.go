package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func pluginCacheDir(id string) (string, error) {
	root := os.Getenv("TACHYON_CACHE_DIR")
	if root == "" {
		root = os.Getenv("XDG_CACHE_HOME")
		if !filepath.IsAbs(root) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			root = filepath.Join(home, ".cache")
		}
		root = filepath.Join(root, "tachyon", "plugins")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("cache root must be absolute")
	}
	return filepath.Join(root, id), nil
}

// Existing symlinks must not redirect writable roots into the plugin bundle.
func runtimeResolved(path string) (string, error) {
	tail := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		path = parent
	}
}
func validateRuntimeRoots(bundle, data, cache string) error {
	bundle, err := runtimeResolved(bundle)
	if err != nil {
		return err
	}
	roots := []string{data, cache}
	for i, root := range roots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("writable root must be absolute")
		}
		roots[i], err = runtimeResolved(root)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bundle, roots[i])
		if err != nil {
			return err
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return fmt.Errorf("writable root is inside plugin bundle")
		}
	}
	if roots[0] == roots[1] {
		return fmt.Errorf("data and cache roots must differ")
	}
	return nil
}

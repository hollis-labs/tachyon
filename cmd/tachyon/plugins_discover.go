package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hollis-labs/tachyon/internal/plugins"
)

// discoveredPlugin is a plugin directory that is ready to spawn.
type discoveredPlugin struct {
	ID     string
	Binary string
}

// discoverPlugins lists the plugins under root. A plugin is a directory
// holding a plugin.yaml manifest and an executable named after the directory
// (plugins/<id>/<id>). Directories with a manifest but no executable are
// reported through missing so the caller can say how to build them; directories
// without a manifest (plugins/hello) are not plugins and are ignored.
// Results are ordered by directory name so load order, and therefore module
// ownership, is deterministic.
func discoverPlugins(root string) (found []discoveredPlugin, missing []string, err error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, err := os.Stat(filepath.Join(root, id, "plugin.yaml")); err != nil {
			continue
		}
		bin := filepath.Join(root, id, id)
		info, err := os.Stat(bin)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			missing = append(missing, id)
			continue
		}
		found = append(found, discoveredPlugin{ID: id, Binary: bin})
	}
	return found, missing, nil
}

// loadDiscoveredPlugins loads every plugin discoverPlugins finds, so adding a
// plugin takes no edit to main.go. A plugin that fails to load is logged and
// skipped, as before.
func loadDiscoveredPlugins(ctx context.Context, mgr *plugins.Manager, root string, logger *slog.Logger) {
	found, missing, err := discoverPlugins(root)
	if err != nil {
		logger.Warn("plugin discovery failed", "root", root, "error", err)
		return
	}
	for _, id := range missing {
		logger.Warn("plugin binary missing (build it with: make build-plugins)", "id", id)
	}
	for _, p := range found {
		if err := mgr.LoadPlugin(ctx, p.Binary); err != nil {
			logger.Warn("failed to load plugin", "id", p.ID, "error", err)
		}
	}
}

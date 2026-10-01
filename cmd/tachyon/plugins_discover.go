package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

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
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, startupFailure(id, filepath.Join(root, id, "plugin.yaml"), "filesystem", err)
		}
		bin := filepath.Join(root, id, id)
		info, err := os.Stat(bin)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, startupFailure(id, bin, "filesystem", err)
		}
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			missing = append(missing, id)
			continue
		}
		found = append(found, discoveredPlugin{ID: id, Binary: bin})
	}
	return found, missing, nil
}

// loadDiscoveredPlugins admits every available executable, in directory order.
// Optional unbuilt plugins are warnings; a present executable failing admission
// is fatal. Explicit legacy capability rejections are handled by Manager.
func loadDiscoveredPlugins(ctx context.Context, mgr *plugins.Manager, root string, logger *slog.Logger) error {
	found, missing, err := discoverPlugins(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Warn("plugin directory missing", "root", root)
			return nil
		}
		var failure *startupAdmissionError
		if errors.As(err, &failure) {
			return err
		}
		return startupFailure("discovery", root, "filesystem", err)
	}
	for _, id := range missing {
		logger.Warn("plugin binary missing (build it with: make build-plugins)", "id", id)
	}
	for _, p := range found {
		if err := mgr.LoadPlugin(ctx, p.Binary); err != nil {
			return startupFailure(p.ID, p.Binary, "lifecycle", err)
		}
	}
	return nil
}

// Rollback has a separate budget so an expired startup context cannot skip
// cleanup. Shutdown force-stops and reaps each child once this grace expires.
const startupRollbackTimeout = 5 * time.Second

// loadStartupPlugins finishes admission before the caller constructs a listener.
// On failure the rejected child is already stopped by LoadPlugin; rollback also
// removes every previously admitted child and its registrations.
func loadStartupPlugins(ctx context.Context, mgr *plugins.Manager, root string, logger *slog.Logger) (err error) {
	defer func() {
		if err == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), startupRollbackTimeout)
		defer cancel()
		if cleanupErr := mgr.Shutdown(cleanup); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("startup rollback: %w", cleanupErr))
		}
	}()
	// hello is the manifest-free proof-of-concept; its absence remains optional.
	hello := filepath.Join(root, "hello", "hello")
	if info, statErr := os.Stat(hello); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return startupFailure("hello", hello, "filesystem", statErr)
		}
		logger.Warn("hello plugin binary missing", "binary", hello)
	} else if info.IsDir() || info.Mode()&0o111 == 0 {
		logger.Warn("hello plugin binary not executable", "binary", hello)
	} else if err := mgr.LoadPlugin(ctx, hello); err != nil {
		return startupFailure("hello", hello, "lifecycle", err)
	}
	return loadDiscoveredPlugins(ctx, mgr, root, logger)
}

// Directory identity and binary path stay attached through rollback wrapping.
type startupAdmissionError struct {
	id, path, reason string
	err              error
}

func (e *startupAdmissionError) Error() string {
	return fmt.Sprintf("plugin %q (%s): %s", e.id, e.path, e.err)
}
func (e *startupAdmissionError) Unwrap() error { return e.err }
func startupFailure(id, path, reason string, err error) error {
	var admission *plugins.AdmissionError
	if errors.As(err, &admission) {
		reason = admission.Reason
	}
	return &startupAdmissionError{id: id, path: path, reason: reason, err: err}
}

func logStartupRefusal(logger *slog.Logger, err error) {
	var failure *startupAdmissionError
	if errors.As(err, &failure) {
		logger.Error("plugin startup refused", "plugin_id", failure.id, "path", failure.path, "reason_class", failure.reason, "error", err)
		return
	}
	logger.Error("plugin startup refused", "reason_class", "cleanup", "error", err)
}

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"

	"github.com/hollis-labs/tachyon/next/internal/appdirs"
	"github.com/hollis-labs/tachyon/next/internal/oldlayout"
	"github.com/hollis-labs/tachyon/next/internal/settings"
)

// bootOptions are this app's boot hooks. After the config module has applied the SUBSTRATE_CONFIG
// override, the data directory is resolved and the files that live in it (the database, the
// identity signing key, the cache directory) are anchored there. With prepare it also creates the
// directory and secures the database file; the read-only commands pass false and create nothing
// (see plan). Before the database module opens anything, a data directory of the old build is
// refused.
func bootOptions(getenv func(string) string, prepare bool) boot.Options {
	opts := boot.Options{AfterConfig: func(_ context.Context, app *module.App) error {
		return anchorPaths(app, getenv, prepare)
	}}
	// The database module starts only for the commands that prepare (see plan); a hook naming a
	// module that is not started is an error, so it is added only then.
	if prepare {
		opts.Before = map[string]boot.Hook{"database": func(_ context.Context, app *module.App) error {
			sqlite, err := configBlock(app, "database", "sqlite")
			if err != nil {
				return err
			}
			path, _ := sqlite["path"].(string)
			return oldlayout.Refuse(path)
		}}
	}
	return opts
}

// plan decides, from the command line, which framework modules to start and whether the data
// directory may be created. Only serving (the default) and `identity` (the offline owner and API
// key bootstrap) write anything: they need the database and identity modules and create the data
// directory, the database and the signing key, exactly as serving does. Every other command,
// including version, config, health and help, reads configuration and starts no module that opens
// a file, so running one without TACHYON_DATA_DIR cannot touch any data directory.
//
// Every argument that does not start with "-" counts as a command word. That is right only while
// the root command has no global flag that takes a value: `--level debug serve` would read "debug"
// as the command. TestRootHasNoValueFlags fails when such a flag is added, and then plan must
// learn it.
func plan(args []string) (modules []string, prepare bool) {
	var words []string
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "--version" {
			return pick(readOnly), false
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	switch {
	case len(words) == 0 || words[0] == "serve":
		return substrateModules, true
	case words[0] == "identity":
		return pick(withIdentity), true
	}
	return pick(readOnly), false
}

// readOnly are the modules that open no file and bind no port when started. secrets only builds a
// resolver; it reads nothing until asked.
var readOnly = []string{"config", "logging", "health", "secrets", "cli"}

// withIdentity adds what the offline identity commands need: the database and the identity module
// (the auth module is where identity looks for the guard it serves; it binds no port).
var withIdentity = []string{"config", "logging", "health", "auth", "database", "identity", "secrets", "cli"}

// pick keeps the start order of substrateModules.
func pick(want []string) []string {
	var out []string
	for _, m := range substrateModules {
		if slices.Contains(want, m) {
			out = append(out, m)
		}
	}
	return out
}

// anchorPaths turns the manifest's relative defaults (database.sqlite.path, identity.key_file,
// tachyon.cache_dir) into paths inside the data directory. A path the operator's override made
// absolute is kept. Only with prepare does it create anything.
func anchorPaths(app *module.App, getenv func(string) string, prepare bool) error {
	dirs, err := appdirs.Resolve(getenv)
	if err != nil {
		return err
	}
	sqlite, err := configBlock(app, "database", "sqlite")
	if err != nil {
		return err
	}
	dbPath, err := anchor(dirs, sqlite, "path", "database.sqlite.path")
	if err != nil {
		return err
	}
	id, err := configBlock(app, "identity")
	if err != nil {
		return err
	}
	if _, err = anchor(dirs, id, "key_file", "identity.key_file"); err != nil {
		return err
	}
	own, err := configBlock(app, settings.Key)
	if err != nil {
		return err
	}
	cacheDir, err := anchor(dirs, own, "cache_dir", settings.Key+".cache_dir")
	if err != nil {
		return err
	}
	// Every command refuses a bad tachyon block, naming the key, before anything is created.
	if _, err = settings.Parse(own); err != nil {
		return err
	}
	if !prepare {
		return nil
	}
	// The old build's data directory is refused before anything is created or tightened in it.
	if err = oldlayout.Refuse(dbPath); err != nil {
		return err
	}
	if err = dirs.PrepareDir(); err != nil {
		return fmt.Errorf("prepare the data directory %s: %w", dirs.Data, err)
	}
	if err = appdirs.PrivateDir(cacheDir); err != nil {
		return fmt.Errorf("prepare the cache directory %s: %w", cacheDir, err)
	}
	// Create the database file private before the database module opens it, when its directory
	// exists (an override may name a directory the module is meant to refuse or create itself).
	if fi, statErr := os.Stat(filepath.Dir(dbPath)); statErr == nil && fi.IsDir() {
		return appdirs.EnsurePrivate(dbPath)
	}
	return nil
}

// anchor rewrites block[key], a non-empty string, to its anchored form and returns it.
func anchor(dirs appdirs.Dirs, block map[string]any, key, name string) (string, error) {
	v, ok := block[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("config: %s must be a non-empty string", name)
	}
	block[key] = dirs.Anchor(v)
	return block[key].(string), nil
}

// configBlock walks app.Config down the named keys; every level must be a map.
func configBlock(app *module.App, path ...string) (map[string]any, error) {
	cur := app.Config
	for i, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config: %s must be a map", strings.Join(path[:i+1], "."))
		}
		cur = next
	}
	return cur, nil
}

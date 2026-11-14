// Command tachyon is the Tachyon control plane on Substrate. The shell (config, logging, health,
// HTTP, auth, identity, database, secrets, scheduler, plugin host, CLI skeleton) comes from the
// manifest; this file and internal/ are the app. Port plan: docs/substrate-port-plan.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	libsched "github.com/hollis-labs/libs/core/util/scheduler"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/scheduler"

	"github.com/hollis-labs/tachyon/next/internal/objectsync"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// Hooks the modules read at start, registered before any boot (tests boot without main):
// the schema, which the database module applies (internal/store), and the scheduler's dispatcher, so
// fired jobs go to Tachyon's own handlers, never to a queue module this app does not run.
func init() {
	store.Use()
	scheduler.UseRunner(jobs{})
	scheduler.UseOptionsFunc(func(libsched.Store) []libsched.Option { return objectsync.EngineOptions() })
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tachyon:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, args []string) error {
	modules, prepare := plan(args)
	opts := bootOptions(getenv, prepare)
	if migrating(args) {
		opts = migrateOptions(getenv, opts)
	}
	started, err := boot.StartWith(ctx, substrateApp, modules, substrateConfigJSON, opts)
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()

	return (&tachyon{app: started.App}).root(ctx, args).ExecuteContext(ctx)
}

// root is the command tree: the framework's skeleton plus serve. `tachyon` with no command serves,
// like `tachyon serve`.
func (t *tachyon) root(ctx context.Context, args []string) *cobra.Command {
	rootCmd := cli.Root(t.app)
	rootCmd.SetArgs(args) // the same words plan() looked at
	serve := t.serveCmd(ctx)
	rootCmd.RunE = func(c *cobra.Command, args []string) error { return serve.RunE(c, args) }
	rootCmd.Args = cobra.NoArgs
	rootCmd.AddCommand(serve, t.migrateFromCmd(ctx), t.accessCmd(ctx), t.syncObjectsCmd(ctx))
	return rootCmd
}

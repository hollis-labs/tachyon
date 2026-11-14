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

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/scheduler"
)

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
	// Fired jobs go to Tachyon's own dispatcher, never to a queue module this app does not run.
	scheduler.UseRunner(jobs{})
	modules, prepare := plan(args)
	started, err := boot.StartWith(ctx, substrateApp, modules, substrateConfigJSON, bootOptions(getenv, prepare))
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
	rootCmd.AddCommand(serve)
	return rootCmd
}

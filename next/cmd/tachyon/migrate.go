package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	paths "github.com/hollis-labs/libs/core/util/apppaths"

	"github.com/hollis-labs/tachyon/next/internal/migratefrom"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// migrateFromCmd is the one-way data migration from the old build (port plan section 5). It runs
// on a new, empty data directory (refused otherwise, before anything is created: migrateOptions),
// after the database module has created the schema and the scheduler module its tables.
func (t *tachyon) migrateFromCmd(ctx context.Context) *cobra.Command {
	var source, report string
	cmd := &cobra.Command{
		Use:   "migrate-from --source DIR",
		Short: "Copy the old build's data (sqlite3 .backup copies of tachyon.db and jobs.db) into this new data directory, and prove it",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			app := t.app
			cfg, err := settings.Parse(app.Config[settings.Key])
			if err != nil {
				return err
			}
			sqlite, err := configBlock(app, "database", "sqlite")
			if err != nil {
				return err
			}
			dbPath, _ := sqlite["path"].(string)
			if report == "" {
				report = filepath.Join(filepath.Dir(dbPath), migratefrom.ReportFile)
			}
			refused := []string{filepath.Dir(dbPath)}
			// The old build's live data directory (XDG data home/tachyon) is never a source.
			if old, e := paths.Resolve("tachyon", paths.WithoutMaterialize()); e == nil {
				refused = append(refused, old.DataDir())
			}
			r, err := migratefrom.Run(ctx, store.From(app), migratefrom.Options{
				Source:   source,
				Refused:  refused,
				JobTypes: jobTypes,
				Instances: map[string]string{
					"tether":   cfg.Upstreams.Tether.URL,
					"github":   cfg.Upstreams.GitHub.URL,
					"cerberus": cfg.Upstreams.Cerberus.URL,
				},
				Report: report,
			})
			out := c.OutOrStdout()
			if r != nil && r.OK {
				var rows int64
				for _, tr := range r.Tables {
					rows += tr.TargetRows
				}
				_, _ = fmt.Fprintf(out, "migrated %d tables, %d rows, %d sequences; every count and checksum equal; sources unchanged; %d ms\n",
					len(r.Tables), rows, len(r.Sequence), r.DurationMS)
			}
			_, _ = fmt.Fprintf(out, "report: %s\n", report)
			return err
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "directory holding the .backup copies of tachyon.db and jobs.db (never the old data directory)")
	cmd.Flags().StringVar(&report, "report", "", "report file (default: migrate-from-report.json beside the new database)")
	return cmd
}

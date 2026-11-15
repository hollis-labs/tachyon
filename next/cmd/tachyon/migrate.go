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
	var allowUnchecked, filesOnly bool
	cmd := &cobra.Command{
		Use:   "migrate-from --source DIR",
		Short: "Copy the old build's data (sqlite3 .backup copies of tachyon.db and jobs.db, and a copy of its workspace/ files) into this new data directory, and prove it",
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
			migrated := filepath.Join(filepath.Dir(dbPath), migratefrom.ReportFile)
			if report == "" {
				report = migrated
				if filesOnly {
					report = filepath.Join(filepath.Dir(dbPath), migratefrom.FilesReportFile)
				}
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
				WorkspaceTarget: cfg.WorkspaceDir,
				FilesOnly:       filesOnly,
				Migrated:        migrated,
				Report:          report,
				AllowUnchecked:  allowUnchecked,
			})
			out := c.OutOrStdout()
			if r != nil && r.OK && !filesOnly {
				var rows int64
				for _, tr := range r.Tables {
					rows += tr.TargetRows
				}
				_, _ = fmt.Fprintf(out, "migrated %d tables, %d rows, %d sequences; every count and checksum equal; sources unchanged; %d ms\n",
					len(r.Tables), rows, len(r.Sequence), r.DurationMS)
			}
			if r != nil && r.OK && r.Files != nil {
				f := r.Files
				_, _ = fmt.Fprintf(out, "chat workspace files: %d in the source (%d bytes), %d copied, %d already equal, %d named by a row; every sha256 equal\n",
					len(f.Files), f.Bytes, f.Copied, f.AlreadyEqual, f.ReferencedRows)
			}
			_, _ = fmt.Fprintf(out, "report: %s\n", report)
			return err
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "directory holding the .backup copies of tachyon.db and jobs.db, and a cp -a copy of the old workspace/ if it exists (never the old data directory)")
	cmd.Flags().BoolVar(&allowUnchecked, "allow-unchecked-instances", false, "rehearsals only: report a schedule whose upstream is not configured as not checked instead of failing")
	cmd.Flags().StringVar(&report, "report", "", "report file (default: migrate-from-report.json beside the new database; migrate-from-files-report.json with --files-only)")
	cmd.Flags().BoolVar(&filesOnly, "files-only", false, "copy only the chat workspace files (source workspace/) into a target this command has already migrated; a rerun over a complete target copies nothing")
	return cmd
}

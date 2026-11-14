package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/tachyon/next/internal/objectsync"
	"github.com/hollis-labs/tachyon/next/internal/settings"
)

// syncObjectsCmd is the operator's object sync maintenance, offline on the data directory: import a
// source now and enable its recurring job, pause it, or show every schedule and the configured
// sources' scan state. It never starts the engine.
func (t *tachyon) syncObjectsCmd(ctx context.Context) *cobra.Command {
	var source string
	var status, enable, disable bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "sync-objects [--status | --enable | --disable] [--source NAME]",
		Short: "Import object sources and manage their recurring sync jobs",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if (status && (enable || disable)) || (enable && disable) {
				return errors.New("conflicting sync command options")
			}
			if enable && (interval < time.Minute || interval > 24*time.Hour) {
				return errors.New("sync interval must be between 1m and 24h")
			}
			cfg, err := settings.Parse(t.app.Config[settings.Key])
			if err != nil {
				return err
			}
			jobs, sources, err := objectSync(t.app, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = jobs.Close() }()
			enc := json.NewEncoder(c.OutOrStdout())
			enc.SetIndent("", "  ")
			if status {
				if err := printSchedules(ctx, enc, jobs); err != nil {
					return err
				}
			}
			var selected []objectsync.Source
			for _, s := range sources {
				if source == "all" || source == s.Name() {
					selected = append(selected, s)
				}
			}
			if len(selected) == 0 {
				if status {
					return nil // the schedules are listed; no source is configured in this process
				}
				return errors.New("requested source is not configured")
			}
			var problems []error
			for _, s := range selected {
				var runErr error
				switch {
				case disable:
					runErr = jobs.Disable(ctx, s)
				case !status:
					_, runErr = jobs.Coordinator().Run(ctx, s, "")
					if runErr == nil && enable {
						runErr = jobs.Enable(ctx, s, interval)
					}
				}
				state, stateErr := jobs.Coordinator().State(ctx, s.Scope())
				schedule, exists, scheduleErr := jobs.Store.GetSchedule(ctx, objectsync.SourceID(s))
				view := struct {
					Name      string           `json:"name"`
					State     objectsync.State `json:"state"`
					Scheduled bool             `json:"scheduled"`
					Enabled   bool             `json:"enabled"`
					Interval  string           `json:"interval,omitempty"`
					NextRun   time.Time        `json:"next_run,omitempty"`
				}{Name: s.Name(), State: state, Scheduled: exists, Enabled: schedule.Enabled, Interval: schedule.Interval.String(), NextRun: schedule.NextRun}
				problems = append(problems, runErr, stateErr, scheduleErr, enc.Encode(view))
			}
			if err := errors.Join(problems...); err != nil {
				return fmt.Errorf("object sync: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "all", "tether-projects, tether-agents, tether-workstreams, tether-sessions, cerberus-machines, github-repositories, or all")
	cmd.Flags().BoolVar(&status, "status", false, "list every schedule and the configured sources' scan state, without fetching")
	cmd.Flags().BoolVar(&enable, "enable", false, "import, then enable the recurring job")
	cmd.Flags().BoolVar(&disable, "disable", false, "pause the recurring job without fetching")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Minute, "interval for a newly enabled job")
	return cmd
}

// printSchedules lists every schedule in the store, configured here or not.
func printSchedules(ctx context.Context, enc *json.Encoder, jobs *objectsync.Jobs) error {
	all, err := jobs.Store.ListSchedules(ctx)
	if err != nil {
		return err
	}
	type view struct {
		ID       string    `json:"id"`
		JobType  string    `json:"job_type"`
		Enabled  bool      `json:"enabled"`
		Interval string    `json:"interval"`
		NextRun  time.Time `json:"next_run"`
	}
	out := []view{}
	for _, s := range all {
		out = append(out, view{ID: s.ID, JobType: s.JobType, Enabled: s.Enabled, Interval: s.Interval.String(), NextRun: s.NextRun})
	}
	return enc.Encode(map[string]any{"schedules": out})
}

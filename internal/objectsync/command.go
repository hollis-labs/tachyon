package objectsync

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Command is a narrow operator maintenance command over the same services the
// host runs. It does not start plugins, an HTTP listener or a scheduler loop.
func Command(ctx context.Context, dataDir string, u catalog.Upstreams, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("tachyon sync-objects", flag.ContinueOnError)
	flags.SetOutput(out)
	sourceName := flags.String("source", "all", "tether-projects, tether-agents, cerberus-machines, github-repositories, or all")
	database := flags.String("data-dir", dataDir, "directory containing tachyon.db and jobs.db")
	status := flags.Bool("status", false, "show scan state and persisted schedules without fetching")
	enable := flags.Bool("enable", false, "enable a recurring job after the import succeeds")
	disable := flags.Bool("disable", false, "pause the recurring job without fetching")
	interval := flags.Duration("interval", 5*time.Minute, "interval for newly enabled jobs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*status && (*enable || *disable)) || (*enable && *disable) {
		return errors.New("conflicting sync command options")
	}
	if *enable && (*interval < time.Minute || *interval > 24*time.Hour) {
		return errors.New("sync interval must be between 1m and 24h")
	}
	sources, err := Sources(u)
	if err != nil {
		return err
	}
	var selected []Source
	for _, s := range sources {
		if *sourceName == "all" || *sourceName == s.Name() {
			selected = append(selected, s)
		}
	}
	if len(selected) == 0 {
		return errors.New("requested source is not configured")
	}
	store, err := localstore.Open(ctx, filepath.Join(*database, "tachyon.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	c := New(store)
	jobs, err := OpenJobs(ctx, filepath.Join(*database, "jobs.db"), c, sources)
	if err != nil {
		return err
	}
	defer jobs.Close()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	var problems []error
	for _, s := range selected {
		var runErr error
		if *disable {
			runErr = jobs.Disable(ctx, s)
		} else if !*status {
			_, runErr = c.Run(ctx, s, "")
			if runErr == nil && *enable {
				runErr = jobs.Enable(ctx, s, *interval)
			}
		}
		state, stateErr := c.State(ctx, s.Scope())
		schedule, exists, scheduleErr := jobs.Store.GetSchedule(ctx, sourceID(s.Scope()))
		view := struct {
			Name      string    `json:"name"`
			State     State     `json:"state"`
			Scheduled bool      `json:"scheduled"`
			Enabled   bool      `json:"enabled"`
			Interval  string    `json:"interval,omitempty"`
			NextRun   time.Time `json:"next_run,omitempty"`
		}{Name: s.Name(), State: state, Scheduled: exists, Enabled: schedule.Enabled, Interval: schedule.Interval.String(), NextRun: schedule.NextRun}
		problems = append(problems, runErr, stateErr, scheduleErr, enc.Encode(view))
	}
	if err = errors.Join(problems...); err != nil {
		return fmt.Errorf("object sync: %w", err)
	}
	return nil
}

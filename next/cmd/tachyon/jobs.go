package main

import (
	"context"
	"fmt"
	"slices"
	"sync"

	libsched "github.com/hollis-labs/libs/core/util/scheduler"

	"github.com/hollis-labs/tachyon/next/internal/objectsync"
)

// jobTypes are the job types this build owns, under the old build's names, so the migrated
// schedules stay valid (migrate-from refuses a schedule of any other type).
var jobTypes = []string{objectsync.JobType}

// jobs is the scheduler's dispatcher: each job type is routed to the runner `serve` registered for
// it. The engine runs only under `serve` and only when the type's feature switch is on, so a fire of
// a type with no runner yet is a configuration error; it fails and is retried under the schedule's
// policy, and nothing is dropped silently.
type jobs struct{}

var (
	runnersMu sync.Mutex
	runners   = map[string]libsched.Runner{}
)

// setRunner registers the runner of a job type (nil removes it).
func setRunner(jobType string, r libsched.Runner) {
	runnersMu.Lock()
	defer runnersMu.Unlock()
	if r == nil {
		delete(runners, jobType)
		return
	}
	runners[jobType] = r
}

func (jobs) Enqueue(ctx context.Context, job libsched.Job) error {
	runnersMu.Lock()
	r, ok := runners[job.JobType]
	runnersMu.Unlock()
	switch {
	case ok:
		return r.Enqueue(ctx, job)
	case slices.Contains(jobTypes, job.JobType):
		return fmt.Errorf("tachyon: no runner is started for job type %q (schedule %s)", job.JobType, job.ScheduleID)
	}
	return fmt.Errorf("tachyon: no handler for job type %q (schedule %s)", job.JobType, job.ScheduleID)
}

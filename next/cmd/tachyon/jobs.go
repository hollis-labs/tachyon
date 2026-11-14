package main

import (
	"context"
	"fmt"
	"slices"

	libsched "github.com/hollis-labs/libs/core/util/scheduler"
)

// jobTypes are the job types this build owns, under the old build's names, so the migrated
// schedules stay valid (migrate-from refuses a schedule of any other type). object-sync.v1 is the
// object sync; its handler arrives with the slice that ports it. Until then its fire fails and is
// retried under the schedule's policy, and the engine does not run at all unless
// tachyon.features.object_sync is on.
var jobTypes = []string{"object-sync.v1"}

// jobs is the scheduler's dispatcher. Each job type Tachyon schedules gets a case here in the slice
// that ports it (object sync: object-sync.v1). An unknown type is an error, which the engine
// records on the fire and retries under the schedule's policy; nothing is dropped silently.
type jobs struct{}

func (jobs) Enqueue(_ context.Context, job libsched.Job) error {
	if slices.Contains(jobTypes, job.JobType) {
		return fmt.Errorf("tachyon: the handler for job type %q is not ported yet (schedule %s)", job.JobType, job.ScheduleID)
	}
	return fmt.Errorf("tachyon: no handler for job type %q (schedule %s)", job.JobType, job.ScheduleID)
}

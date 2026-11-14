package main

import (
	"context"
	"fmt"

	libsched "github.com/hollis-labs/libs/core/util/scheduler"
)

// jobs is the scheduler's dispatcher. Each job type Tachyon schedules gets a case here in the slice
// that ports it (object sync: object-sync.v1). An unknown type is an error, which the engine
// records on the fire and retries under the schedule's policy; nothing is dropped silently.
type jobs struct{}

func (jobs) Enqueue(_ context.Context, job libsched.Job) error {
	return fmt.Errorf("tachyon: no handler for job type %q (schedule %s)", job.JobType, job.ScheduleID)
}

package main

import (
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
)

// The scheduler and syncer find optional features by type assertion, so the
// jobs must carry the platform client itself, not the configured wrapper.
func TestScheduleJobsKeepOptionalInterfaces(t *testing.T) {
	jobs := scheduleJobs([]configured{{Platform: fake.New(), concurrency: 2}})
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want full and quick", len(jobs))
	}
	for _, j := range jobs {
		if _, ok := j.Platform.(platform.StatsFetcher); !ok {
			t.Errorf("%s job's platform (%T) hides platform.StatsFetcher", j.Mode, j.Platform)
		}
	}
}

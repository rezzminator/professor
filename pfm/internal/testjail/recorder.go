package testjail

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// PauseFlightRecorder stops this process's flight recorder (profile.go) for the
// rest of t, and t's cleanup starts a new one with the same window. A test
// that reads process-wide allocation or heap counters (testing.AllocsPerRun,
// runtime.MemStats) calls it before its first measurement: the recorder's
// reader goroutine allocates while it drains the trace, and those counters
// count every goroutine's allocations, not only the code under test.
//
// The test stays serial: the helper first sets PFM_TEST_PROFILE to the value
// it already has, so Go's t.Setenv / t.Parallel panic stops a parallel caller,
// before or after the call. With profiling off, or no recorder running, that
// is all it does.
func PauseFlightRecorder(t *testing.T) {
	t.Helper()
	t.Setenv(paths.EnvTestProfile, paths.TestProfileMode())
	p := activeProfiler.Load()
	if p == nil || !p.pauseRecorder(t.Name()) {
		return
	}
	t.Cleanup(p.resumeRecorder)
}

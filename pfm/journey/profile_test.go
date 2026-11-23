//go:build journey

package journey

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pfmPackages is the module's package pattern: the coverage counts pfm's own source and nothing the
// same GOFLAGS instrumented beside it (engine-sync).
const pfmPackages = "github.com/rezzminator/professor/pfm/..."

// profile writes the run's profile into the artifacts directory once the suite has run, green or red:
// TestJourney's windDown has already ended every pfm process so each wrote its coverage counters; it merges
// them (harness.Cover) and writes summary.md beside the steps.tsv and waits.tsv the walk wrote.
func profile(artifacts, coverDir string, wall time.Duration, meter *machineMeter) error {
	machine, err := meter.read()
	if err != nil {
		return err
	}
	summary := harness.Summary{Wall: wall, Machine: machine}
	if coverDir == "" {
		summary.Coverage.Missing = "GOCOVERDIR is unset: run the suite through infra/fence/journey/run.sh or CI's Linux journey job"
		return harness.WriteSummary(artifacts, summary)
	}
	if summary.Coverage, err = harness.Cover(artifacts, coverDir, "..", pfmPackages); err != nil {
		return err
	}
	return harness.WriteSummary(artifacts, summary)
}

// windDown sends SIGTERM to every pfm process of the owner's, again to any a dying one started, and waits on
// the wait door until none runs: a process that exits writes its counters, and a daemon or a seat host would
// otherwise outlive the run. A run without the profile (no JOURNEY_ARTIFACTS or GOCOVERDIR) winds nothing
// down. A process still running at the door's hung-only limit is named in the failure.
func windDown(t *testing.T) {
	env := paths.OSEnv{}
	if env.Get("JOURNEY_ARTIFACTS") == "" || env.Get("GOCOVERDIR") == "" {
		return
	}
	procs, err := gather.OpenProcFS("")
	if err != nil {
		t.Fatalf("wind-down: open the process table: %v", err)
	}
	door := gather.NewDoor(procs)
	type generation struct {
		pid   int
		start uint64
	}
	signalled := map[generation]bool{}
	var failed error
	testwait.Until(t, "every pfm process of the owner's to exit after SIGTERM", func() (bool, string) {
		running, err := pfmProcesses(procs, door.UID)
		if err != nil {
			failed = err
			return true, err.Error()
		}
		if len(running) == 0 {
			return true, ""
		}
		for _, id := range running {
			key := generation{id.PID, id.Start}
			if signalled[key] {
				continue
			}
			signalled[key] = true
			if _, err := door.Signal(gather.Target{Identity: id}, syscall.SIGTERM); err != nil {
				failed = fmt.Errorf("wind-down: SIGTERM pid %d %q: %w", id.PID, id.Argv, err)
				return true, failed.Error()
			}
		}
		return false, fmt.Sprintf(
			"%d pfm processes still run, first pid %d: %q",
			len(running),
			running[0].PID,
			running[0].Argv,
		)
	})
	if failed != nil {
		t.Fatalf("%v", failed)
	}
}

// pfmProcesses is every live process of uid whose argv[0] is pfm's binary.
func pfmProcesses(procs gather.ProcFS, uid uint32) ([]gather.Identity, error) {
	pids, err := procs.PIDs()
	if err != nil {
		return nil, fmt.Errorf("wind-down: list the process table: %w", err)
	}
	var running []gather.Identity
	for _, pid := range pids {
		argv, err := procs.Cmdline(pid)
		if err != nil || len(argv) == 0 || !pfmProcess(process{Args: strings.Join(argv, " ")}) {
			continue // gone since the listing, a zombie, or not pfm
		}
		id, err := gather.IdentifyProcess(procs, pid)
		if gather.IsGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("wind-down: identify pid %d %q: %w", pid, argv, err)
		}
		if id.UID == uid && pid != os.Getpid() {
			running = append(running, id)
		}
	}
	return running, nil
}

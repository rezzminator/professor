//go:build journey

package journey

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pfmPackages is the module's package pattern: the coverage counts pfm's own source and nothing the
// same GOFLAGS instrumented beside it (engine-sync).
const pfmPackages = "github.com/rezzminator/professor/pfm/..."

// windDownBound bounds the wind-down: how long pfm's processes get to write their counters and exit.
const windDownBound = 20 * time.Second

// profile writes the run's profile into the artifacts directory once the suite has run, green or red:
// it ends every pfm process still running so each writes its coverage counters, merges them
// (harness.Cover) and writes summary.md beside the steps.tsv and waits.tsv the walk wrote.
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
	if err := windDown(); err != nil {
		return err
	}
	if summary.Coverage, err = harness.Cover(artifacts, coverDir, "..", pfmPackages); err != nil {
		return err
	}
	return harness.WriteSummary(artifacts, summary)
}

// windDown sends SIGTERM to every pfm process of the owner's, again to any a dying one started, until
// none runs or windDownBound passes: a process that exits writes its counters, and a daemon or a seat
// host would otherwise outlive the run. What still runs at the bound is named in the error.
func windDown() error {
	procs, err := gather.OpenProcFS("")
	if err != nil {
		return fmt.Errorf("wind-down: open the process table: %w", err)
	}
	door := gather.NewDoor(procs)
	deadline := clock.Real.Now().Add(windDownBound)
	type generation struct {
		pid   int
		start uint64
	}
	signalled := map[generation]bool{}
	for {
		running, err := pfmProcesses(procs, door.UID)
		if err != nil {
			return err
		}
		if len(running) == 0 {
			return nil
		}
		if !clock.Real.Now().Before(deadline) {
			return fmt.Errorf("wind-down: %d pfm processes still run after %s, first pid %d: %q",
				len(running), windDownBound, running[0].PID, running[0].Argv)
		}
		for _, id := range running {
			key := generation{id.PID, id.Start}
			if signalled[key] {
				continue
			}
			signalled[key] = true
			if _, err := door.Signal(gather.Target{Identity: id}, syscall.SIGTERM); err != nil {
				return fmt.Errorf("wind-down: SIGTERM pid %d %q: %w", id.PID, id.Argv, err)
			}
		}
		time.Sleep(harness.PollTick) // POLL-STEP: the process table, bounded by windDownBound
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

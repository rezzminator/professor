package doctor

import (
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/restart"
)

func printHerdrIndicatorsDoctor(stdout io.Writer, home string, procs gather.ProcFS) (warnings, failures int) {
	pids, err := restart.StaleHerdrIndicators(restart.Deps{
		Procs: procs, Self: os.Getpid(), EffectiveUID: uint32(os.Geteuid()),
		Install: gather.InstallAt(home), Executable: agentlauncher.ExecTarget(home),
	})
	if err != nil {
		fmt.Fprintf(
			stdout,
			"doctor: herdr-indicators: ERROR %s — fix: repair process-table access, then rerun pfm doctor\n",
			err,
		)
		return 0, 1
	}
	if len(pids) != 0 {
		fmt.Fprintf(stdout, "doctor: herdr-indicators: WARN stale executable pids=%v — fix: pfm install --yes\n", pids)
		return 1, 0
	}
	fmt.Fprintln(stdout, "doctor: herdr-indicators: ok process enumeration confirms no stale painter")
	return 0, 0
}

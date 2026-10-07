package doctor

import (
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/hostcheck"
)

func printHostChecks(stdout io.Writer, runtime config.Runtime, now time.Time) (warnings, failures int) {
	rows := hostcheck.RunAll(hostcheck.EnvFor(runtime, now))
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "host-check: ok (%d checks)\n", len(hostcheck.Detectors()))
	}
	for _, row := range rows {
		fmt.Fprint(stdout, row.Render("host-check: "))
	}
	return hostcheck.Count(rows, hostcheck.Warn), hostcheck.Count(rows, hostcheck.Block)
}

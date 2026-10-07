package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func printManagedCleanupChecks(stdout io.Writer, runtime config.Runtime) (warnings, failures int) {
	dir := runtime.Paths.ManagedSettingsDir
	prefs := runtime.Config.Claude
	status := installer.InspectManagedCleanup(dir, prefs.RequireManagedCleanup, prefs.CleanupPeriodDays)
	switch status.State {
	case installer.ManagedCleanupOff:
		fmt.Fprintln(stdout, "managed-cleanup: check off by config")
	case installer.ManagedCleanupRelative:
		fmt.Fprintf(stdout, "managed-cleanup: managed settings dir %s is not absolute — nothing written\n", dir)
	case installer.ManagedCleanupMissing:
		fmt.Fprintf(
			stdout,
			"managed-cleanup: %s missing — transcripts older than 30 days are deleted by any Claude launch outside pfm; run: %s\n",
			status.Path,
			installer.ManagedCleanupFix(dir, status.Path, prefs.CleanupPeriodDays),
		)
		warnings++
	case installer.ManagedCleanupWrong:
		if status.KeyAbsent {
			fmt.Fprintf(
				stdout,
				"managed-cleanup: %s has no cleanupPeriodDays, want %d",
				status.Path,
				prefs.CleanupPeriodDays,
			)
		} else {
			fmt.Fprintf(
				stdout,
				"managed-cleanup: %s cleanupPeriodDays=%d, want %d",
				status.Path,
				status.Value,
				prefs.CleanupPeriodDays,
			)
		}
		fmt.Fprintf(stdout, "; run: %s\n", installer.ManagedCleanupFix(dir, status.Path, prefs.CleanupPeriodDays))
		warnings++
	case installer.ManagedCleanupUnreadable:
		fmt.Fprintf(stdout, "managed-cleanup: %s UNREADABLE error=%v\n", status.Path, status.Err)
		failures++
	case installer.ManagedCleanupOK:
		fmt.Fprintf(stdout, "managed-cleanup: %s ok\n", status.Path)
	}
	return warnings, failures
}

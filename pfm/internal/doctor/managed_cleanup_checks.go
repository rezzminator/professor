package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// prependConsequence is what a missing prependPlugins costs the person.
const prependConsequence = "Claude Code 2.1.299+ seats cc-plugin-sec-default and skips your plugin hooks"

func printManagedCleanupChecks(stdout io.Writer, runtime config.Runtime) (warnings, failures int) {
	dir := runtime.Paths.ManagedSettingsDir
	prefs := runtime.Config.Claude
	status := installer.InspectManagedCleanup(dir, prefs.RequireManagedCleanup, prefs.CleanupPeriodDays)
	// alsoUnset names the second gap when the repair line fixes both.
	alsoUnset := ""
	if status.Prepend == installer.PrependAbsent {
		alsoUnset = "; prependPlugins is unset too — " + prependConsequence
	}
	fix := installer.ManagedCleanupFix(dir, status.Path, status.Want)
	switch status.State {
	case installer.ManagedCleanupOff:
		fmt.Fprintln(stdout, "managed-cleanup: check off by config")
	case installer.ManagedCleanupRelative:
		fmt.Fprintf(stdout, "managed-cleanup: managed settings dir %s is not absolute — nothing written\n", dir)
	case installer.ManagedCleanupMissing:
		fmt.Fprintf(
			stdout,
			"managed-cleanup: %s missing — transcripts older than 30 days are deleted by any Claude launch outside pfm%s; run: %s\n",
			status.Path,
			alsoUnset,
			fix,
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
		fmt.Fprintf(stdout, "%s; run: %s\n", alsoUnset, fix)
		warnings++
	case installer.ManagedCleanupNoPrepend:
		fmt.Fprintf(
			stdout,
			"managed-cleanup: %s has no prependPlugins — %s; run: %s\n",
			status.Path,
			prependConsequence,
			fix,
		)
		warnings++
	case installer.ManagedCleanupUnreadable:
		fmt.Fprintf(stdout, "managed-cleanup: %s UNREADABLE error=%v\n", status.Path, status.Err)
		failures++
	case installer.ManagedCleanupOK:
		fmt.Fprintf(stdout, "managed-cleanup: %s ok\n", status.Path)
	}
	switch status.Prepend {
	case installer.PrependOther:
		fmt.Fprintf(stdout, "managed-cleanup: prependPlugins is set by %s — pfm leaves it\n", status.PrependSource)
	case installer.PrependInvalid:
		fmt.Fprintf(
			stdout,
			"managed-cleanup: %s prependPlugins is not a list of plugin ids, which Claude Code ignores — %s; set it to [] or a list of plugin ids\n",
			status.PrependSource,
			prependConsequence,
		)
		warnings++
	case installer.PrependUnknown:
		fmt.Fprintf(stdout, "managed-cleanup: %s UNREADABLE error=%v\n", status.PrependSource, status.PrependErr)
		failures++
	}
	return warnings, failures
}

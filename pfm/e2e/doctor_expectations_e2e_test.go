//go:build e2e

package e2e

import (
	"fmt"
	"strings"
)

// schedulerRowWarnings reports how many warnings doctor's service-manager row
// contributes on THIS host. The jail's scheduler fixture answers every probe
// but never enables the pfm-mcp unit, so the row warns wherever a user
// service manager answers at all (Linux systemd here) and stays silent where
// the manager is absent or reports the unit healthy — printServiceManagerDoctor
// renders those states apart, and only two of them are warnings.
//
// The row is a property of the HOST, not of the install, so the warning tally
// in requireSkippedHarvestDoctor adds it instead of hard-coding a number that
// would be right on one e2e platform and wrong on the other. Every OTHER
// warning still has to be zero: the count remains the check that a new
// unexpected doctor warning cannot slip through the fresh-install e2e.
func schedulerRowWarnings(output string) int {
	if strings.Contains(output, "doctor: service-manager=") &&
		(strings.Contains(output, "could_not_ask") || strings.Contains(output, "— start with: ")) {
		return 1
	}
	return 0
}

// claudePluginRowWarnings counts doctor's claude_plugins gap rows. The jail's
// stub writes settings and install records, so a completed install has zero.
func claudePluginRowWarnings(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "doctor: claude_plugins claude[") &&
			strings.HasSuffix(line, " — run pfm install --yes") {
			count++
		}
	}
	return count
}

func (h *e2eHarness) requireSkippedHarvestDoctor(result commandResult) {
	h.t.Helper()
	output := result.stdout + result.stderr
	if result.err == nil {
		h.t.Fatalf("doctor after --skip-harvest succeeded, want named unprovisioned dependencies; output=%q", output)
	}
	for _, want := range []string{
		"doctor: dep uv path= broken",
		"doctor: dep harvestpy path= broken",
		"doctor: harvestpy skipped",
		"doctor: pre-push gate=armed core.hooksPath=.githooks",
		"doctor: harness-prompt: matches baseline",
		"doctor: service-manager=",
		fmt.Sprintf("doctor: warnings=%d", 2+schedulerRowWarnings(output)),
	} {
		if !strings.Contains(output, want) {
			h.t.Fatalf(
				"doctor after --skip-harvest omitted %q; stdout=%q stderr=%q",
				want,
				result.stdout,
				result.stderr,
			)
		}
	}
	if gaps := claudePluginRowWarnings(output); gaps != 0 {
		h.t.Fatalf("doctor found %d Claude plugin gaps after install: %s", gaps, output)
	}
	// M2 (issue #24 finding 1): the unprovisioned harvestpy sidecar deps
	// (uv, harvestpy) above are warnings, not failures — the fleet engine
	// runs without them, and `--skip-harvest` is pfm's own decision not to
	// provision them. `doctor: failures=` must never appear here.
	if strings.Contains(output, "doctor: failures=") {
		h.t.Fatalf(
			"doctor after --skip-harvest printed a failures= line for warnings-only rows; stdout=%q stderr=%q",
			result.stdout,
			result.stderr,
		)
	}
}

//go:build e2e

package e2e

import "strings"

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

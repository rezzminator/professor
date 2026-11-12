//go:build journey

package journey

import (
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// installedServices is what pfm's install leaves loaded: its name-sync and schedule agents.
const installedServices = "com.professor.pfm.name-sync\ncom.professor.pfm.schedule"

// services is pfm's launchd agents as launchctl lists them for the owner, one label a line.
func services(c *harness.Ctx) string {
	out := read(c, "launchctl", "list")
	var labels []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && strings.HasPrefix(f[2], "com.professor.pfm.") {
			labels = append(labels, f[2])
		}
	}
	slices.Sort(labels)
	return strings.Join(labels, "\n")
}

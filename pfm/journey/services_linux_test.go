//go:build journey

package journey

import (
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// installedServices is what pfm's install and its theme on leave running: their path and timer units,
// active.
const installedServices = "pfm-name-sync.path active\npfm-name-sync.timer active\npfm-schedule.timer active\n" +
	"pfm-theme.path active\npfm-theme.timer active"

// services is pfm's units as the owner's systemd user manager lists them, "{unit} {active state}" a line,
// sorted; the services a path or timer starts on demand are left out, as their state is only a moment's.
func services(c *harness.Ctx) string {
	out := read(c, "systemctl", "--user", "list-units", "--all", "--no-legend", "--plain", "pfm*")
	var units []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasSuffix(f[0], ".service") {
			units = append(units, f[0]+" "+f[2])
		}
	}
	slices.Sort(units)
	return strings.Join(units, "\n")
}

// pausedService is the unit pfmRestart stops before its plan, as pfm restart names it; pausedBefore is the
// state the plan reads it in.
const (
	pausedService = "systemd pfm-name-sync.timer"
	pausedBefore  = "ActiveState=inactive"
)

// pausedServices is services while pausedService is stopped: listed, inactive.
var pausedServices = strings.Replace(installedServices, "pfm-name-sync.timer active", "pfm-name-sync.timer inactive", 1)

// pauseService stops pausedService the way the owner does.
func pauseService(c *harness.Ctx) { read(c, "systemctl", "--user", "stop", "pfm-name-sync.timer") }

// resumeService starts pausedService again.
func resumeService(c *harness.Ctx) { read(c, "systemctl", "--user", "start", "pfm-name-sync.timer") }

// serviceStarts is when each of pfm's units last became active, systemd's Id and monotonic
// ActiveEnterTimestamp per unit: a unit an install restarted reads a later time.
func serviceStarts(c *harness.Ctx) string {
	argv := []string{"systemctl", "--user", "show", "--property=Id,ActiveEnterTimestampMonotonic"}
	for _, line := range strings.Split(services(c), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			argv = append(argv, f[0])
		}
	}
	return strings.TrimSpace(read(c, argv...))
}

// serviceDefinitions is every unit file pfm's units run from, "== {path}" then its bytes.
func serviceDefinitions(c *harness.Ctx) string {
	return read(c, "sh", "-c", `for f in "$HOME"/.config/systemd/user/pfm-*; do printf '== %s\n' "$f"; cat "$f"; done`)
}

// jobsRunning is the name-sync and schedule jobs that are mid-run, "" when both are at rest: an install
// started beside a running one is refused, so a step that installs waits for "" first.
func jobsRunning(c *harness.Ctx) string {
	var running []string
	for _, unit := range []string{"pfm-name-sync.service", "pfm-schedule.service"} {
		state := strings.TrimSpace(read(c, "systemctl", "--user", "show", "--property=ActiveState", "--value", unit))
		if state != "inactive" && state != "failed" {
			running = append(running, unit+" "+state)
		}
	}
	return strings.Join(running, "\n")
}

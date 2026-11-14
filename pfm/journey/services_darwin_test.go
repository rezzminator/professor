//go:build journey

package journey

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// installedServices is what pfm's install and its theme on leave loaded: the name-sync, schedule and
// theme follower agents.
const installedServices = "com.professor.pfm.name-sync\ncom.professor.pfm.schedule\ncom.professor.pfm.theme"

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

// pausedService is the agent pfmRestart stops before its plan, as pfm restart names it; pausedBefore is the
// state the plan reads it in.
const (
	pausedService = "launchd com.professor.pfm.name-sync"
	pausedBefore  = "not loaded"
)

// pausedServices is services while pausedService is booted out: no longer listed.
var pausedServices = strings.Replace(installedServices, "com.professor.pfm.name-sync\n", "", 1)

// pauseService boots pausedService out the way the owner does; resumeService bootstraps it again, and
// leaves an agent that is already loaded as it is (launchctl refuses a second bootstrap).
func pauseService(c *harness.Ctx) {
	read(c, "sh", "-c", `launchctl bootout "gui/$(id -u)/com.professor.pfm.name-sync"`)
}

func resumeService(c *harness.Ctx) {
	read(c, "sh", "-c", `launchctl print "gui/$(id -u)/com.professor.pfm.name-sync" >/dev/null 2>&1 ||
launchctl bootstrap "gui/$(id -u)" "$1"`, "sh",
		filepath.Join(ownerHome, "Library", "LaunchAgents", "com.professor.pfm.name-sync.plist"))
}

// serviceStarts is each of pfm's agents with the pid launchctl lists for it ("-" while it is not
// running): an agent an install booted out and bootstrapped again runs under a new pid.
func serviceStarts(c *harness.Ctx) string {
	var rows []string
	for _, line := range strings.Split(read(c, "launchctl", "list"), "\n") {
		if f := strings.Fields(line); len(f) == 3 && strings.HasPrefix(f[2], "com.professor.pfm.") {
			rows = append(rows, f[0]+" "+f[2])
		}
	}
	slices.Sort(rows)
	return strings.Join(rows, "\n")
}

// serviceDefinitions is every plist pfm's agents run from, "== {path}" then its bytes, and the digest of
// the pfm-agent they exec; every plist's ProgramArguments[0] must be that pfm-agent, never pfm itself,
// or each rebuild of pfm re-registers the login items.
func serviceDefinitions(c *harness.Ctx) string {
	agent := filepath.Join(ownerHome, ".local", "libexec", "pfm", "pfm-agent")
	plists := strings.Fields(read(c, "sh", "-c", `ls "$HOME"/Library/LaunchAgents/com.professor.pfm.*.plist`))
	for _, plist := range plists {
		got := strings.TrimSpace(read(c, "plutil", "-extract", "ProgramArguments.0", "raw", "-o", "-", plist))
		if got != agent {
			c.Failf("%s ProgramArguments[0] = %q, want pfm's agent %q", plist, got, agent)
		}
	}
	return read(c, "sh", "-c", `for f in "$HOME"/Library/LaunchAgents/com.professor.pfm.*.plist; do
printf '== %s\n' "$f"; cat "$f"; done; shasum -a 256 "$1"`, "sh", agent)
}

// jobsRunning is the name-sync and schedule jobs that are mid-run, "" when both are at rest: an install
// started beside a running one is refused, so a step that installs waits for "" first.
func jobsRunning(c *harness.Ctx) string {
	var running []string
	for _, label := range []string{"com.professor.pfm.name-sync", "com.professor.pfm.schedule"} {
		out := read(c, "sh", "-c", `launchctl print "gui/$(id -u)/$1" 2>/dev/null || true`, "sh", label)
		if strings.Contains(out, "state = running") {
			running = append(running, label+" running")
		}
	}
	return strings.Join(running, "\n")
}

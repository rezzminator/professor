package themesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Outside macOS the follower is a systemd user unit trio in the directory
// every pfm user unit lives in (the installer's pfm-name-sync and
// pfm-schedule units): a oneshot service running `pfm theme sync`, a path
// unit starting it when what moves the source changes, and a timer starting
// it at load and a minute after each run. `pfm theme` writes and removes them
// itself, as it does the macOS launch agent; install never stages them.
const (
	systemctlBinary   = "systemctl"
	followerUnitStem  = "pfm-theme"
	followerService   = followerUnitStem + ".service"
	followerPathUnit  = followerUnitStem + ".path"
	followerTimerUnit = followerUnitStem + ".timer"
)

// followerTriggers are the units that start the service: enable, restart,
// disable and is-active act on them.
var followerTriggers = []string{followerPathUnit, followerTimerUnit}

// errFollowerUnitsReloadDue is the read-back while a rewritten unit still owes
// the user manager its reload: the running units hold the old definition.
var errFollowerUnitsReloadDue = errors.New(
	"themesync: systemd units rewritten but the user manager was not reloaded: run `pfm theme sync` in a shell",
)

// followerUnit is one unit file and the bytes it must hold.
type followerUnit struct {
	path, content string
}

// systemdExecWord quotes one ExecStart word: systemd expands % specifiers and
// $ variables there, so both are doubled, and a quote or backslash escaped.
func systemdExecWord(word string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(word) + `"`
}

// systemdEnvironment quotes one Environment= assignment, where systemd
// expands % specifiers but no variables.
func systemdEnvironment(name, value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(name+"="+value) + `"`
}

// systemdPath escapes a path setting (PathModified=, StandardOutput=append:),
// whose value is the rest of the line with % specifiers expanded.
func systemdPath(path string) string { return strings.ReplaceAll(path, "%", "%%") }

func (s *session) followerUnitDir() string {
	return filepath.Join(s.env.Home, ".config", "systemd", "user")
}

// systemdFollowerPath is the follower row's file outside macOS: the path unit.
func (s *session) systemdFollowerPath() string {
	return filepath.Join(s.followerUnitDir(), followerPathUnit)
}

// followerUnits renders the trio for the run's pfm, home and state dir. The
// service sets the follower's job label in XPC_SERVICE_NAME, the variable
// launchd sets for the macOS agent, so its sync is the quiet one; PATH is
// paths.ServicePath, the one search path every pfm unit runs on.
func (s *session) followerUnits() ([]followerUnit, error) {
	watches, err := followerWatchPaths(s.env.Home, false)
	if err != nil {
		return nil, err
	}
	logPath := systemdPath(filepath.Join(s.store.dir, "follow.log"))
	header := "# Written by `pfm theme on` and removed by `pfm theme off`; pfm rewrites any edit.\n"
	var watch strings.Builder
	for _, path := range watches {
		watch.WriteString("PathModified=" + systemdPath(path) + "\n")
	}
	service := header + "[Unit]\n" +
		"Description=Follow Claude Code's theme onto every pfm theme surface (pfm theme sync)\n\n" +
		"[Service]\nType=oneshot\n" +
		"Environment=" + systemdEnvironment("PATH", paths.ServicePath(s.env.Home)) + "\n" +
		"Environment=" + systemdEnvironment("XPC_SERVICE_NAME", followerLabel) + "\n" +
		"ExecStart=" + systemdExecWord(s.env.PfmBinary) + " theme sync\n" +
		"StandardOutput=append:" + logPath + "\n" +
		"StandardError=append:" + logPath + "\n"
	pathUnit := header + "[Unit]\n" +
		"Description=Run pfm theme sync when a Claude account's settings.json or themes dir changes\n\n" +
		"[Path]\n" + watch.String() + "Unit=" + followerService + "\n\n" +
		"[Install]\nWantedBy=default.target\n"
	timer := header + "[Unit]\n" +
		"Description=Run pfm theme sync at start and a minute after each run (the backstop for a missed watch)\n\n" +
		"[Timer]\nOnActiveSec=1s\nOnUnitInactiveSec=" + fmt.Sprint(followerInterval) + "s\nAccuracySec=1s\n" +
		"Unit=" + followerService + "\n\n" +
		"[Install]\nWantedBy=timers.target\n"
	dir := s.followerUnitDir()
	return []followerUnit{
		{filepath.Join(dir, followerService), service},
		{filepath.Join(dir, followerPathUnit), pathUnit},
		{filepath.Join(dir, followerTimerUnit), timer},
	}, nil
}

// systemctl runs one `systemctl --user` verb; a run that could not start or
// exited non-zero is its error, naming the command and systemd's words.
func (s *session) systemctl(ctx context.Context, args ...string) error {
	argv := append([]string{systemctlBinary, "--user"}, args...)
	result, err := s.env.Runner.Run(ctx, argv, deps.RunOptions{})
	if err != nil {
		return fmt.Errorf("themesync: %s: %w", strings.Join(argv, " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("themesync: %s exited %d: %s",
			strings.Join(argv, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

// followerTriggersActive asks the user manager for each trigger's state:
// whether both are active, and the ones that are not with their state. A
// manager that answered no state per unit is an error, never "inactive".
func (s *session) followerTriggersActive(ctx context.Context) (bool, string, error) {
	argv := append([]string{systemctlBinary, "--user", "is-active"}, followerTriggers...)
	result, err := s.env.Runner.Run(ctx, argv, deps.RunOptions{})
	if err != nil {
		return false, "", fmt.Errorf("themesync: %s: %w", strings.Join(argv, " "), err)
	}
	states := strings.Fields(string(result.Stdout))
	if len(states) != len(followerTriggers) {
		return false, "", fmt.Errorf("themesync: %s exited %d printing %q: %s", strings.Join(argv, " "),
			result.ExitCode, strings.TrimSpace(string(result.Stdout)), strings.TrimSpace(string(result.Stderr)))
	}
	var inactive []string
	for index, state := range states {
		if state != "active" {
			inactive = append(inactive, followerTriggers[index]+" "+state)
		}
	}
	return len(inactive) == 0, strings.Join(inactive, ", "), nil
}

// removeStrayLaunchAgent deletes the macOS plist a pfm before the Linux
// follower wrote here; launchd never ran it, so there is nothing to boot out.
func (s *session) removeStrayLaunchAgent() error {
	if err := os.Remove(s.followerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("themesync: remove %s: %w", s.followerPath(), err)
	}
	return nil
}

func (s *session) applySystemdFollower(ctx context.Context) SurfaceResult {
	path := s.systemdFollowerPath()
	if s.env.PfmBinary == "" {
		return s.failed(
			surfaceFollower,
			path,
			fmt.Errorf("themesync: Env.PfmBinary is empty; the follower cannot run pfm"),
		)
	}
	if err := s.removeStrayLaunchAgent(); err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	units, err := s.followerUnits()
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	changed := false
	for _, unit := range units {
		current, err := os.ReadFile(unit.path)
		if err == nil && string(current) == unit.content {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return s.failed(surfaceFollower, path, fmt.Errorf("themesync: read %s: %w", unit.path, err))
		}
		if err := atomicfile.Write(unit.path, []byte(unit.content), 0o644); err != nil {
			return s.failed(surfaceFollower, path, fmt.Errorf("themesync: write %s: %w", unit.path, err))
		}
		changed = true
	}
	if changed {
		s.setDue(surfaceFollower, true) // owed until a restart succeeds
	}
	active, _, err := s.followerTriggersActive(ctx)
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	if active && !s.reloadDue(surfaceFollower, changed) {
		return s.result(surfaceFollower, VerdictApplied, path, "")
	}
	// A restart from inside the follower's own service is safe: the oneshot
	// run is not one of the units it restarts, and the timer's start fires
	// into the run still active, so no second sync starts.
	for _, args := range [][]string{
		{"daemon-reload"},
		append([]string{"enable"}, followerTriggers...),
		append([]string{"restart"}, followerTriggers...),
	} {
		if err := s.systemctl(ctx, args...); err != nil {
			return s.settle(surfaceFollower, path, "", err)
		}
	}
	return s.settle(surfaceFollower, path, "", nil)
}

func (s *session) readSystemdFollower(ctx context.Context) SurfaceResult {
	path := s.systemdFollowerPath()
	units, err := s.followerUnits()
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	for _, unit := range units {
		current, err := os.ReadFile(unit.path)
		name := filepath.Base(unit.path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return s.in(surfaceFollower, VerdictDrift, path, "systemd unit "+name+" is missing", 0)
		case err != nil:
			return s.failed(surfaceFollower, path, fmt.Errorf("themesync: read %s: %w", unit.path, err))
		case string(current) != unit.content:
			return s.in(surfaceFollower, VerdictDrift, path, "systemd unit "+name+" differs from the render", 0)
		}
	}
	active, inactive, err := s.followerTriggersActive(ctx)
	switch {
	case err != nil:
		return s.failed(surfaceFollower, path, err)
	case !active:
		return s.in(surfaceFollower, VerdictDrift, path, "systemd units not active: "+inactive, 0)
	case s.state.Due[surfaceFollower]:
		return s.failed(surfaceFollower, path, errFollowerUnitsReloadDue)
	}
	return s.in(surfaceFollower, VerdictInSync, path, "", 0)
}

// restoreSystemdFollower disables the triggers whose unit files are present,
// deletes the trio and reloads the manager; with no unit file present nothing
// was installed, and the user manager is not asked.
func (s *session) restoreSystemdFollower(ctx context.Context) SurfaceResult {
	path := s.systemdFollowerPath()
	if err := s.removeStrayLaunchAgent(); err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	// Removal needs only the installed names, never account discovery.
	var units []followerUnit
	for _, name := range []string{followerService, followerPathUnit, followerTimerUnit} {
		units = append(units, followerUnit{path: filepath.Join(s.followerUnitDir(), name)})
	}
	existed := false
	present := map[string]bool{}
	for _, unit := range units {
		if _, err := os.Lstat(unit.path); err == nil {
			existed = true
			present[filepath.Base(unit.path)] = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return s.failed(surfaceFollower, path, fmt.Errorf("themesync: stat %s: %w", unit.path, err))
		}
	}
	if !existed {
		return s.absent(surfaceFollower, path, "no systemd units installed")
	}
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	// systemctl refuses the whole disable when one named unit has no file,
	// so a trio missing a trigger (a hand-deleted unit, an apply cut short)
	// disables only the triggers still there, and is never stuck on.
	disable := []string{"disable", "--now"}
	for _, trigger := range followerTriggers {
		if present[trigger] {
			disable = append(disable, trigger)
		}
	}
	if len(disable) > 2 {
		if err := s.systemctl(ctx, disable...); err != nil {
			return s.failed(surfaceFollower, path, err)
		}
	}
	for _, unit := range units {
		if err := os.Remove(unit.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return s.failed(surfaceFollower, path, fmt.Errorf("themesync: remove %s: %w", unit.path, err))
		}
	}
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	return s.result(surfaceFollower, VerdictRestored, path, "")
}

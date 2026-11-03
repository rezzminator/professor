package restart

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

const (
	// serviceStopWithin bounds the wait for a stopped service to read stopped.
	serviceStopWithin = 5 * time.Second
	// serviceStartWithin bounds the wait for a started service to read up.
	serviceStartWithin = 15 * time.Second
	// launchdBootstrapAttempts spreads a launch agent's bootstrap attempts over
	// serviceStopWithin, one per servicemanager.BootstrapRetryInterval, the
	// first at once.
	launchdBootstrapAttempts = int(serviceStopWithin/servicemanager.BootstrapRetryInterval) + 1
	// launchdLoaded is the word for a launch agent launchd knows.
	launchdLoaded = "loaded"
)

// ServiceManager finds the user services under this host's service manager
// whose installed file name match accepts, as units that stop and start
// through that manager.
type ServiceManager interface {
	Units(ctx context.Context, family string, match func(name string) bool, verify Verifier) (
		units []Unit, detail string, err error)
}

// nativeServiceManager is the manager pfm installs its services under for
// kind: launchd per-user agents in the gui/<uid> domain, or systemd user
// units; nil for a kind pfm installs none under.
func nativeServiceManager(
	kind servicemanager.Kind,
	runner deps.Runner,
	clk clock.Clock,
	home string,
	uid int,
) ServiceManager {
	switch kind {
	case servicemanager.KindLaunchd:
		return NewLaunchdManager(runner, clk, home, uid)
	case servicemanager.KindSystemd:
		return NewSystemdManager(runner, clk, home)
	}
	return nil
}

// Verifier returns one more proof that the named unit is up after its manager
// says it is, or nil when the manager's word is the whole proof.
type Verifier func(name string) func(context.Context) error

// check is the verifier's probe for name; a nil verifier has none.
func (verify Verifier) check(name string) func(context.Context) error {
	if verify == nil {
		return nil
	}
	return verify(name)
}

// serviceTools is what both managers run on: the command runner and the clock
// every bounded wait polls on.
type serviceTools struct {
	runner deps.Runner
	clock  clock.Clock
}

// waitFor polls check until it answers true, fails, or within elapses.
func (tools serviceTools) waitFor(ctx context.Context, within time.Duration, check func() (bool, error)) (bool, error) {
	deadline := tools.clock.Now().Add(within)
	for {
		done, err := check()
		if err != nil || done {
			return done, err
		}
		if !tools.clock.Now().Before(deadline) {
			return false, nil
		}
		if err := tools.pause(ctx, pollEvery); err != nil {
			return false, err
		}
	}
}

// pause waits d on the clock every bounded wait polls on.
func (tools serviceTools) pause(ctx context.Context, d time.Duration) error {
	if err := tools.clock.Sleep(ctx, d); err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	return nil
}

// missing reports whether name is not on PATH; any other lookup failure is
// an error, never "not installed".
func (tools serviceTools) missing(name string) (bool, error) {
	_, err := tools.runner.LookPath(name)
	if errors.Is(err, exec.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", name, err)
	}
	return false, nil
}

// ran is a service-manager command's result; a command that failed to run at
// all is the error, naming it.
func ran(answer servicemanager.Answer) (deps.RunResult, error) {
	if answer.RunErr != nil {
		return answer.Result, fmt.Errorf("%s: %w", answer.Command(), answer.RunErr)
	}
	return answer.Result, nil
}

// installedNames lists the file names in dir that match accepts. A missing
// dir is no services installed; any other read failure is an error.
func installedNames(dir string, match func(string) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && match(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// unreadableUnit is a unit whose state could not be read. It stays in the plan:
// its stop step fails naming why, so the restart reports it and exits 1, and
// every other unit is still restarted. It is never started, and never read as
// stopped.
func unreadableUnit(family, name, foundBy string, cause error) Unit {
	return Unit{
		Family:  family,
		Name:    name,
		FoundBy: foundBy,
		Before:  "unreadable",
		StopHow: "not attempted: its state could not be read: " + stepLine(cause.Error()),
		Stop:    func(context.Context) (string, error) { return "", fmt.Errorf("state unreadable: %w", cause) },
	}
}

// LaunchdManager supervises per-user launch agents in the gui/<uid> domain.
type LaunchdManager struct {
	serviceTools
	Dir string
	UID int
}

// NewLaunchdManager is the macOS manager over home's LaunchAgents directory.
func NewLaunchdManager(runner deps.Runner, clk clock.Clock, home string, uid int) LaunchdManager {
	return LaunchdManager{
		serviceTools: serviceTools{runner: runner, clock: clk},
		Dir:          paths.LaunchAgentsDir(home),
		UID:          uid,
	}
}

// Units lists every matching plist as a launch agent unit. The MCP daemon,
// which every other agent's chats reach, is ordered last so it starts first.
// An agent is up when launchd has it loaded — a job launchd runs or schedules,
// a KeepAlive one whatever its state word; one not loaded stays stopped.
func (manager LaunchdManager) Units(
	ctx context.Context, family string, match func(string) bool, verify Verifier,
) ([]Unit, string, error) {
	if absent, err := manager.missing(servicemanager.LaunchctlBinary); absent || err != nil {
		return nil, "no launchctl on this host — no launch agents to restart", err
	}
	names, err := installedNames(manager.Dir, func(name string) bool {
		return strings.HasSuffix(name, ".plist") && match(strings.TrimSuffix(name, ".plist"))
	})
	if err != nil {
		return nil, "", err
	}
	var units, daemons []Unit
	for _, name := range names {
		label := strings.TrimSuffix(name, ".plist")
		path := filepath.Join(manager.Dir, name)
		loaded, body, err := manager.read(ctx, label, path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", err
			}
			units = append(units, unreadableUnit(family, "launchd "+label, "plist "+path, err))
			continue
		}
		keepAlive := launchdKeepAlive(body)
		unit := manager.unit(family, label, path, loaded, keepAlive, verify.check(label))
		if keepAlive {
			daemons = append(daemons, unit)
		} else {
			units = append(units, unit)
		}
	}
	return append(units, daemons...), fmt.Sprintf(
		"%d launch agent(s) — %s, %s domain", len(names), manager.Dir, servicemanager.Domain(manager.UID)), nil
}

// launchdKeepAlive reports whether a plist keeps its job alive: such a job is
// up only when launchd says it is running, not merely loaded.
func launchdKeepAlive(plist string) bool {
	_, after, found := strings.Cut(plist, "<key>KeepAlive</key>")
	return found && strings.HasPrefix(strings.TrimSpace(after), "<true/>")
}

// launchd is the manager's gui/<uid> domain, pausing between bootstrap
// attempts on the manager's clock.
func (manager LaunchdManager) launchd() servicemanager.Launchd {
	return servicemanager.Launchd{Runner: manager.runner, UID: manager.UID, Pause: manager.pause}
}

// state asks launchd about label: whether it is loaded, and its state word.
// A label launchd does not know is not loaded; any other non-answer is an error.
func (manager LaunchdManager) state(ctx context.Context, label string) (loaded bool, state string, err error) {
	answer := manager.launchd().Print(ctx, label)
	result, err := ran(answer)
	if err != nil {
		return false, "", err
	}
	if result.ExitCode != 0 {
		if answer.Unknown() {
			return false, "", nil
		}
		return false, "", fmt.Errorf("%s: exit %d: %s",
			answer.Command(), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return true, answer.State(), nil
}

// read is one launch agent's state: whether launchd has label loaded, and its
// plist's text.
func (manager LaunchdManager) read(ctx context.Context, label, path string) (bool, string, error) {
	loaded, err := manager.loaded(ctx, label)
	if err != nil {
		return false, "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false, "", fmt.Errorf("read launch agent %s: %w", path, err)
	}
	return loaded, string(body), nil
}

func (manager LaunchdManager) loaded(ctx context.Context, label string) (bool, error) {
	loaded, _, err := manager.state(ctx, label)
	return loaded, err
}

func (manager LaunchdManager) unit(
	family, label, path string, loaded, keepAlive bool, verify func(context.Context) error,
) Unit {
	upWord := launchdLoaded
	if keepAlive {
		upWord = "loaded and state = running"
	}
	if verify != nil {
		upWord += ", then its own health probe"
	}
	before := "not loaded"
	if loaded {
		before = launchdLoaded
	}
	target := manager.launchd().Target(label)
	unit := Unit{
		Family:   family,
		Name:     "launchd " + label,
		FoundBy:  "plist " + path + ", " + before,
		Before:   before,
		StopHow:  "launchctl bootout " + target + "; verified: launchctl print no longer knows it",
		StartHow: "launchctl bootstrap " + servicemanager.Domain(manager.UID) + " " + path + "; verified: " + upWord,
		Stop: func(ctx context.Context) (string, error) {
			return manager.stop(ctx, label)
		},
		Start: func(ctx context.Context) (string, error) {
			return manager.start(ctx, label, path, keepAlive, verify)
		},
	}
	if !loaded {
		unit.StaysStopped, unit.Stop, unit.Start = true, nil, nil
	}
	return unit
}

func (manager LaunchdManager) stop(ctx context.Context, label string) (string, error) {
	loaded, err := manager.loaded(ctx, label)
	if err != nil {
		return "", err
	}
	if !loaded {
		return "not loaded — launchctl print does not know it", nil
	}
	launchd := manager.launchd()
	result, err := ran(launchd.Run(ctx, "bootout", launchd.Target(label)))
	if err != nil {
		return "", err
	}
	gone, err := manager.waitFor(ctx, serviceStopWithin, func() (bool, error) {
		loaded, err := manager.loaded(ctx, label)
		return !loaded, err
	})
	if err != nil {
		return "", err
	}
	if !gone {
		return "", fmt.Errorf("still loaded %s after launchctl bootout (exit %d: %s)",
			serviceStopWithin, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return "booted out; launchctl print no longer knows it", nil
}

func (manager LaunchdManager) start(
	ctx context.Context, label, path string, keepAlive bool, verify func(context.Context) error,
) (string, error) {
	// A bootout's teardown can still be in flight: bootstrap retries until the
	// label reads loaded, whatever each bootstrap's exit.
	loadedNow := func(ctx context.Context) (bool, error) { return manager.loaded(ctx, label) }
	last, loaded, err := manager.launchd().Bootstrap(ctx, path, launchdBootstrapAttempts, loadedNow)
	if err != nil {
		if last.RunErr != nil {
			_, err = ran(last)
		}
		return "", err
	}
	if !loaded {
		return "", fmt.Errorf("not loaded after launchctl bootstrap %s (exit %d: %s)",
			path, last.Result.ExitCode, strings.TrimSpace(string(last.Result.Stderr)))
	}
	detail := launchdLoaded
	if keepAlive {
		var state string
		running, err := manager.waitFor(ctx, serviceStartWithin, func() (bool, error) {
			_, current, err := manager.state(ctx, label)
			state = current
			return current == "running", err
		})
		if err != nil {
			return "", err
		}
		if !running {
			return "", fmt.Errorf("loaded but state = %q, not running, %s after bootstrap", state, serviceStartWithin)
		}
		detail = "loaded, state = running"
	}
	if verify != nil {
		if err := manager.probeUp(ctx, verify); err != nil {
			return "", fmt.Errorf("%s, but its health probe failed: %w", detail, err)
		}
		detail += ", health probe answered"
	}
	return detail, nil
}

// probeUp polls a started service's health probe until it answers or
// serviceStartWithin elapses: a daemon its manager reads up may not serve yet.
func (tools serviceTools) probeUp(ctx context.Context, probe func(context.Context) error) error {
	var last error
	up, err := tools.waitFor(ctx, serviceStartWithin, func() (bool, error) {
		last = probe(ctx)
		return last == nil, nil
	})
	if err != nil {
		return err
	}
	if !up {
		return fmt.Errorf("%w, still %s after start", last, serviceStartWithin)
	}
	return nil
}

// SystemdManager supervises systemd user units.
type SystemdManager struct {
	serviceTools
	Dir string
}

// NewSystemdManager is the Linux manager over home's systemd user unit directory.
func NewSystemdManager(runner deps.Runner, clk clock.Clock, home string) SystemdManager {
	return SystemdManager{
		serviceTools: serviceTools{runner: runner, clock: clk},
		Dir:          paths.SystemdUserDir(home),
	}
}

// Units lists every matching unit file, every ActiveState read before any
// unit is built. A service a .timer or .path of the same name triggers, and
// that trigger was up, stops but is not started directly: its trigger starts
// it. One whose triggers all stay stopped starts directly.
// Order: triggers, triggered services, then standalone services (the MCP
// daemon), so start brings the standalone ones up first.
func (manager SystemdManager) Units(
	ctx context.Context, family string, match func(string) bool, verify Verifier,
) ([]Unit, string, error) {
	if absent, err := manager.missing(servicemanager.SystemctlBinary); absent || err != nil {
		return nil, "no systemctl on this host — no user units to restart", err
	}
	names, err := installedNames(manager.Dir, func(name string) bool {
		ext := filepath.Ext(name)
		return (ext == ".service" || ext == ".timer" || ext == ".path") && match(name)
	})
	if err != nil {
		return nil, "", err
	}
	installed := map[string]bool{}
	for _, name := range names {
		installed[name] = true
	}
	states, up, unreadable := map[string]string{}, map[string]bool{}, map[string]error{}
	for _, name := range names {
		state, err := manager.activeState(ctx, name)
		if err == nil {
			up[name], err = systemdUp(name, state)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", err
			}
			unreadable[name] = err
			continue
		}
		states[name] = state
	}
	var triggers, triggered, standalone []Unit
	for _, name := range names {
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		// A service's restart path hangs on its own state and its triggers': one
		// unread among them leaves the service unreadable too.
		watched := []string{name}
		if filepath.Ext(name) == ".service" {
			watched = append(watched, stem+".timer", stem+".path")
		}
		var cause error
		for _, watch := range watched {
			if cause == nil {
				cause = unreadable[watch]
			}
		}
		if cause != nil {
			unit := unreadableUnit(family, "systemd "+name, filepath.Join(manager.Dir, name), cause)
			if filepath.Ext(name) != ".service" {
				triggers = append(triggers, unit)
			} else {
				standalone = append(standalone, unit)
			}
			continue
		}
		switch {
		case filepath.Ext(name) != ".service":
			triggers = append(triggers, manager.unit(family, name, states[name], up[name], "", verify.check(name)))
		case installed[stem+".timer"] || installed[stem+".path"]:
			trigger := ""
			if up[stem+".timer"] || up[stem+".path"] {
				trigger = "its timer or path unit starts it again"
			}
			triggered = append(
				triggered,
				manager.unit(family, name, states[name], up[name], trigger, verify.check(name)),
			)
		default:
			standalone = append(standalone, manager.unit(family, name, states[name], up[name], "", verify.check(name)))
		}
	}
	units := append(append(triggers, triggered...), standalone...)
	return units, fmt.Sprintf("%d user unit(s) — %s, systemctl --user", len(names), manager.Dir), nil
}

// activeState is the unit's ActiveState under systemd --user.
func (manager SystemdManager) activeState(ctx context.Context, unit string) (string, error) {
	show := servicemanager.UserSystemctl(ctx, manager.runner, "show", "--property=ActiveState", "--value", unit)
	result, err := ran(show)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("systemctl --user show %s: exit %d: %s",
			unit, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	state := strings.TrimSpace(string(result.Stdout))
	if state == "" {
		return "", fmt.Errorf("systemctl --user show %s answered no ActiveState", unit)
	}
	return state, nil
}

// systemdUp reads an ActiveState word as up or not: active, activating,
// reloading and refreshing are up; inactive, failed, deactivating and
// maintenance are not. Any other word is a failed read, never "stopped".
func systemdUp(name, state string) (bool, error) {
	switch state {
	case "active", "activating", "reloading", "refreshing":
		return true, nil
	case "inactive", "failed", "deactivating", "maintenance":
		return false, nil
	}
	return false, fmt.Errorf("systemctl --user show %s: unknown ActiveState %q", name, state)
}

// unit is one user unit. One not up stays stopped. One up whose trigger was
// up too (trigger names how it comes back) is stopped and left to it; any
// other up unit stops and starts.
func (manager SystemdManager) unit(
	family, name, state string, up bool, trigger string, verify func(context.Context) error,
) Unit {
	unit := Unit{
		Family:  family,
		Name:    "systemd " + name,
		FoundBy: filepath.Join(manager.Dir, name) + ", ActiveState=" + state,
		StopHow: "systemctl --user stop " + name + "; verified: ActiveState inactive or failed",
		Stop:    func(ctx context.Context) (string, error) { return manager.stop(ctx, name) },
		Before:  "ActiveState=" + state,
	}
	if !up {
		unit.StaysStopped, unit.Stop = true, nil
		return unit
	}
	if trigger != "" {
		unit.StopHow += " (" + trigger + ")"
		return unit
	}
	unit.StartHow = "systemctl --user start " + name + "; verified: ActiveState active"
	if verify != nil {
		unit.StartHow += ", then its own health probe"
	}
	unit.Start = func(ctx context.Context) (string, error) { return manager.start(ctx, name, verify) }
	return unit
}

func (manager SystemdManager) stop(ctx context.Context, name string) (string, error) {
	result, err := ran(servicemanager.UserSystemctl(ctx, manager.runner, "stop", name))
	if err != nil {
		return "", err
	}
	var state string
	stopped, err := manager.waitFor(ctx, serviceStopWithin, func() (bool, error) {
		current, err := manager.activeState(ctx, name)
		state = current
		return current == "inactive" || current == "failed", err
	})
	if err != nil {
		return "", err
	}
	if !stopped {
		return "", fmt.Errorf("ActiveState=%s %s after systemctl --user stop (exit %d: %s)",
			state, serviceStopWithin, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return "ActiveState=" + state, nil
}

func (manager SystemdManager) start(
	ctx context.Context,
	name string,
	verify func(context.Context) error,
) (string, error) {
	result, err := ran(servicemanager.UserSystemctl(ctx, manager.runner, "start", name))
	if err != nil {
		return "", err
	}
	var state string
	active, err := manager.waitFor(ctx, serviceStartWithin, func() (bool, error) {
		current, err := manager.activeState(ctx, name)
		state = current
		return current == "active", err
	})
	if err != nil {
		return "", err
	}
	if !active {
		return "", fmt.Errorf("ActiveState=%s %s after systemctl --user start (exit %d: %s) — journalctl --user -u %s",
			state, serviceStartWithin, result.ExitCode, strings.TrimSpace(string(result.Stderr)), name)
	}
	if verify != nil {
		if err := manager.probeUp(ctx, verify); err != nil {
			return "", fmt.Errorf("ActiveState=active, but its health probe failed: %w", err)
		}
		return "ActiveState=active, health probe answered", nil
	}
	return "ActiveState=active", nil
}

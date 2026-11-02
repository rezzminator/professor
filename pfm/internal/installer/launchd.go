package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// launchdLabel is the job's name, and the handle launchctl addresses it by.
const launchdLabel = "com.professor.pfm.name-sync"

const mcpLaunchdLabel = "com.professor.pfm.mcp"

// launchd is the calling user's GUI domain, asked through the install's runner
// and pausing through its own pause.
func (installer *engine) launchd() servicemanager.Launchd {
	pause := func(_ context.Context, d time.Duration) error {
		installer.pause(d)
		return nil
	}
	return servicemanager.NewLaunchd(commandRun{installer.options.Runner}, pause)
}

const launchdAsset = "launchd/" + launchdLabel + ".plist"

const mcpLaunchdAsset = "launchd/" + mcpLaunchdLabel + ".plist"

// launchdLogDir is where both agents' StandardOutPath/StandardErrorPath
// point: __PFM_HOME__/Library/Logs/pfm. launchd starts an agent with whatever
// ancestor directories already exist — it never creates one for a log path —
// so a fresh install without this directory would set the paths only for the
// daemon to silently drop every line of stdout/stderr.
func (installer *engine) launchdLogDir() string {
	return filepath.Join(installer.options.Home, "Library", "Logs", "pfm")
}

// ensureLaunchdLogDir stages ~/Library/Logs/pfm before either launch agent is
// loaded, reported the same way every other planned installer step is: "ok"
// when it already exists, a "change" (created only in apply mode, planned in
// dry run) when it does not.
func (installer *engine) ensureLaunchdLogDir() error {
	path := installer.launchdLogDir()
	if _, err := os.Stat(path); err == nil {
		installer.ok(path)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat launchd log dir %s: %w", path, err)
	}
	return installer.change("create "+path, func() error {
		return os.MkdirAll(path, 0o755)
	})
}

// launchAgentPath returns where macOS expects a per-user agent to live.
func (installer *engine) launchAgentPath() string {
	return filepath.Join(paths.LaunchAgentsDir(installer.options.Home), launchdLabel+".plist")
}

func (installer *engine) mcpLaunchAgentPath() string {
	return filepath.Join(paths.LaunchAgentsDir(installer.options.Home), mcpLaunchdLabel+".plist")
}

// wireLaunchAgent installs the macOS half of the name-sync scheduler.
//
// The plist is written as a REAL FILE, not a symlink into the managed asset
// root the way every other installed asset is. launchd refuses to load an agent
// through a symlink, and the failure is silent — the job simply never runs — so
// the one place the managed-asset pattern is broken is the one place breaking
// it is required.
func (installer *engine) wireLaunchAgent(ctx context.Context) error {
	path := installer.launchAgentPath()
	installer.say("launchd user agent -> %s", path)
	if err := installer.ensureAgentLauncher(); err != nil {
		return err
	}

	template, err := readAsset(launchdAsset)
	if err != nil {
		return fmt.Errorf("read embedded launch agent: %w", err)
	}
	// launchd has no %h, so the home is substituted here rather than expanded
	// at load time. The poll comes from the machine config's nameSync.interval
	// through the same renderer the systemd timer uses, so the two schedulers
	// cannot drift.
	wanted, err := renderLaunchAgentTemplate(template, installer.options.Home)
	if err == nil {
		wanted, err = renderNameSyncLaunchAgent(wanted, installer.options)
	}
	if err == nil {
		wanted, err = renderServiceEnv(wanted, installer.options.Home)
	}
	if err != nil {
		return fmt.Errorf("render launch agent: %w", err)
	}
	installer.say("  %s", nameSyncScheduleSummary(installer.options))

	plistChanged := false
	if sameFile(path, wanted, 0o644) {
		installer.ok(path)
	} else {
		if err := installer.change("write "+path, func() error {
			if _, statErr := os.Lstat(path); statErr == nil {
				backup := availableBackup(path, installer.stamp)
				if err := copyBackup(path, backup); err != nil {
					return err
				}
			}
			return atomicfile.Write(path, wanted, 0o644)
		}); err != nil {
			return err
		}
		plistChanged = true
	}
	if !installer.apply {
		installer.say("")
		return nil
	}
	return installer.reloadLaunchAgent(ctx, path, plistChanged)
}

func (installer *engine) wireMCPLaunchAgent(ctx context.Context) error {
	path := installer.mcpLaunchAgentPath()
	if !installer.options.MCPEnabled[chatName] {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		message := fmt.Sprintf("remove %s (no MCP server is enabled in %s)", path, installer.options.MCPConfigPath)
		return installer.retireLaunchAgent(ctx, path, mcpLaunchdLabel, message)
	}
	template, err := readAsset(mcpLaunchdAsset)
	if err != nil {
		return fmt.Errorf("read embedded MCP launch agent: %w", err)
	}
	if err := installer.ensureAgentLauncher(); err != nil {
		return err
	}
	wanted, err := renderLaunchAgentTemplate(template, installer.options.Home)
	if err == nil {
		wanted, err = renderServiceEnv(wanted, installer.options.Home)
	}
	if err != nil {
		return fmt.Errorf("render MCP launch agent: %w", err)
	}
	plistChanged := false
	if !sameFile(path, wanted, 0o644) {
		if err := installer.change("write "+path, func() error {
			if _, statErr := os.Lstat(path); statErr == nil {
				if err := copyBackup(path, availableBackup(path, installer.stamp)); err != nil {
					return err
				}
			}
			return atomicfile.Write(path, wanted, 0o644)
		}); err != nil {
			return err
		}
		plistChanged = true
	} else {
		installer.ok(path)
	}
	if !installer.apply {
		return nil
	}
	return installer.reloadLaunchAgentWithLabel(ctx, path, mcpLaunchdLabel, plistChanged)
}

// reloadLaunchAgent re-registers the job so an edited plist takes effect.
func (installer *engine) reloadLaunchAgent(ctx context.Context, path string, plistChanged bool) error {
	return installer.reloadLaunchAgentWithLabel(ctx, path, launchdLabel, plistChanged)
}

// reloadLaunchAgentWithLabel re-registers a job ONLY when re-registering can
// change something.
//
// bootout STOPS the running job. A loaded service whose plist did not move
// gains nothing from a reload and loses every client it was serving, so an
// ordinary install leaves it alone — the difference between an install that
// reconciles files and one that restarts the user's daemons as a side effect.
//
// When the plist DID move, the job is stopped and re-registered, with the
// bootstrap retried while launchd finishes the teardown (bootstrapWithRetry).
// A bootstrap that fails after the job was stopped says so in its own words:
// that is the one outcome where the installer left the host worse than it
// found it, and it must never read like a plain "not loaded".
func (installer *engine) reloadLaunchAgentWithLabel(ctx context.Context, path, label string, plistChanged bool) error {
	launchd := installer.launchd()
	domain := servicemanager.Domain(launchd.UID)
	loaded := launchd.Print(ctx, label).OK()
	if loaded && !plistChanged {
		reason, err := installer.launchAgentStale(ctx, label)
		if err != nil {
			installer.skip(
				"launchctl " + label + " already loaded, plist unchanged — health NOT probed (" + err.Error() +
					"); left as loaded",
			)
			installer.say("")
			return nil
		}
		if reason == "" {
			installer.ok("launchctl " + label + " already loaded, plist unchanged — left running")
			installer.say("")
			return nil
		}
		installer.say("  reload  launchctl %s: %s", label, reason)
	}
	if loaded {
		// A job already gone is what bootout is for, and Bootout answers nil
		// for it. A job it could not unload would only fail the
		// bootstrap below as already loaded, under an error claiming the
		// service is DOWN while its old job still runs.
		if err := launchd.Bootout(ctx, label); err != nil {
			return fmt.Errorf(
				"launchctl %s not reloaded: the new plist at %s is installed, but its loaded job could not be unloaded: %w",
				label,
				path,
				err,
			)
		}
	}
	if err := bootstrapWithRetry(ctx, launchd, path); err != nil {
		if loaded {
			return fmt.Errorf(
				"launchctl bootstrap %s failed AFTER its running job was stopped to load the new plist; the agent file is installed and the service is now DOWN — restart it with `launchctl bootstrap %s %s`: %w",
				label,
				domain,
				path,
				err,
			)
		}
		return fmt.Errorf(
			"launchctl bootstrap %s failed; agent file is installed but service is not loaded: %w",
			label,
			err,
		)
	}
	if err := launchd.Print(ctx, label).Err(); err != nil {
		return fmt.Errorf(
			"launchctl bootstrap %s returned success but loaded-agent verification failed: %w",
			label,
			err,
		)
	}
	installer.ok("launchctl bootstrap " + label)
	installer.say("")
	return nil
}

// launchdSpawnFailed is the exit code launchd records when it could not start
// the job at all (EX_CONFIG); pfm's own exits never use it — a daemon leaving
// for a new binary exits binwatch.ExitReplaced.
const launchdSpawnFailed = 78

// launchAgentStale names why a loaded agent whose plist did not change must
// still be reloaded, or returns "" for a healthy one. Only a wedged job
// qualifies: launchd could not spawn it (`spawn failed`, last exit 78, not
// running). A job running a pfm build the latest install replaced is NOT stale:
// every agent runs through the stable launcher, so a rebuild changes nothing
// launchd or Background Task Management registered, and a reload — a bootout
// and bootstrap — would itself re-register the item. The MCP daemon restarts on
// the new build by itself (binwatch exits it, KeepAlive relaunches it), and the
// interval agents start the new build on their next tick. An error means the
// state could not be read, never "healthy".
func (installer *engine) launchAgentStale(ctx context.Context, label string) (string, error) {
	reader, ok := installer.options.Runner.(OutputRunner)
	if !ok {
		return "", errors.New("the command runner cannot read launchctl output")
	}
	answer := servicemanager.NewLaunchd(commandOutput{reader}, nil).Print(ctx, label)
	if err := answer.Err(); err != nil {
		return "", fmt.Errorf("%s: %w", answer.Command(), err)
	}
	running, spawnFailed := false, false
	for _, line := range strings.Split(string(answer.Result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "state = running":
			running = true
		case line == "job state = spawn failed",
			strings.HasPrefix(line, "last exit code = "+strconv.Itoa(launchdSpawnFailed)+":"):
			spawnFailed = true
		}
	}
	if !running && spawnFailed {
		return "launchd could not spawn it (spawn failed, last exit 78)", nil
	}
	return "", nil
}

// pause waits, through the Options seam when one was supplied. Options is
// documented as directly constructible, so a nil Sleep is a legitimate caller
// state rather than a bug — it falls back rather than panicking.
func (installer *engine) pause(d time.Duration) {
	if installer.options.Sleep != nil {
		installer.options.Sleep(d)
		return
	}
	waiter := installer.options.Clock
	if waiter == nil {
		waiter = clock.Real
	}
	if err := waiter.Sleep(context.Background(), d); err != nil {
		installer.say("installer: launchd retry sleep failed: %v", err)
	}
}

// bootstrapWithRetry re-registers the job through launchd's Bootstrap,
// retrying while launchd finishes a teardown that bootout only REQUESTED.
//
// `launchctl bootout` returns as soon as the request is accepted, not when the
// label is gone, so an immediate bootstrap can fail with EIO against a label
// still on its way out. That is the failure that left a stopped daemon with
// nothing to restart it. Retrying the bootstrap closes the window without
// asking anyone to predict how long a teardown takes, and it costs a healthy
// host nothing: the first attempt succeeds and no wait is ever taken. A
// launchctl that could not run at all ends it at once. The error is the last
// attempt's, verbatim.
func bootstrapWithRetry(ctx context.Context, launchd servicemanager.Launchd, path string) error {
	last, done, err := launchd.Bootstrap(ctx, path, servicemanager.BootstrapAttempts, nil)
	if err != nil || done {
		return err
	}
	return last.Err()
}

// unwireLaunchAgent removes the agent and unloads it.
func (installer *engine) unwireLaunchAgent(ctx context.Context) error {
	path := installer.launchAgentPath()
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		installer.skip("no launch agent at " + path)
		return installer.unwireMCPLaunchAgent(ctx)
	}
	if err := installer.retireLaunchAgent(ctx, path, launchdLabel, "remove "+path); err != nil {
		return err
	}
	return installer.unwireMCPLaunchAgent(ctx)
}

func (installer *engine) unwireMCPLaunchAgent(ctx context.Context) error {
	path := installer.mcpLaunchAgentPath()
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return installer.retireLaunchAgent(ctx, path, mcpLaunchdLabel, "remove "+path)
}

// retireLaunchAgent unloads label (when applying) and removes its plist at
// path, reporting the removal as message. A job the bootout could not unload
// keeps its plist: the next run finds it and retries, where removing it would
// orphan a loaded job that no plist names and no unwire looks for.
func (installer *engine) retireLaunchAgent(ctx context.Context, path, label, message string) error {
	if installer.apply {
		if err := installer.launchd().Bootout(ctx, label); err != nil {
			return fmt.Errorf("unload %s before removing %s: %w", label, path, err)
		}
	}
	return installer.change(message, func() error { return os.Remove(path) })
}

// launchAgentRunning reports whether the launch agent labelled label (name-sync
// or the schedule fire) is executing right now, and whether the question could
// be asked at all.
//
// "state = not running" contains "running", so the state line is compared whole
// rather than searched — a substring match here would refuse every install on a
// perfectly idle agent. An `Output` error is inspected the same way the systemd
// gate's is: a positive coded exit means launchctl ran and answered (a label it
// does not know is not an error to report — nothing is installed yet, so
// nothing can be mid-execution); anything else means the probe never got an
// answer at all.
func launchAgentRunning(ctx context.Context, runner CommandRunner, label string) (running, probed bool) {
	reader, ok := runner.(OutputRunner)
	if !ok {
		return false, false
	}
	answer := servicemanager.NewLaunchd(commandOutput{reader}, nil).Print(ctx, label)
	if !answer.OK() {
		// A label launchd does not know is not an error to report: nothing is
		// installed yet, so nothing can be mid-execution.
		return false, answer.Ran()
	}
	for _, line := range strings.Split(string(answer.Result.Stdout), "\n") {
		if strings.TrimSpace(line) == "state = running" {
			return true, true
		}
	}
	return false, true
}

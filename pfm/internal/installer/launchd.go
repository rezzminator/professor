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
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// launchdBootstrapAttempts and launchdBootstrapRetryInterval bound the retry
// that rides out a teardown still in flight — ~2s total, bounded so a genuinely
// unloadable job reports instead of hanging the install.
const (
	launchdBootstrapAttempts      = 20
	launchdBootstrapRetryInterval = 100 * time.Millisecond
)

// launchdLabel is the job's name, and the handle launchctl addresses it by.
const launchdLabel = "com.professor.pfm.name-sync"

const mcpLaunchdLabel = "com.professor.pfm.mcp"

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
	return filepath.Join(
		installer.options.Home, "Library", "LaunchAgents", launchdLabel+".plist",
	)
}

func (installer *engine) mcpLaunchAgentPath() string {
	return filepath.Join(
		installer.options.Home, "Library", "LaunchAgents", mcpLaunchdLabel+".plist",
	)
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

	template, err := readAsset(launchdAsset)
	if err != nil {
		return fmt.Errorf("read embedded launch agent: %w", err)
	}
	// launchd has no %h, so the home is substituted here rather than expanded
	// at load time. The poll comes from the machine config's nameSync.interval
	// through the same renderer the systemd timer uses, so the two schedulers
	// cannot drift.
	wanted, err := renderNameSyncLaunchAgent([]byte(strings.ReplaceAll(
		string(template), "__PFM_HOME__", installer.options.Home,
	)), installer.options)
	if err == nil {
		wanted, err = renderServicePath(wanted, installer.options.Home)
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
	if !installer.mcpAnyEnabled() {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		if installer.apply {
			domain := "gui/" + strconv.Itoa(os.Getuid())
			_ = installer.options.Runner.Run(ctx, "launchctl", "bootout", domain+"/"+mcpLaunchdLabel)
		}
		message := fmt.Sprintf("remove %s (no MCP server is enabled in %s)", path, installer.options.MCPConfigPath)
		return installer.change(message, func() error { return os.Remove(path) })
	}
	template, err := readAsset(mcpLaunchdAsset)
	if err != nil {
		return fmt.Errorf("read embedded MCP launch agent: %w", err)
	}
	wanted, err := renderServicePath(
		[]byte(strings.ReplaceAll(string(template), "__PFM_HOME__", installer.options.Home)),
		installer.options.Home,
	)
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
	domain := "gui/" + strconv.Itoa(os.Getuid())
	loaded := installer.options.Runner.Run(ctx, "launchctl", "print", domain+"/"+label) == nil
	if loaded && !plistChanged {
		installer.ok("launchctl " + label + " already loaded, plist unchanged — left running")
		installer.say("")
		return nil
	}
	if loaded {
		// Best effort: a job already gone reports failure here for exactly the
		// state we want, so its exit status decides nothing. The bootstrap
		// retry below is what actually establishes the outcome.
		_ = installer.options.Runner.Run(ctx, "launchctl", "bootout", domain+"/"+label)
	}
	if err := installer.bootstrapWithRetry(ctx, domain, path, label); err != nil {
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
	if err := installer.options.Runner.Run(ctx, "launchctl", "print", domain+"/"+label); err != nil {
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

// bootstrapWithRetry re-registers the job, retrying while launchd finishes a
// teardown that bootout only REQUESTED.
//
// `launchctl bootout` returns as soon as the request is accepted, not when the
// label is gone, so an immediate bootstrap can fail with EIO against a label
// still on its way out. That is the failure that left a stopped daemon with
// nothing to restart it. Retrying the bootstrap closes the window without
// asking anyone to predict how long a teardown takes, and it costs a healthy
// host nothing: the first attempt succeeds and no wait is ever taken.
func (installer *engine) bootstrapWithRetry(ctx context.Context, domain, path, _ string) error {
	var err error
	for attempt := 0; attempt < launchdBootstrapAttempts; attempt++ {
		if attempt > 0 {
			installer.pause(launchdBootstrapRetryInterval)
		}
		if err = installer.options.Runner.Run(ctx, "launchctl", "bootstrap", domain, path); err == nil {
			return nil
		}
	}
	return err
}

// unwireLaunchAgent removes the agent and unloads it.
func (installer *engine) unwireLaunchAgent(ctx context.Context) error {
	path := installer.launchAgentPath()
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		installer.skip("no launch agent at " + path)
		return installer.unwireMCPLaunchAgent(ctx)
	}
	if installer.apply {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = installer.options.Runner.Run(ctx, "launchctl", "bootout", domain+"/"+launchdLabel)
	}
	if err := installer.change("remove "+path, func() error { return os.Remove(path) }); err != nil {
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
	if installer.apply {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = installer.options.Runner.Run(ctx, "launchctl", "bootout", domain+"/"+mcpLaunchdLabel)
	}
	return installer.change("remove "+path, func() error { return os.Remove(path) })
}

// probeAnswered reports whether err came from a probe that ran to completion
// and reported a status, rather than one that never got an answer at all
// (the tool missing from PATH, permission denied, or a signal). A positive
// coded exit means the tool ran and answered; deps.ExitCode reports -1 for
// anything else, including the production runner's plain errors and a
// signal-killed *exec.ExitError.
func probeAnswered(err error) bool {
	return deps.ExitCode(err) > 0
}

// launchAgentRunning reports whether the name-sync job is executing right now,
// and whether the question could be asked at all.
//
// "state = not running" contains "running", so the state line is compared whole
// rather than searched — a substring match here would refuse every install on a
// perfectly idle agent. An `Output` error is inspected the same way the systemd
// gate's is: a positive coded exit means launchctl ran and answered (a label it
// does not know is not an error to report — nothing is installed yet, so
// nothing can be mid-execution); anything else means the probe never got an
// answer at all.
func launchAgentRunning(ctx context.Context, runner CommandRunner) (running, probed bool) {
	reader, ok := runner.(OutputRunner)
	if !ok {
		return false, false
	}
	output, err := reader.Output(
		ctx, "launchctl", "print", "gui/"+strconv.Itoa(os.Getuid())+"/"+launchdLabel,
	)
	if err != nil {
		if probeAnswered(err) {
			// A label launchd does not know is not an error to report: nothing is
			// installed yet, so nothing can be mid-execution.
			return false, true
		}
		return false, false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "state = running" {
			return true, true
		}
	}
	return false, true
}

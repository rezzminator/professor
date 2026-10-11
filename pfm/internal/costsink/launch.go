package costsink

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// launchExec is the process-replacing seam of RunLaunch.
var launchExec = syscall.Exec

// LaunchOptions is one launch the cost ledger decides for.
type LaunchOptions struct {
	// Environ is the environment Claude would start with.
	Environ []string
	// Home resolves the ledger's log file and the ambient settings directory.
	Home string
	// Self is the pfm binary a sink runs from; PID the process that becomes Claude.
	Self string
	PID  int
	// Enabled is claude.costLedger.
	Enabled bool
	// Arm starts the sink; nil takes Arm.
	Arm func(ArmOptions) (Armed, error)
}

// Prepare decides the ledger for one launch and returns the environment Claude starts with and the
// decision (a claudelaunch.CostLedger* word, also recorded in the environment as the marker the
// status line reads): off when claude.costLedger is false; preempted when the user's own telemetry
// settings (process environment or the account's settings.json env, or one that cannot be read) must be kept; failed when the
// sink could not be armed — never fatal, the cause is in the sink log; else on, with the exporter
// variables pointed at a freshly armed sink. Exporters inherited from a parent pfm chat are
// replaced, or dropped when no sink is armed, never kept: their sink dies with that chat. A launch
// already armed for this very process (the cost launcher exec'd pfm's claude shim, which comes back
// here with the same pid) keeps its sink.
func Prepare(opts LaunchOptions) ([]string, string) {
	logf := func(format string, args ...any) { appendLog(opts.Home, "cost-launch", fmt.Sprintf(format, args...)) }
	if !opts.Enabled {
		return withEnv(
			opts.Environ,
			claudelaunch.InheritedCostNames(opts.Environ),
			claudelaunch.EnvCostLedger+"="+claudelaunch.CostLedgerOff,
		), claudelaunch.CostLedgerOff
	}
	settingsDir := claudelaunch.ConfigDirFromEnv(opts.Environ)
	if settingsDir == "" {
		settingsDir = filepath.Join(opts.Home, ".claude")
	}
	names, err := claudelaunch.SettingsEnvNames(settingsDir)
	if err != nil {
		logf("settings.json unreadable, so the user's telemetry settings cannot be ruled out: %v", err)
	}
	reason := claudelaunch.CostPreemptedReason(opts.Environ, names)
	if err != nil && reason == "" {
		reason = "settings.json is unreadable, so the user's telemetry settings cannot be ruled out"
	}
	if reason != "" {
		logf("pre-empted, no cost ledger for pid %d: %s", opts.PID, reason)
		return withEnv(
			opts.Environ,
			claudelaunch.InheritedCostNames(opts.Environ),
			claudelaunch.EnvCostLedger+"="+claudelaunch.CostLedgerPreempted,
		), claudelaunch.CostLedgerPreempted
	}
	if claudelaunch.CostArmedFor(opts.Environ, opts.PID) {
		return opts.Environ, claudelaunch.CostLedgerOn
	}
	arm := opts.Arm
	if arm == nil {
		arm = Arm
	}
	armed, err := arm(ArmOptions{Self: opts.Self, PID: opts.PID, Home: opts.Home})
	if err != nil {
		logf("sink not armed for pid %d: %v", opts.PID, err)
		return withEnv(
			opts.Environ,
			claudelaunch.CostEnvNames(),
			claudelaunch.EnvCostLedger+"="+claudelaunch.CostLedgerFailed,
		), claudelaunch.CostLedgerFailed
	}
	logf("armed a sink on 127.0.0.1:%d for pid %d", armed.Port, opts.PID)
	return withEnv(
		opts.Environ,
		claudelaunch.CostEnvNames(),
		claudelaunch.CostEnv(armed.Port, opts.PID)...), claudelaunch.CostLedgerOn
}

// withEnv returns environ without the entries named in drop or by an assignment, plus the
// assignments.
func withEnv(environ, drop []string, assignments ...string) []string {
	remove := map[string]bool{}
	for _, name := range drop {
		remove[name] = true
	}
	for _, assignment := range assignments {
		name, _, _ := strings.Cut(assignment, "=")
		remove[name] = true
	}
	out := make([]string, 0, len(environ)+len(assignments))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if !remove[name] {
			out = append(out, entry)
		}
	}
	return append(out, assignments...)
}

// Main dispatches `pfm internal cost-launch` and `pfm internal cost-sink`; args[0] names the verb.
func Main(args []string, stderr io.Writer, env paths.Env) int {
	if args[0] == "cost-sink" {
		return RunSink(args[1:], stderr, env)
	}
	return RunLaunch(args[1:], stderr, env)
}

// RunLaunch is `pfm internal cost-launch -- {claude} {args…}`: the wrapper claudelaunch.Render puts in
// front of every interactive Claude. It decides the ledger (Prepare) and execs Claude with the
// resulting environment, keeping its own pid — which the sink watches. It cannot fail a chat: every
// ledger problem is a marker and a log line, and Claude starts regardless.
func RunLaunch(args []string, stderr io.Writer, env paths.Env) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pfm internal cost-launch -- {claude} [arguments]")
		return 2
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal cost-launch: find %s: %v\n", args[0], err)
		return 127
	}
	environ, _ := PrepareCurrent(os.Environ(), env, true, stderr)
	if err := launchExec(path, append([]string{path}, args[1:]...), environ); err != nil {
		fmt.Fprintf(stderr, "pfm internal cost-launch: exec %s: %v\n", path, err)
		return 126
	}
	return 0
}

// PrepareCurrent is Prepare for the running pfm process, the one that is about to become Claude by
// exec: its own pid and binary, the home resolved from env. A home that cannot be resolved marks
// the launch failed on stderr instead of arming a sink whose log has nowhere to go.
func PrepareCurrent(environ []string, env paths.Env, enabled bool, stderr io.Writer) ([]string, string) {
	home, err := paths.HomeFrom(env)
	if err != nil {
		fmt.Fprintf(stderr, "pfm: no cost ledger for this chat: %v\n", err)
		return withEnv(
				environ,
				claudelaunch.CostEnvNames(),
				claudelaunch.EnvCostLedger+"="+claudelaunch.CostLedgerFailed,
			),
			claudelaunch.CostLedgerFailed
	}
	self, err := os.Executable()
	if err != nil {
		self = filepath.Join(home, ".local", "bin", "pfm")
		appendLog(home, "cost-launch", fmt.Sprintf("own path unknown (%v); the sink runs %s", err, self))
	}
	return Prepare(LaunchOptions{Environ: environ, Home: home, Self: self, PID: os.Getpid(), Enabled: enabled})
}

// appendLog adds one line to the sink log; a failure to log is reported on stderr and ignored,
// since the log must never be what stops a chat.
func appendLog(home, who, message string) {
	file, err := openSinkLog(home)
	if err != nil {
		obs.Logger(context.Background()).Warn("cost sink log unwritable; line dropped",
			"who", who, "line", message, obs.FieldErr, err.Error())
		return
	}
	_, err = fmt.Fprintf(file, "%s %s: %s\n", clock.Real.Now().UTC().Format(timeLayoutMS), who, message)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		obs.Logger(context.Background()).Warn("cost sink log write failed; line dropped",
			"who", who, "path", file.Name(), obs.FieldErr, err.Error())
	}
}

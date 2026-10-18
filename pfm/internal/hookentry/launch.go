package hookentry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/costsink"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// LaunchExec is the process-replacing seam used by launch and codex-launch.
var LaunchExec = syscall.Exec

const launcherWaitTimeout = 7 * 24 * time.Hour

// claudeLaunchPIDEnv carries the pid of the launcher that exec'd the real
// Claude path. exec keeps the pid, so a launcher -> shim -> launcher exec chain
// re-enters with its own pid in this marker and is refused, while a real
// Claude's child `claude` run through the shim always has a different pid and
// passes: no binary classification is needed.
const claudeLaunchPIDEnv = "PFM_CLAUDE_LAUNCH_PID"

var nonInteractiveClaudeSubcommands = map[string]bool{
	"agents": true, "mcp": true, "update": true, "install": true,
	"doctor": true, "setup-token": true, "plugin": true, "config": true,
}

func launchPassThrough(arguments []string, tmux string, forced bool) bool {
	return forced || launchInsidePfmSocket(tmux) || launchRunsNonInteractive(arguments)
}

// launchInsidePfmSocket reports a launch from inside a pfm-managed tmux socket.
func launchInsidePfmSocket(tmux string) bool {
	socketPath, _, _ := strings.Cut(tmux, ",")
	if socketPath == "" {
		return false
	}
	_, ok := pfmengine.FromSocket(filepath.Base(socketPath))
	return ok
}

// launchRunsNonInteractive reports a run its arguments make no chat: print mode, help, a version
// query or a non-interactive subcommand.
func launchRunsNonInteractive(arguments []string) bool {
	for _, argument := range arguments {
		switch argument {
		case "-p", "--print", "--output-format", "-h", helpFlag, versionFlag, "-v":
			return true
		}
		if strings.HasPrefix(argument, "--output-format=") {
			return true
		}
	}
	for _, argument := range arguments {
		if argument == "--" || strings.HasPrefix(argument, "-") {
			continue
		}
		return nonInteractiveClaudeSubcommands[argument]
	}
	return false
}

func launchStartsSession(arguments []string) bool {
	for _, argument := range arguments {
		switch argument {
		case "-h", helpFlag, versionFlag, "-v":
			return false
		}
	}
	for _, argument := range arguments {
		if argument == "--" || strings.HasPrefix(argument, "-") {
			continue
		}
		return !nonInteractiveClaudeSubcommands[argument]
	}
	return true
}

// armCostLedger decides the ledger in the process owning the session lifetime; a seam so
// the launcher tests never start a sink.
var armCostLedger = costsink.PrepareCurrent

// launchIsHeadless reports a print-mode run: one prompt in, one answer out, no chat to meter.
func launchIsHeadless(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--" {
			return false
		}
		if argument == "-p" || argument == "--print" || argument == "--output-format" ||
			strings.HasPrefix(argument, "--output-format=") {
			return true
		}
	}
	return false
}

// Launch is the managed Claude launcher entry.
func Launch(args []string, stdout, stderr io.Writer, runtime config.Runtime, env paths.Env) int {
	if env == nil {
		env = paths.OSEnv{}
	}
	flags := cli.NewFlagSet(
		"internal launch",
		"usage: pfm internal launch --real /absolute/path [--cwd DIR] -- [claude arguments]",
		stderr,
	)
	realBinary := flags.String("real", "", "absolute path to the real Claude binary")
	cwd := flags.String("cwd", "", "working directory for the Claude pane")
	backend := flags.String("backend", runtime.Config.Chat.Backend, "chat backend: auto, tmux or herdr")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	arguments := append([]string(nil), flags.Args()...)
	if !filepath.IsAbs(*realBinary) || strings.ContainsRune(*realBinary, '\x00') {
		flags.Usage()
		return 2
	}
	ownPID := strconv.Itoa(os.Getpid())
	if env.Get(claudeLaunchPIDEnv) == ownPID {
		fmt.Fprintf(stderr, "pfm claude launcher re-entered itself via %s; refusing to loop\n", *realBinary)
		return 127
	}
	forcedPassThrough := env.Get("PFM_LAUNCH_PASSTHROUGH") == "1"
	if launchPassThrough(arguments, env.Get("TMUX"), forcedPassThrough) {
		environment := os.Environ()
		if !forcedPassThrough && !launchInsidePfmSocket(env.Get("TMUX")) {
			// A print, help or subcommand run is never the chat bound to the parent's Herdr pane.
			environment = herdr.StripPane(environment)
		}
		if launchStartsSession(arguments) {
			prefs := runtime.Config.Claude
			configDir := config.AmbientClaudeConfigDir()
			for _, account := range runtime.Config.Accounts {
				if configDir != "" && filepath.Clean(account.ConfigDir) == filepath.Clean(configDir) {
					prefs = runtime.Config.EffectiveClaude(account.ID)
					break
				}
			}
			sessionEnv := claudelaunch.SessionEnv(prefs)
			replaced := make(map[string]bool, len(sessionEnv))
			for _, entry := range sessionEnv {
				name, _, _ := strings.Cut(entry, "=")
				replaced[name] = true
			}
			filtered := make([]string, 0, len(environment)+len(sessionEnv))
			for _, entry := range environment {
				name, _, _ := strings.Cut(entry, "=")
				if !replaced[name] {
					filtered = append(filtered, entry)
				}
			}
			filtered = append(filtered, sessionEnv...)
			environment = filtered
			if !launchIsHeadless(arguments) {
				environment, _ = armCostLedger(environment, env, prefs.CostLedger, stderr)
			}
		}
		stamped := make([]string, 0, len(environment)+1)
		for _, entry := range environment {
			if !strings.HasPrefix(entry, claudeLaunchPIDEnv+"=") {
				stamped = append(stamped, entry)
			}
		}
		stamped = append(stamped, claudeLaunchPIDEnv+"="+ownPID)
		environment = stamped
		var guard *gather.AccountGuard
		if launchStartsSession(arguments) {
			account := claudelaunch.ConfigDirFromEnv(environment)
			if account == "" {
				account = config.AmbientClaudeConfigDir()
			}
			var guardErr error
			guard, guardErr = gather.AcquireAccountGuard(account, true)
			if guardErr != nil {
				fmt.Fprintf(stderr, "pfm internal launch: %v\n", guardErr)
				return 1
			}
			if guardErr = guard.Record(os.Getpid()); guardErr != nil {
				fmt.Fprintf(stderr, "pfm internal launch: %v\n", errors.Join(guardErr, guard.Abort(), guard.Close()))
				return 1
			}
			if guardErr = guard.Close(); guardErr != nil {
				fmt.Fprintf(stderr, "pfm internal launch: %v\n", guardErr)
				return 1
			}
		}
		if err := LaunchExec(*realBinary, append([]string{*realBinary}, arguments...), environment); err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: exec real Claude: %v\n", errors.Join(err, guard.Abort()))
			return 1
		}
		return 0
	}
	workingDir := *cwd
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: resolve cwd: %v\n", err)
			return 1
		}
	}
	if !filepath.IsAbs(workingDir) || strings.ContainsRune(workingDir, '\x00') {
		fmt.Fprintln(stderr, "pfm internal launch: --cwd must be an absolute path")
		return 2
	}
	choice, err := launchBackend(context.Background(), *backend, env)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		return 1
	}
	if choice.Warning != "" {
		fmt.Fprintln(stderr, choice.Warning)
	}
	primary, primaryErr := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
	if primaryErr != nil {
		fmt.Fprintf(stderr, "pfm internal launch: read primary account: %v\n", primaryErr)
		return 1
	}
	configDir, accountID := launcherSeatAccount(runtime, primary, paths.OSEnv{})
	identity, resuming, continuing := action.LauncherIdentity(arguments)
	mode := workbench.New
	if resuming || continuing {
		mode = workbench.Resume
	}
	persona, err := action.WorkbenchPersona(workingDir, pfmengine.Claude, mode)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		return 1
	}
	freshID := ""
	if identity == "" && !continuing {
		var idErr error
		freshID, idErr = claudelaunch.NewSessionID()
		if idErr != nil {
			fmt.Fprintf(stderr, "pfm internal launch: new session id: %v\n", idErr)
			return 1
		}
		identity = freshID
	}
	// A forked chat keeps its parent's pfm label as Claude's --name, so Remote
	// Control shows the name the picker shows. A fresh launch has none, and so
	// has a resume in place: it restores its own name from its transcript
	// (claudelaunch.Render gives it no --name).
	name := ""
	inPlace := resuming && !continuing
	if target := action.LauncherResumeTarget(arguments); target != "" && !inPlace {
		label, labelErr := store.SessionLabel(context.Background(), target, store.WithWarningWriter(stderr))
		if labelErr != nil {
			fmt.Fprintf(
				stderr,
				"pfm internal launch: resolve the label of session %s: %v — Claude names this chat itself\n",
				target, labelErr,
			)
		} else {
			name = label
		}
	}
	if choice.Backend == spawn.BackendHerdr {
		return launchNative(
			choice,
			claudelaunch.Request{
				Purpose:    claudelaunch.PurposeLauncher,
				Home:       runtime.Paths.Home,
				Account:    accountID,
				Binary:     *realBinary,
				ConfigDir:  configDir,
				Args:       arguments,
				SessionID:  freshID,
				Name:       name,
				PromptFile: persona.Prompt,
				Effort:     persona.Effort,
				Model:      persona.Model,
			},
			workingDir,
			stdout,
			stderr,
			runtime,
		)
	}
	realRun, promptWarning, err := action.LauncherLaunchAs(
		persona,
		*realBinary,
		arguments,
		configDir,
		runtime.Paths.Home,
		runtime.Config,
		runtime.Config.EffectiveClaude(accountID),
		name,
		freshID,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: build Claude command: %v\n", err)
		return 1
	}
	tmuxBinary, err := obs.Runner(deps.RealRunner{}).LookPath(pfmtmux.Binary)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: find tmux: %v\n", err)
		return 1
	}
	socket := spawn.FreshSocket(pfmengine.Claude)
	session := socket
	socketPath := filepath.Join(runtime.Paths.TmuxDir, socket)
	startChannel := "pfm-launch-start-" + socket
	doneChannel := "pfm-launch-done-" + socket
	startWait := "TMUX= " + action.Quote(tmuxBinary) + " -S " + action.Quote(socketPath) +
		" wait-for " + action.Quote(startChannel)
	interactive := term.IsTerminal(os.Stdin.Fd())
	gateRun := startWait + " && exec " + realRun
	statusPath := ""
	if !interactive {
		if err := os.MkdirAll(runtime.Paths.SIDDir, 0o700); err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: create status directory: %v\n", err)
			return 1
		}
		statusFile, err := os.CreateTemp(runtime.Paths.SIDDir, ".launch-status-")
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: create launcher status file: %v\n", err)
			return 1
		}
		statusPath = statusFile.Name()
		if err := statusFile.Close(); err != nil {
			_ = os.Remove(statusPath)
			fmt.Fprintf(stderr, "pfm internal launch: close launcher status file: %v\n", err)
			return 1
		}
		defer func() {
			if err := os.Remove(statusPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(stderr, "pfm internal launch: remove status file %s: %v\n", statusPath, err)
			}
		}()
		gateRun = launcherStatusRun(startWait, realRun, tmuxBinary, socketPath, doneChannel, statusPath)
	}
	titles := runtime.Config.Tmux.Titles
	client := spawn.TmuxSpawner{Binary: tmuxBinary, TmuxDir: runtime.Paths.TmuxDir, Titles: &titles}
	ctx := context.Background()
	if identity != "" {
		cache := runtime.Config.EffectiveClaude(accountID).Cache1H
		if recordErr := fleetdb.RecordLaunch(ctx, runtime.Paths, fleetdb.Launch{
			SessionID: identity, Engine: pfmengine.Claude, Account: accountID, Cache1H: cache,
		}, clock.Real.Now().Unix()); recordErr != nil {
			fmt.Fprintf(stderr, "pfm: record launch %s: %v\n", identity, recordErr)
		}
	}
	if err := client.NewSession(ctx, spawn.SessionSpec{
		Socket: socket, Session: session, Window: spawn.WindowName(""), CWD: workingDir, Run: gateRun,
		Width: action.HeadlessWidth, Height: action.HeadlessHeight,
	}); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		return 1
	}
	if err := recordLauncherTmuxSeat(ctx, runtime, socket, identity, name); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: persist tmux seat: %v\n", err)
		_ = launchTmuxCommand(ctx, tmuxBinary, socketPath, "kill-server").Run()
		return 1
	}
	defer cleanupLauncherTmuxSeat(ctx, runtime, socket, stderr)
	failed := true
	defer func() {
		if failed {
			_ = launchTmuxCommand(ctx, tmuxBinary, socketPath, "kill-server").Run()
		}
	}()
	if promptWarning != "" && launchnotice.HoldCancellable(stderr, "pfm internal launch", []string{promptWarning}) {
		fmt.Fprintln(stderr, "pfm internal launch: cancelled at the warning — the chat was not started")
		return 130
	}
	if interactive {
		failed = false
		arguments := attachArguments(socketPath, startChannel, session, env.Get("TMUX") != "")
		if err := LaunchExec(tmuxBinary, arguments, deps.EnvironmentWith("TMUX", "")); err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: attach tmux session: %v\n", err)
			return 1
		}
		return 0
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, launcherWaitTimeout)
	defer cancelWait()
	waiter := launchTmuxCommand(waitCtx, tmuxBinary, socketPath, "wait-for", doneChannel)
	if err := waiter.Start(); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: wait for Claude pane: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "pfm launch: %s %s\n", socket, session); err != nil {
		_ = waiter.Process.Kill()
		fmt.Fprintf(stderr, "pfm internal launch: print socket: %v\n", err)
		return 1
	}
	if output, err := launchTmuxCommand(
		ctx,
		tmuxBinary,
		socketPath,
		"wait-for",
		"-S",
		startChannel,
	).CombinedOutput(); err != nil {
		_ = waiter.Process.Kill()
		fmt.Fprintf(
			stderr,
			"pfm internal launch: release Claude pane: %v: %s\n",
			err,
			strings.TrimSpace(string(output)),
		)
		return 1
	}
	waitErr := waiter.Wait()
	if waitCtx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(stderr, "pfm internal launch: launcher wait-for timeout after %s\n", launcherWaitTimeout)
		return 1
	}
	// Starting the waiter does not prove its client connected: a fast child
	// can record its status and close the last pane before that connection.
	// The durable status is authoritative only after the bounded wait ended;
	// a timeout above still fails, and absent or invalid status never succeeds.
	status, err := readLaunchStatus(statusPath)
	if waitErr != nil {
		fmt.Fprintf(stderr, "pfm internal launch: wait for launcher status signal: %v\n", waitErr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		return 1
	}
	failed = false
	return status
}

func launcherStatusRun(startWait, realRun, tmuxBinary, socketPath, doneChannel, statusPath string) string {
	signalDone := "TMUX= " + action.Quote(tmuxBinary) + " -S " + action.Quote(socketPath) +
		" wait-for -S " + action.Quote(doneChannel)
	return startWait + " || exit $?; " + realRun + "; rc=$?; umask 077; " +
		"printf '%s\\n' \"$rc\" > " + action.Quote(statusPath) + "; write_rc=$?; " +
		signalDone + "; signal_rc=$?; " +
		"if [ \"$write_rc\" -ne 0 ]; then exit \"$write_rc\"; fi; " +
		"if [ \"$signal_rc\" -ne 0 ]; then exit \"$signal_rc\"; fi; exit \"$rc\""
}

func readLaunchStatus(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("launcher status file missing: %w", err)
	}
	value := strings.TrimSpace(string(content))
	status, err := strconv.Atoi(value)
	if err != nil || status < 0 || status > 255 {
		return 0, fmt.Errorf("launcher status file invalid: parse=%v value=%q", err, value)
	}
	return status, nil
}

func launchTmuxCommand(ctx context.Context, binary, socketPath string, args ...string) *pfmtmux.Cmd {
	return pfmtmux.Exec(ctx, binary, socketPath, args...)
}

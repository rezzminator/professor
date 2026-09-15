package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/spawn"
	pfmtmux "hostops/pfm/internal/tmux"
)

var launchExec = syscall.Exec

const launcherWaitTimeout = 7 * 24 * time.Hour

var nonInteractiveClaudeSubcommands = map[string]bool{
	agentsCommand: true, mcpCommand: true, updateCommand: true, installCommand: true,
	doctorCommand: true, "setup-token": true, "plugin": true, configCommand: true,
}

// launchPassThrough is the pure policy boundary for the managed Claude
// launcher. It deliberately knows nothing about config, files, or tmux state
// beyond the caller-provided environment strings.
func launchPassThrough(arguments []string, tmux string, forced bool) bool {
	if forced {
		return true
	}
	if socketPath, _, _ := strings.Cut(tmux, ","); socketPath != "" {
		socket := filepath.Base(socketPath)
		if _, ok := pfmengine.FromSocket(socket); ok {
			return true
		}
	}
	for _, argument := range arguments {
		switch argument {
		case "-p", "--print", "--output-format", "-h", helpFlag, "--version", "-v":
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

func runInternalLaunch(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := newFlagSet(
		"internal launch",
		"usage: pfm internal launch --real /absolute/path [--cwd DIR] -- [claude arguments]",
		stderr,
	)
	realBinary := flags.String("real", "", "absolute path to the real Claude binary")
	cwd := flags.String("cwd", "", "working directory for the Claude pane")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	arguments := append([]string(nil), flags.Args()...)
	if !filepath.IsAbs(*realBinary) || strings.ContainsRune(*realBinary, '\x00') {
		flags.Usage()
		return 2
	}
	if launchPassThrough(arguments, os.Getenv("TMUX"), os.Getenv("PFM_LAUNCH_PASSTHROUGH") == "1") {
		if err := launchExec(*realBinary, append([]string{*realBinary}, arguments...), os.Environ()); err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: exec real Claude: %v\n", err)
			return 1
		}
		return 0
	}

	workingDirectory := *cwd
	if workingDirectory == "" {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: resolve cwd: %v\n", err)
			return 1
		}
	}
	if !filepath.IsAbs(workingDirectory) || strings.ContainsRune(workingDirectory, '\x00') {
		fmt.Fprintln(stderr, "pfm internal launch: --cwd must be an absolute path")
		return 2
	}
	primary := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
	configDir := config.AmbientClaudeConfigDir()
	if configDir == "" {
		if account, found := runtime.Config.AccountByID(primary); found && !account.Implicit {
			configDir = account.ConfigDir
		}
	}
	realRun, err := action.LauncherRun(
		*realBinary,
		arguments,
		configDir,
		runtime.Paths.Home,
		runtime.Config.EffectiveClaude(primary),
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: build Claude command: %v\n", err)
		return 1
	}
	tmuxBinary, err := deps.Resolve(tmuxExecutable)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: find tmux: %v\n", err)
		return 1
	}
	socket, err := freshSocket(compose.NewClaude)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: allocate socket: %v\n", err)
		return 1
	}
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
	client := spawn.CommandTmux{Binary: tmuxBinary, TmuxDir: runtime.Paths.TmuxDir, Titles: &titles}
	ctx := context.Background()
	if err := client.NewSession(ctx, spawn.SessionSpec{
		Socket: socket, Session: session, Window: spawn.WindowName(""),
		CWD: workingDirectory, Run: gateRun,
		Width: action.HeadlessWidth, Height: action.HeadlessHeight,
	}); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		return 1
	}
	failed := true
	defer func() {
		if failed {
			_ = launchTmuxCommand(ctx, tmuxBinary, socketPath, "kill-server").Run()
		}
	}()

	if interactive {
		failed = false
		arguments := []string{
			tmuxExecutable,
			"-S",
			socketPath,
			"wait-for",
			"-S",
			startChannel,
			";",
			"attach-session",
			"-t",
			session,
		}
		if err := launchExec(tmuxBinary, arguments, environmentWith("TMUX", "")); err != nil {
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
	if waitErr != nil {
		fmt.Fprintf(stderr, "pfm internal launch: wait for launcher status signal: %v\n", waitErr)
		return 1
	}
	status, err := readLaunchStatus(statusPath)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		return 1
	}
	failed = false
	return status
}

// tmux 1.8 introduced wait-for. Keep this minimum aligned with the doctor
// dependency registry. Reference: upstream CHANGES, "CHANGES FROM 1.7 TO 1.8":
// https://github.com/tmux/tmux/blob/master/CHANGES
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

func launchTmuxCommand(ctx context.Context, binary, socketPath string, args ...string) *exec.Cmd {
	return pfmtmux.Command(ctx, binary, socketPath, args...)
}

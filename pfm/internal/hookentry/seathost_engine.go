package hookentry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// currentClaudeBinary swaps a recorded Claude build that version retention has
// since pruned for the build the launcher picks now; any other recorded path stays.
func currentClaudeBinary(recorded, home, configured string) (string, error) {
	if filepath.Dir(recorded) != gather.ClaudeVersionsDir(home) {
		return recorded, nil
	}
	if _, err := os.Stat(recorded); !errors.Is(err, os.ErrNotExist) {
		return recorded, nil
	}
	resolved, err := gather.ResolveClaudeBinary(home, configured, paths.OSEnv{}.Get("PATH"))
	if err != nil {
		return "", fmt.Errorf("recorded Claude binary %s is gone, resolve its successor: %w", recorded, err)
	}
	return resolved, nil
}

func seatCommand(run fleetdb.SeatRun, argv0 string, tty *os.File, _, _ io.Writer) ([]string, deps.StartOptions, error) {
	if !filepath.IsAbs(run.Binary) || !filepath.IsAbs(run.CWD) {
		return nil, deps.StartOptions{}, fmt.Errorf("binary and cwd must be absolute")
	}
	for _, word := range append(append([]string{run.Binary, run.CWD}, run.Args...), run.Env...) {
		if strings.ContainsRune(word, '\x00') {
			return nil, deps.StartOptions{}, fmt.Errorf("recipe contains NUL")
		}
	}
	options := deps.StartOptions{
		Argv0: argv0, Dir: run.CWD,
		Env:   seatEnvironment(run.Env, os.Environ(), seatPaneKeys(run.Env, os.Environ())),
		Stdin: tty, Stdout: tty, Stderr: tty,
		ProcessGroup: true, Foreground: true, Ctty: int(tty.Fd()),
	}
	return append([]string{run.Binary}, run.Args...), options, nil
}

// seatTerminalKeys is the terminal identity Herdr gives the pane seat-host runs
// in (TERM, COLORTERM, TERM_PROGRAM, TERM_PROGRAM_VERSION, LANG on this host),
// plus the locale and terminfo names that travel with it. A recipe born in the
// launchd daemon captured no terminal, so these never come from the recipe.
var seatTerminalKeys = []string{"TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "LANG", "TERMINFO"}

// seatPaneKeys names every variable the current pane owns over the recipe: its
// Herdr and tmux identity and its terminal identity, LC_* found in either env.
func seatPaneKeys(saved, current []string) []string {
	keys := append(append(herdr.PaneEnv(), "TMUX", "TMUX_PANE"), seatTerminalKeys...)
	for _, entry := range append(append([]string(nil), saved...), current...) {
		if key, _, _ := strings.Cut(entry, "="); strings.HasPrefix(key, "LC_") && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// seatEnvironment applies saved engine settings, then the current pane identity.
// Missing current identity keys remove stale recipe entries rather than reviving them.
func seatEnvironment(saved, current, keys []string) []string {
	replaced := make(map[string]bool, len(keys))
	for _, key := range keys {
		replaced[key] = true
	}
	result := make([]string, 0, len(saved)+len(current))
	for _, entry := range saved {
		key, _, _ := strings.Cut(entry, "=")
		if !replaced[key] {
			result = append(result, entry)
		}
	}
	for _, entry := range current {
		key, _, _ := strings.Cut(entry, "=")
		if replaced[key] {
			result = append(result, entry)
		}
	}
	return result
}

func runSeatEngine(
	runner deps.Runner,
	argv []string,
	options deps.StartOptions,
	tty *os.File,
	onStarted ...func() error,
) (status int, started bool, returnErr error) {
	defer func() {
		ignored := signal.Ignored(syscall.SIGTTOU)
		signal.Ignore(syscall.SIGTTOU)
		restoreErr := seatForeground(int(tty.Fd()), syscall.Getpgrp())
		if !ignored {
			signal.Reset(syscall.SIGTTOU)
		}
		// An engine may handle terminal loss by exiting 1 rather than by signal.
		// Judge the actual PTY hangup, not its exit code or an unreachable API:
		// ordinary engine failures must remain errors even if Herdr is offline.
		hungUp, probeErr := seatTerminalHungUp(options.Ctty)
		if probeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("probe ended terminal: %w", probeErr))
		}
		if hungUp {
			status = 128 + int(syscall.SIGHUP)
			return
		}
		returnErr = errors.Join(returnErr, restoreErr)
	}()
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(signals)
	process, err := startSeatEngine(runner, argv, options)
	if err != nil {
		return 1, false, err
	}
	for _, acknowledge := range onStarted {
		if err := acknowledge(); err != nil {
			return 1, false, errors.Join(err, process.KillGroup(), process.Wait())
		}
	}
	done := make(chan struct{})
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for {
			select {
			case <-done:
				return
			case sig := <-signals:
				if err := process.SignalGroup(sig.(syscall.Signal)); err != nil {
					fmt.Fprintf(options.Stderr, "pfm internal seat-host: forward %s: %v\n", sig, err)
				}
			}
		}
	}()
	waitErr := process.Wait()
	close(done)
	<-forwarded
	// The engine owns this group; reap any job it left behind before restoring the tty.
	groupErr := process.KillGroup()
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		status := exit.ExitCode()
		if status < 0 {
			status = 128 + int(exit.Sys().(syscall.WaitStatus).Signal())
		}
		return status, true, groupErr
	}
	return 0, true, errors.Join(waitErr, groupErr)
}

// seatForeground hands the terminal's foreground to group.
func seatForeground(tty, group int) error {
	return unix.IoctlSetPointerInt(tty, unix.TIOCSPGRP, group)
}

func startSeatEngine(runner deps.Runner, argv []string, options deps.StartOptions) (deps.Process, error) {
	if options.Argv0 != pfmengine.MustLookup(pfmengine.Claude).Binary {
		process, err := obs.Runner(runner).Start(context.Background(), argv, options)
		if err != nil {
			return nil, fmt.Errorf("start foreground engine: %w", err)
		}
		return process, nil
	}
	guard, err := gather.AcquireAccountGuard(claudelaunch.ConfigDirFromEnv(options.Env), true)
	if err != nil {
		return nil, fmt.Errorf("guard native engine account: %w", err)
	}
	process, err := obs.Runner(runner).Start(context.Background(), argv, options)
	if err != nil {
		return nil, fmt.Errorf("start foreground engine: %w", errors.Join(err, guard.Abort(), guard.Close()))
	}
	err = errors.Join(guard.Record(process.Pid()), guard.Close())
	if err == nil {
		return process, nil
	}
	killErr := process.KillGroup()
	waitErr := process.Wait()
	return nil, fmt.Errorf("record native engine account: %w", errors.Join(err, killErr, waitErr, guard.Abort()))
}

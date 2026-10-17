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
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// SeatHost supervises a native engine without a shell or a tmux intermediary.
func SeatHost(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "pfm internal seat-host: expected one seat id")
		return 1
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: close state: %v\n", err)
		}
	}()
	seat, found, err := db.Seat(ctx, args[0])
	if err != nil || !found || seat.Backend != spawn.BackendHerdr {
		fmt.Fprintf(stderr, "pfm internal seat-host: native seat %q unavailable: %v\n", args[0], err)
		return 1
	}
	if (paths.OSEnv{}).Get("TMUX") != "" || (paths.OSEnv{}).Get(herdr.EnvMarker) != "1" ||
		(paths.OSEnv{}).Get(herdr.EnvSocket) == "" ||
		(paths.OSEnv{}).Get(herdr.EnvPane) == "" {
		return failedSeatStart(ctx, db, seat, errors.New("current Herdr pane identity is required"), stderr)
	}
	before := seat
	seat.HerdrSocket = (paths.OSEnv{}).Get(herdr.EnvSocket)
	seat.HerdrPane = (paths.OSEnv{}).Get(herdr.EnvPane)
	seat.HerdrWorkspace, _, _ = strings.Cut(seat.HerdrPane, ":")
	if err := db.UpdateSeat(ctx, before, seat); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: bind current pane: %v\n", err)
		return 1
	}
	descriptor, lookupErr := pfmengine.Lookup(pfmengine.ID(seat.Engine))
	if lookupErr != nil || !descriptor.HerdrNativePrompt {
		return failedSeatStart(ctx, db, seat, fmt.Errorf("engine %q has no native seat support", seat.Engine), stderr)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return failedSeatStart(ctx, db, seat, fmt.Errorf("open controlling terminal: %w", err), stderr)
	}
	defer func() {
		if err := tty.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: close controlling terminal: %v\n", err)
		}
	}()
	// /dev/tty is an ioctl handle, not the concrete device engines expect on stdio.
	terminalPath, err := seatTerminalPath(int(tty.Fd()))
	if err != nil {
		return failedSeatStart(ctx, db, seat, fmt.Errorf("resolve pane terminal: %w", err), stderr)
	}
	terminal, err := os.OpenFile(terminalPath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return failedSeatStart(ctx, db, seat, fmt.Errorf("open pane terminal %s: %w", terminalPath, err), stderr)
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: close pane terminal: %v\n", err)
		}
	}()
	for {
		run, err := db.ReadSeatRun(seat.SeatID)
		if err != nil {
			return failedSeatStart(ctx, db, seat, err, stderr)
		}
		argv, options, err := seatCommand(run, descriptor.Binary, terminal, stdout, stderr)
		if err != nil {
			return failedSeatStart(ctx, db, seat, err, stderr)
		}
		options.Env = seatEnvironment(
			options.Env,
			[]string{paths.EnvStateDB + "=" + runtime.Paths.StateDB},
			[]string{paths.EnvStateDB},
		)
		// Every native birth door shares this lifetime owner; no shell wrapper or creator-bound sink.
		options.Env, _ = armCostLedger(options.Env, paths.OSEnv{}, run.CostLedger, stderr)
		status, started, err := runSeatEngine(argv, options, tty)
		if !started {
			return failedSeatStart(ctx, db, seat, err, stderr)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: seat %s: %v\n", seat.SeatID, err)
		}
		next, found, readErr := db.Seat(ctx, seat.SeatID)
		if readErr != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: read ended seat: %v\n", readErr)
			return 1
		}
		if found && next.ReloadPending && err == nil {
			before := next
			next.ReloadPending = false
			if err := db.UpdateSeat(ctx, before, next); err != nil {
				fmt.Fprintf(stderr, "pfm internal seat-host: clear reload: %v\n", err)
				return 1
			}
			seat = next
			continue
		}
		cleanupErr := errors.Join(db.DeleteSeatRun(seat.SeatID), db.DeleteSeat(ctx, seat.SeatID))
		if cleanupErr != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: clean ended seat: %v\n", cleanupErr)
			return 1
		}
		if _, err := herdr.SeatRequest(
			ctx,
			seat.HerdrSocket,
			"pane.close",
			map[string]string{"pane_id": seat.HerdrPane},
		); err != nil &&
			!isHerdrNotFound(err) {
			fmt.Fprintf(stderr, "pfm internal seat-host: close ended pane: %v\n", err)
			if status == 0 {
				return 1
			}
		}
		return status
	}
}

// failedSeatStart preserves the durable row and recipe for diagnosis. It is not
// the normal engine-exit path: the pane remains available to show the failure.
func failedSeatStart(ctx context.Context, db *fleetdb.Store, seat fleetdb.Seat, cause error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "pfm internal seat-host: seat %s: %v\n", seat.SeatID, cause)
	before := seat
	seat.State = "failed"
	seat.LostNote = cause.Error()
	seat.UpdatedAt = clock.Real.Now().Unix()
	if err := db.UpdateSeat(ctx, before, seat); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: record failed seat %s: %v\n", seat.SeatID, err)
	}
	return 1
}

func seatCommand(
	run fleetdb.SeatRun,
	argv0 string,
	tty *os.File,
	_, _ io.Writer,
) ([]string, deps.StartOptions, error) {
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
		Env:   seatEnvironment(run.Env, os.Environ(), append(herdr.Env(), "TMUX", "TMUX_PANE")),
		Stdin: tty, Stdout: tty, Stderr: tty,
		ProcessGroup: true, Foreground: true, Ctty: int(tty.Fd()),
	}
	return append([]string{run.Binary}, run.Args...), options, nil
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

func runSeatEngine(argv []string, options deps.StartOptions, tty *os.File) (status int, started bool, returnErr error) {
	defer func() {
		ignored := signal.Ignored(syscall.SIGTTOU)
		signal.Ignore(syscall.SIGTTOU)
		restoreErr := seatForeground(int(tty.Fd()), syscall.Getpgrp())
		if !ignored {
			signal.Reset(syscall.SIGTTOU)
		}
		returnErr = errors.Join(returnErr, restoreErr)
	}()
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(signals)
	process, err := startSeatEngine(argv, options)
	if err != nil {
		return 1, false, err
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
				if err := syscall.Kill(
					-process.Pid(),
					sig.(syscall.Signal),
				); err != nil &&
					!errors.Is(err, syscall.ESRCH) {
					fmt.Fprintf(options.Stderr, "pfm internal seat-host: forward %s: %v\n", sig, err)
				}
			}
		}
	}()
	waitErr := process.Wait()
	close(done)
	<-forwarded
	// The engine owns this group; reap any job it left behind before restoring the tty.
	groupErr := syscall.Kill(-process.Pid(), syscall.SIGKILL)
	if errors.Is(groupErr, syscall.ESRCH) {
		groupErr = nil
	}
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

func startSeatEngine(argv []string, options deps.StartOptions) (deps.Process, error) {
	guard, err := gather.AcquireAccountGuard(claudelaunch.ConfigDirFromEnv(options.Env), true)
	if err != nil {
		return nil, fmt.Errorf("guard native engine account: %w", err)
	}
	process, err := obs.Runner(deps.RealRunner{}).Start(context.Background(), argv, options)
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

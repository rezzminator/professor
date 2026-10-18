package hookentry

import (
	"bufio"
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
	"time"

	"golang.org/x/sys/unix"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const (
	nativeSeatLive  = "live"
	internalCommand = "internal"
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
		fmt.Fprintln(stderr, "pfm internal seat-host: current Herdr pane identity is required")
		return 1
	}
	seat, err = bindNativeHost(ctx, db, seat, runtime.Paths)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: bind current pane: %v\n", err)
		return 1
	}

	descriptor, lookupErr := pfmengine.Lookup(pfmengine.ID(seat.Engine))
	if lookupErr != nil {
		return failedSeatStart(
			ctx,
			db,
			seat,
			fmt.Errorf("engine %q has no native seat support", seat.Engine),
			stderr,
		)
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
		return failedSeatStart(
			ctx,
			db,
			seat,
			fmt.Errorf("open pane terminal %s: %w", terminalPath, err),
			stderr,
		)
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: close pane terminal: %v\n", err)
		}
	}()
	input := bufio.NewReader(terminal)
	for {
		run, runErr := db.ReadSeatRun(seat.SeatID)
		var argv []string
		var options deps.StartOptions
		if runErr == nil {
			argv, options, runErr = seatCommand(run, descriptor.Binary, terminal, stdout, stderr)
		}
		status, started := 1, false
		if runErr == nil {
			options.Env = seatEnvironment(
				options.Env,
				[]string{
					paths.EnvStateDB + "=" + runtime.Paths.StateDB,
					herdr.EnvPane + "=" + seat.HerdrPane,
					herdr.EnvWorkspace + "=" + seat.HerdrWorkspace,
				},
				[]string{paths.EnvStateDB, herdr.EnvPane, herdr.EnvWorkspace, herdr.EnvTab},
			)
			options.Env, _ = armCostLedger(options.Env, paths.OSEnv{}, run.CostLedger, stderr)
			acknowledge := seat.ReloadPending
			status, started, runErr = runSeatEngine(argv, options, tty, func() error {
				current, found, err := db.Seat(ctx, seat.SeatID)
				if err != nil {
					return fmt.Errorf("read started seat: %w", err)
				}
				if !found {
					return errors.New("started seat disappeared")
				}
				current, err = proveRunningHost(ctx, db, current, seat, runtime.Paths)
				if err != nil {
					return fmt.Errorf("prove started seat: %w", err)
				}
				before := current
				if current.State == "failed" {
					current.LostNote = ""
				}
				current.State = nativeSeatLive
				if acknowledge {
					current.ReloadPending = false
				}
				current.UpdatedAt = clock.Real.Now().Unix()
				if err := db.UpdateSeat(ctx, before, current); err != nil {
					return fmt.Errorf("record started seat: %w", err)
				}
				seat = current
				return nil
			})
		}
		if !started {
			failedSeatStart(ctx, db, seat, runErr, stderr)
			for {
				fmt.Fprintln(stderr, "press Enter to retry, q to close")
				line, err := input.ReadString('\n')
				if err != nil {
					fmt.Fprintf(stderr, "pfm internal seat-host: read retry choice: %v\n", err)
					return 1
				}
				choice := strings.TrimSpace(line)
				if choice == "q" {
					return closeSeat(ctx, db, seat, 0, stderr, runtime)
				}
				if choice == "" {
					break
				}
			}
			current, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil || !found {
				fmt.Fprintf(stderr, "pfm internal seat-host: read retry seat: found=%t: %v\n", found, err)
				return 1
			}
			current, err = proveRunningHost(ctx, db, current, seat, runtime.Paths)
			if err != nil {
				fmt.Fprintf(stderr, "pfm internal seat-host: prove retry seat: %v\n", err)
				return 1
			}
			seat = current
			continue
		}
		if runErr != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: seat %s: %v\n", seat.SeatID, runErr)
		}
		next, found, readErr := db.Seat(ctx, seat.SeatID)
		if readErr != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: read ended seat: %v\n", readErr)
			return 1
		}
		if found && next.ReloadPending && runErr == nil {
			seat = next
			continue
		}
		return closeSeat(ctx, db, seat, status, stderr, runtime)
	}
}

// A canonical pane move never replaces the running supervisor identity.
func proveRunningHost(
	ctx context.Context,
	db *fleetdb.Store,
	current, host fleetdb.Seat,
	values paths.Values,
) (fleetdb.Seat, error) {
	if current.SeatHostPID != host.SeatHostPID || current.SeatHostStart != host.SeatHostStart ||
		current.SeatHostBirth != host.SeatHostBirth {
		return current, errors.New("running supervisor binding changed")
	}
	canonical, owned, err := gather.ProveHerdrSeat(ctx, db, gather.NewProcFS(values.ProcRoot), current)
	if err != nil {
		return current, err
	}
	if !owned {
		return current, errors.New("running supervisor is dead")
	}
	if canonical.SeatHostPID != host.SeatHostPID || canonical.SeatHostStart != host.SeatHostStart ||
		canonical.SeatHostBirth != host.SeatHostBirth {
		return current, errors.New("proved supervisor binding changed")
	}
	return canonical, nil
}

// closeSeat keeps the supervisor alive while an independent worker closes the
// pane. HUP is restoration, not end; an already pending worker owns cleanup.
func closeSeat(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	status int,
	stderr io.Writer,
	runtimes ...config.Runtime,
) int {
	if status == 128+int(syscall.SIGHUP) {
		return status
	}
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: read ended seat: %v\n", err)
		return 1
	}
	if !found {
		return status
	}
	if current.SeatHostPID != seat.SeatHostPID || current.SeatHostStart != seat.SeatHostStart ||
		current.SeatHostBirth != seat.SeatHostBirth {
		fmt.Fprintln(stderr, "pfm internal seat-host: ended supervisor binding changed")
		return 1
	}
	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: end runtime: %v\n", err)
		return 1
	}
	if !current.EndPending {
		if err := pfmchat.ScheduleHerdrEnd(
			ctx,
			headless.Chat{Backend: current.Backend, SeatID: current.SeatID, ID: current.SessionID},
			runtime,
		); err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: schedule ended seat: %v\n", err)
			return 1
		}
	}
	deadline := clock.Real.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := clock.Real.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(stderr, "pfm internal seat-host: wait for retirement: %v\n", ctx.Err())
			return 1
		case <-deadline.C():
			fmt.Fprintln(stderr, "pfm internal seat-host: retirement unconfirmed; pending owner retained")
			return 1
		case <-tick.C():
			_, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil {
				fmt.Fprintf(stderr, "pfm internal seat-host: retirement state: %v\n", err)
				return 1
			}
			if !found {
				return status
			}
		}
	}
}

// failedSeatStart preserves the durable row and recipe for diagnosis. It is not
// the normal engine-exit path: the pane remains available to show the failure.
func failedSeatStart(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	cause error,
	stderr io.Writer,
) int {
	fmt.Fprintf(stderr, "pfm internal seat-host: seat %s: %v\n", seat.SeatID, cause)
	// Reload retains conversation exclusion while awaiting this report. Publish
	// through UpdateSeat's binding-guarded lifecycle CAS, never wait on its lock.
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: read failed seat: %v\n", err)
		return 1
	}
	if !found || current.EndPending || current.Backend != seat.Backend || current.HerdrSocket != seat.HerdrSocket ||
		current.HerdrTerminalID != seat.HerdrTerminalID || current.Engine != seat.Engine ||
		current.SeatHostPID != seat.SeatHostPID || current.SeatHostStart != seat.SeatHostStart || current.SeatHostBirth != seat.SeatHostBirth {
		fmt.Fprintln(stderr, "pfm internal seat-host: failed supervisor binding changed")
		return 1
	}
	seat = current
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
		terminal := []unix.PollFd{{Fd: int32(options.Ctty)}}
		_, pollErr := unix.Poll(terminal, 0)
		if pollErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("probe ended terminal: %w", pollErr))
		}
		if pollErr == nil && terminal[0].Revents&unix.POLLHUP != 0 {
			status = 128 + int(syscall.SIGHUP)
			return
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
	if options.Argv0 != pfmengine.MustLookup(pfmengine.Claude).Binary {
		process, err := obs.Runner(deps.RealRunner{}).Start(context.Background(), argv, options)
		if err != nil {
			return nil, fmt.Errorf("start foreground engine: %w", err)
		}
		return process, nil
	}
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

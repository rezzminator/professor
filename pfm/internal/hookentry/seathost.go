package hookentry

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const (
	nativeSeatLive  = "live"
	internalCommand = "internal"
)

// SeatHost supervises a native engine without a shell or a tmux intermediary.
func SeatHost(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	return SeatHostWith(args, stdout, stderr, runtime, nil)
}

// SeatHostWith is SeatHost with the runner that starts the seat engine; nil is the real one.
func SeatHostWith(args []string, stdout, stderr io.Writer, runtime config.Runtime, runner deps.Runner) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "pfm internal seat-host: expected one seat id")
		return 1
	}
	image, imageErr := binwatch.OwnImage()
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal seat-host: close state: %v\n", err)
		}
	}()
	if end, resumed := (paths.OSEnv{}).Lookup(paths.EnvSeatHostEnd); resumed {
		if code, reloading := resumeSeatHostEnd(ctx, db, args[0], end, stderr, runtime); !reloading {
			return code
		}
	}
	seat, found, err := db.Seat(ctx, args[0])
	if err != nil || !found || seat.Backend != spawn.BackendHerdr {
		fmt.Fprintf(stderr, "pfm internal seat-host: native seat %q unavailable: %v\n", args[0], err)
		return 1
	}
	if identity := resolve.CallerEnvironment(); identity.TMUX != "" || (paths.OSEnv{}).Get(herdr.EnvMarker) != "1" ||
		identity.HerdrSocket == "" ||
		identity.HerdrPane == "" {
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
		// Every engine start (the first, a retry, a reload) runs the installed pfm.
		refreshSeatHostImage(image, imageErr, syscall.Exec, os.Environ(), stderr)
		run, runErr := db.ReadSeatRun(seat.SeatID)
		var argv []string
		var options deps.StartOptions
		if runErr == nil {
			machine := runtime.Config
			// The supervisor can outlive a preference change and restart without the
			// restore door. Read the pinned config again before every engine start.
			var policyErr error
			if pfmengine.ID(seat.Engine) == pfmengine.Claude && machine.Path != "" {
				machine, policyErr = config.Load(
					machine.Path,
					runtime.Paths.Home,
					runtime.Paths.Roots[pfmengine.Claude],
					runtime.Paths.FirstRoot(pfmengine.Codex),
				)
			}
			if policyErr != nil {
				run = reload.DisableRecipeRemoteControl(
					run,
					seat.Name,
					fmt.Sprintf("refresh seat launch preferences: %v", policyErr),
					stderr,
				)
			} else {
				run = reload.RefreshRecipeRemoteControl(run, pfmengine.ID(seat.Engine), machine, seat.Name, stderr)
			}
			if pfmengine.ID(seat.Engine) == pfmengine.Claude {
				run.Binary, runErr = currentClaudeBinary(run.Binary, runtime.Paths.Home, machine.Claude.Binary)
			}
		}
		if runErr == nil {
			argv, options, runErr = seatCommand(run, descriptor.Binary, terminal, stdout, stderr)
		}
		if runErr == nil {
			// An in-place open execs this host from the pane's shell: Herdr reads
			// the pane's directory from it, so it works where its chat does.
			if err := os.Chdir(run.CWD); err != nil {
				runErr = fmt.Errorf("enter chat directory %s: %w", run.CWD, err)
			}
		}
		status, started := 1, false
		if runErr == nil {
			// The bound seat's Herdr socket and pane.
			options.Env = seatEnvironment(
				options.Env,
				[]string{
					paths.EnvStateDB + "=" + runtime.Paths.StateDB,
					herdr.EnvSocket + "=" + seat.HerdrSocket,
					herdr.EnvPane + "=" + seat.HerdrPane,
					herdr.EnvWorkspace + "=" + seat.HerdrWorkspace,
				},
				[]string{paths.EnvStateDB, herdr.EnvSocket, herdr.EnvPane, herdr.EnvWorkspace, herdr.EnvTab},
			)
			options.Env = armSeatCostLedger(options.Env, run.CostLedger, stderr)
			acknowledge := seat.ReloadPending
			status, started, runErr = runSeatEngine(runner, argv, options, tty, func() error {
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
			quit, code, asked := seatRetryChoice(input, int(tty.Fd()), stderr)
			if !asked {
				return code
			}
			if quit {
				// Closed on the installed pfm, as an ended engine is; faulted, so a pending reload never starts.
				refreshSeatHostImage(image, imageErr, syscall.Exec, seatHostEndEnviron(0, true), stderr)
				return closeSeat(ctx, db, seat, 0, stderr, runtime)
			}
			// An install during the prompt: the installed pfm reads and proves the seat for the retry.
			refreshSeatHostImage(image, imageErr, syscall.Exec, os.Environ(), stderr)
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
		// The chat ends on the installed pfm: an install's newer schema refuses the replaced image.
		refreshSeatHostImage(image, imageErr, syscall.Exec, seatHostEndEnviron(status, runErr != nil), stderr)
		next, reloading, code := seatEngineEnded(ctx, db, seat, status, runErr != nil, stderr, runtime)
		if !reloading {
			return code
		}
		seat = next
	}
}

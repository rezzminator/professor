package hookentry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// bindNativeHost validates the caller's canonical producer identity and kernel
// membership before publishing any new binding. Only explicit lost restoration
// after positive old-host death may adopt a new terminal. A supervisor that
// re-exec'd itself keeps the binding its earlier image published.
func bindNativeHost(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	values paths.Values,
) (result fleetdb.Seat, returnErr error) {
	if seat.SeatHostPID == os.Getpid() {
		// refreshSeatHostImage keeps the pid and kernel birth this binding names,
		// while a reload awaiting the new engine holds the conversation lock: prove
		// the standing binding as the in-process retry does, never wait on that lock.
		dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), seat)
		if err != nil {
			return seat, fmt.Errorf("check re-executed supervisor binding: %w", err)
		}
		if !dead {
			if seat.EndPending {
				return seat, errors.New("seat absent or pending end")
			}
			proved, err := proveRunningHost(ctx, db, seat, seat, values)
			if err != nil {
				return seat, fmt.Errorf("prove re-executed supervisor binding: %w", err)
			}
			return proved, nil
		}
	}
	if err := awaitReservedPane(ctx, db, seat); err != nil {
		return seat, err
	}
	key := fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID)
	unlock, err := fleetdb.LockSeat(ctx, values, key)
	if err != nil {
		return seat, fmt.Errorf("lock supervisor bind: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return seat, fmt.Errorf("read supervisor binding: %w", err)
	}
	if !found || current.EndPending {
		return seat, errors.New("seat absent or pending end")
	}
	seat = current
	proc := gather.NewProcFS(values.ProcRoot)
	cold := false
	if seat.SeatHostPID > 0 {
		dead, err := gather.HerdrHostDead(proc, seat)
		if err != nil {
			return seat, err
		}
		if seat.SeatHostPID != os.Getpid() || dead {
			if seat.State != "lost" || !dead {
				return seat, errors.New("previous supervisor is not explicitly lost and dead")
			}
			cold = true
		}
	}
	identity := resolve.CallerEnvironment()
	socket, pane := identity.HerdrSocket, identity.HerdrPane
	if socket != seat.HerdrSocket && !cold {
		return seat, errors.New("caller socket differs from durable owner")
	}
	chain, err := herdrAncestry(proc, os.Getpid())
	if err != nil {
		return seat, fmt.Errorf("read supervisor %w", err)
	}
	asked, _, _, err := gather.HerdrChatPane(ctx, db, proc, socket, pane, chain)
	if err != nil {
		return seat, err
	}
	info, found, err := herdr.LocatePane(ctx, socket, asked, chain)
	if err == nil && !found {
		err = fmt.Errorf("no pane of %s holds the supervisor", socket)
	}
	if err != nil {
		return seat, fmt.Errorf("resolve caller alias: %w", err)
	}
	if !cold && seat.HerdrTerminalID != "" && seat.HerdrTerminalID != info.Terminal {
		return seat, errors.New("caller terminal differs from durable owner")
	}
	if !cold && seat.HerdrTerminalID == "" && seat.HerdrPane != pane && seat.HerdrPane != info.ID {
		return seat, errors.New("unbound caller differs from reserved pane")
	}
	// Validate our exact process role before a tentative binding can be observed.
	argv, err := proc.Cmdline(os.Getpid())
	if err != nil {
		return seat, fmt.Errorf("read supervisor argv: %w", err)
	}
	if len(argv) < 4 {
		return seat, errors.New("supervisor argv unavailable")
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	if len(args) >= 2 && args[0] == "--config" {
		args = args[2:]
	}
	if (name != "pfm" && name != "pfm.dev") || len(args) != 3 || args[0] != internalCommand || args[1] != "seat-host" ||
		args[2] != seat.SeatID {
		return seat, errors.New("supervisor must exec exact seat-host role")
	}
	process, err := herdr.ReadPaneProcesses(ctx, socket, info.ID)
	if err != nil {
		return seat, fmt.Errorf("read caller shell: %w", err)
	}
	if process.Pane != info.ID || process.Shell <= 1 {
		return seat, errors.New("caller shell identity unavailable")
	}
	member := false
	seen := map[int]bool{}
	for pid := os.Getpid(); pid > 0 && !seen[pid]; {
		if pid == process.Shell {
			member = true
			break
		}
		seen[pid] = true
		stat, err := proc.Stat(pid)
		if err != nil {
			return seat, fmt.Errorf("read supervisor ancestry: %w", err)
		}
		pid = stat.ParentPID
	}
	if !member {
		return seat, errors.New("supervisor outside caller pane")
	}
	next := seat
	next.HerdrSocket = socket
	next.HerdrPane = info.ID
	next.HerdrWorkspace = info.Workspace
	next.HerdrTerminalID = info.Terminal
	if err := gather.StampHerdrHost(proc, &next, os.Getpid()); err != nil {
		return seat, err
	}
	if err := db.UpdateSeat(ctx, seat, next); err != nil {
		// A gather proof publishes this same binding without the seat lock: adopt it, refuse any other.
		stored, found, readErr := db.Seat(ctx, seat.SeatID)
		adopted := stored
		adopted.HerdrSocket, adopted.HerdrPane = next.HerdrSocket, next.HerdrPane
		adopted.HerdrWorkspace, adopted.HerdrTerminalID = next.HerdrWorkspace, next.HerdrTerminalID
		adopted.SeatHostPID, adopted.SeatHostStart, adopted.SeatHostBirth = next.SeatHostPID, next.SeatHostStart,
			next.SeatHostBirth
		if readErr != nil || !found || adopted != stored {
			return seat, fmt.Errorf("persist supervisor binding: %w", errors.Join(err, readErr))
		}
		next = stored
	}
	proved, owned, err := gather.ProveHerdrSeat(ctx, db, proc, next)
	if err != nil || !owned {
		if err == nil {
			err = errors.New("bound supervisor disappeared")
		}
		rollbackErr := db.UpdateSeat(ctx, next, seat)
		return seat, fmt.Errorf(
			"prove bound supervisor (binding rolled back unless state changed): %w",
			errors.Join(err, rollbackErr),
		)
	}
	return proved, nil
}

// reservedPaneWait bounds a new tab's supervisor waiting for its pane address:
// SpawnHerdr reserves the seat before layout.apply and records the pane Herdr
// names in its answer, and the supervisor that answer started can run first.
var reservedPaneWait = 5 * time.Second

const reservedPanePoll = 20 * time.Millisecond

// awaitReservedPane returns once seat's durable row names a pane or terminal,
// so the bind compares the caller with the reserved pane, never with none.
func awaitReservedPane(ctx context.Context, db *fleetdb.Store, seat fleetdb.Seat) error {
	deadline := clock.Real.Now().Add(reservedPaneWait)
	for seat.HerdrPane == "" && seat.HerdrTerminalID == "" {
		if !clock.Real.Now().Before(deadline) {
			return fmt.Errorf("reservation %s names no Herdr pane after %s", seat.SeatID, reservedPaneWait)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("await reserved pane of %s: %w", seat.SeatID, ctx.Err())
		case <-clock.Real.After(reservedPanePoll):
		}
		current, found, err := db.Seat(ctx, seat.SeatID)
		if err != nil {
			return fmt.Errorf("read reservation %s: %w", seat.SeatID, err)
		}
		if !found || current.EndPending {
			return errors.New("seat absent or pending end")
		}
		seat = current
	}
	return nil
}

package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

// nativeAccountGate asks whether a live native chat born on another account
// should reboot to the picker's account: the one gate the tmux rows use
// (action.Executor's prepareLive). A package var solely so this package's tests
// swap the terminal prompt — production never assigns it.
var nativeAccountGate action.Gate = action.DeviceGate{}

// runNativeReboot is RebootNative, the reboot the open door runs after a yes.
// A package var solely so this package's tests record the request instead of
// reloading a pane — production never assigns it.
var runNativeReboot = RebootNative

// runHerdrReload is reload.RunHerdr, swapped only by this package's tests.
var runHerdrReload = reload.RunHerdr

// RebootNative reboots one live native Herdr seat in place onto account (0:
// the fleet's primary account) with the row's cache setting: the one builder of
// the reload request, shared by the picker's reboot key and the open door's
// account-change offer. A non-live row has nothing to reboot and is returned
// untouched by the caller; runtimes is the optional machine runtime.
func RebootNative(
	ctx context.Context,
	resolved paths.Values,
	row compose.Row,
	account int,
	cache1H bool,
	stderr io.Writer,
	runtimes ...config.Runtime,
) error {
	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		return fmt.Errorf("native reboot runtime: %w", err)
	}
	primary := account
	if primary == 0 {
		primary, err = fleet.PrimaryAccount(resolved, runtime.Config)
		if err != nil {
			return fmt.Errorf("native reboot account: %w", err)
		}
	}
	ids := runtime.Config.AccountIDs()
	codexHome := ""
	if compose.EngineForKind(row.Kind) == pfmengine.Codex {
		ids = runtime.Config.CodexAccountIDs()
		selected, found := runtime.Config.CodexAccountByID(primary)
		if !found {
			return fmt.Errorf("native reboot Codex account %d is not configured", primary)
		}
		codexHome = selected.Home
	}
	_, err = runHerdrReload(
		ctx,
		reload.Request{
			Engine:     compose.EngineForKind(row.Kind),
			SocketPath: row.Socket,
			Pane:       row.PaneID,
			SessionID:  row.ID,
			CWD:        row.CWD,
			Name:       row.Name,
			Account:    primary,
			AccountIDs: ids,
			Cache1H:    cache1H,
			CodexHome:  codexHome,
			Home:       resolved.Home,
			Machine:    runtime.Config,
		},
		reload.Options{Home: resolved.Home, SIDDir: resolved.SIDDir},
		stderr,
	)
	if err != nil {
		return fmt.Errorf("native reboot: %w", err)
	}
	return nil
}

// offerNativeAccount is the account-change offer for a live native chat the
// open door is about to view: when the chat's birth account differs from the
// picker's (primary), it asks the tmux rows' gate and, on yes, reboots the
// seat onto primary. It returns the seat to view: the same one, or the
// rebooted seat re-read by conversation. A refused offer, an unknown birth
// account and the same account open as today; a failed reboot is printed and
// the chat attaches as it is; a failed gate stops the open.
func offerNativeAccount(
	ctx context.Context,
	seat fleetdb.Seat,
	primary int,
	cache1H bool,
	stderr io.Writer,
	runtime config.Runtime,
) (fleetdb.Seat, error) {
	engine := pfmengine.ID(seat.Engine)
	want := runtime.Config.PrimaryAccountFor(engine, primary)
	if want == 0 {
		return seat, nil
	}
	run, err := readNativeRun(ctx, runtime.Paths, seat.SeatID)
	if err != nil {
		// No recipe is no birth account: the open proceeds, never on a guess.
		obs.Logger(ctx).WarnContext(ctx, "native account offer: birth account unreadable",
			"seat", seat.SeatID, "err", err)
		return seat, nil
	}
	birth, known := reload.RecipeAccount(runtime.Config, engine, run.Env)
	if !known || birth == want {
		return seat, nil
	}
	reboot, err := nativeAccountGate.Confirm(ctx, action.GateRequest{
		Name:           seat.Name,
		BirthAccount:   birth,
		PrimaryAccount: want,
		BirthCache1H:   nativeBirthCache(ctx, runtime.Paths, seat, cache1H),
		WantCache1H:    cache1H,
	})
	if err != nil {
		return seat, fmt.Errorf("open gate: %w", err)
	}
	if !reboot {
		return seat, nil
	}
	target := nativeRow(seat)
	target.CWD = run.CWD
	if err := runNativeReboot(ctx, runtime.Paths, target, want, cache1H, stderr, runtime); err != nil {
		fmt.Fprintf(stderr, "pfm chat open: reboot-to-match failed; attaching the live chat as-is: %v\n", err)
		return seat, nil
	}
	current, found, err := nativeSeat(ctx, runtime, fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID), "")
	if err == nil && !found {
		err = fmt.Errorf("native conversation %s vanished after its reboot", seat.SessionID)
	}
	if err != nil {
		return seat, err
	}
	current, alive, err := nativeAlive(ctx, current, runtime.Paths)
	if err == nil && !alive {
		err = fmt.Errorf("native seat %s died during its reboot", current.SeatID)
	}
	if err != nil {
		return seat, err
	}
	return current, nil
}

// nativeBirthCache is the 1h-cache setting the chat was launched with, from
// its launch record, so the gate names a cache change the reboot would make.
// A Codex chat, a missing record or an unreadable one shows no difference
// (want), and an unreadable one is logged.
func nativeBirthCache(ctx context.Context, values paths.Values, seat fleetdb.Seat, want bool) bool {
	if pfmengine.ID(seat.Engine) != pfmengine.Claude {
		return want
	}
	launches, err := fleetdb.OpenLaunches(ctx, values)
	if err != nil {
		obs.Logger(ctx).WarnContext(ctx, "native account offer: launch record unreadable",
			"seat", seat.SeatID, "err", err)
		return want
	}
	launch, err := launches.LaunchFor(ctx, seat.SessionID)
	if closeErr := launches.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		if !errors.Is(err, fleetdb.ErrNoLaunch) {
			obs.Logger(ctx).WarnContext(ctx, "native account offer: launch record unreadable",
				"seat", seat.SeatID, "err", err)
		}
		return want
	}
	return launch.Cache1H
}

func readNativeRun(ctx context.Context, values paths.Values, seatID string) (run fleetdb.SeatRun, returnErr error) {
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	return db.ReadSeatRun(seatID)
}

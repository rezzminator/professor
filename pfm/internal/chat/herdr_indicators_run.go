package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// PaintHerdrIndicators is the actual plugin command's chat-aware painter.
// The inspector persists across half-second polls and incrementally reads tails.
func PaintHerdrIndicators(ctx context.Context, socket, lockDir string, stderr io.Writer) (returnErr error) {
	persisted := tabIndicatorWarnings{dir: lockDir, stderr: stderr}
	runtime, err := pfmconfig.RuntimeOrDefault(nil)
	if err != nil {
		loadErr := fmt.Errorf("load tab indicator runtime: %w", err)
		_, writeErr := fmt.Fprintln(persisted, loadErr)
		return errors.Join(loadErr, writeErr)
	}
	run, err := newHerdrIndicatorRun(&runtime, persisted)
	if err != nil {
		_, writeErr := fmt.Fprintln(persisted, err)
		return errors.Join(err, writeErr)
	}
	defer func() {
		if err := run.held.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the tab indicators' fleet store: %w", err))
		}
	}()
	return herdr.PaintIndicatorsWithTabs(
		ctx,
		socket,
		lockDir,
		persisted,
		run.provider.tabs,
		run.provider.changed,
		run.provePane,
	)
}

// herdrIndicatorRun is one painter run's tab provider, view reader and pane
// proof, all reading the fleet through one store held open across the run's
// ticks: a tick reuses its connections instead of opening and migrating the
// databases for every scan, pane proof and tab validation.
type herdrIndicatorRun struct {
	held     *store.Held
	provider herdrTabProvider
	reader   tabViewReader
}

// newHerdrIndicatorRun builds a painter run over runtime; persisted receives
// its diagnostics, each per-chat one named once while it recurs (tickWarnings).
func newHerdrIndicatorRun(runtime *pfmconfig.Runtime, persisted io.Writer) (*herdrIndicatorRun, error) {
	warnings := &tickWarnings{out: persisted}
	proc := gather.NewProcFS(runtime.Paths.ProcRoot)
	inspector := NewSeatInspector(runtime)
	run := &herdrIndicatorRun{held: store.NewHeld(store.WithWarningWriter(persisted))}
	run.provider = herdrTabProvider{
		inspect: inspector.Inspect,
		rest:    inspector.Rest,
		warn:    warnings,
		probe:   &tabProbe{openCodeRoots: runtime.Paths.Roots[pfmengine.OpenCode]},
	}
	calls, err := NewCallEngine(false, runtime)
	if err != nil {
		return nil, fmt.Errorf("load tab indicator screen reader: %w", err)
	}
	run.provider.screen = func(ctx context.Context, chat headless.Chat) (string, error) {
		return calls.CaptureScreen(ctx, call.Target{
			Backend: chat.Backend, SeatID: chat.SeatID, SocketPath: chat.Socket,
			Pane: chat.Pane, Engine: string(chat.Engine),
		})
	}
	settings := herdrIndicatorConfig{path: runtime.Config.Path}
	run.provider.mode = settings.mode
	stateDir := themesync.DefaultStateDir(runtime.Paths.Home)
	run.provider.glyphs = func() (herdr.TabGlyphs, error) { return themesync.HerdrTabGlyphs(stateDir) }
	var panes []herdr.TabPane
	var panesRead uint64 // the read the panes were listed for
	run.provider.scan = func(ctx context.Context) (FleetScan, error) {
		inspector.Sweep()
		database, err := run.held.Store(ctx)
		if err != nil {
			return FleetScan{}, fmt.Errorf("open the tab indicators' fleet store: %w", err)
		}
		return scanFleetRows(ctx, database, persisted, runtime)
	}
	run.reader = tabViewReader{
		runtime: runtime,
		warn:    warnings,
		tmux:    gather.TmuxRead,
		resolve: herdr.WireResolver(
			proc.Environ,
			herdr.Call,
			func() uint64 { return uint64(clock.Real.Now().UnixNano()) },
		).Resolve,
		state: run.state,
		prove: func(ctx context.Context, seat fleetdb.Seat, values paths.Values) (fleetdb.Seat, bool, error) {
			state, err := run.state(ctx)
			if err != nil {
				return seat, false, err
			}
			return gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
		},
	}
	run.provider.views = func(ctx context.Context, target headless.Chat, socket string) ([]herdr.TabDecoration, error) {
		if panesRead != run.provider.reads {
			var err error
			panes, err = herdr.ReadTabPanes(ctx, socket)
			var incomplete *herdr.IncompleteTabPanesError
			if errors.As(err, &incomplete) {
				// The complete panes still paint; only the skipped ones go unpainted.
				if _, writeErr := fmt.Fprintf(
					warnings,
					"Herdr tab panes: %v (painting the complete panes)\n",
					err,
				); writeErr != nil {
					return nil, fmt.Errorf("persist tab pane warning: %w", writeErr)
				}
			} else if err != nil {
				return nil, fmt.Errorf("%w: %w", errTabPaneList, err)
			}
			panesRead = run.provider.reads
		}
		return run.reader.views(ctx, target, socket, panes)
	}
	return run, nil
}

// state is the shared state of the run's held store.
func (run *herdrIndicatorRun) state(ctx context.Context) (*fleetdb.Store, error) {
	database, err := run.held.Store(ctx)
	if err != nil {
		return nil, fmt.Errorf("open the tab indicators' fleet store: %w", err)
	}
	return database.Shared(), nil
}

// provePane is call.ProveHerdrPane over the run's held shared state; a store
// that could not open leaves the pane unproved, never a non-fleet pane.
func (run *herdrIndicatorRun) provePane(ctx context.Context, socket, pane string) error {
	state, err := run.state(ctx)
	if err != nil {
		return fmt.Errorf("herdr pane %s ownership unproved: %w", pane, err)
	}
	return call.ProveHerdrPaneIn(ctx, state, socket, pane)
}

// seat reads a native seat through the reader's state, else by opening the
// shared state for this read (nativeSeat).
func (reader *tabViewReader) seat(ctx context.Context, id, seatID string) (fleetdb.Seat, bool, error) {
	if reader.state == nil {
		return nativeSeat(ctx, *reader.runtime, id, seatID)
	}
	db, err := reader.state(ctx)
	if err != nil {
		return fleetdb.Seat{}, false, err
	}
	return nativeSeatIn(ctx, db, id, seatID)
}

// nativeSeatIn is nativeSeat over a shared state its caller holds open.
func nativeSeatIn(
	ctx context.Context,
	db *fleetdb.Store,
	id, seatID string,
) (seat fleetdb.Seat, found bool, returnErr error) {
	if seatID != "" {
		seat, found, returnErr = db.Seat(ctx, seatID)
	} else {
		seat, found, returnErr = db.SeatBySession(ctx, id)
		if returnErr == nil && !found && id != "" {
			seat, found, returnErr = db.Seat(ctx, id)
		}
	}
	if returnErr == nil && found &&
		(seat.Backend != spawn.BackendHerdr || (seat.SessionID != id && seat.SeatID != id)) {
		found = false
	}
	return seat, found, returnErr
}

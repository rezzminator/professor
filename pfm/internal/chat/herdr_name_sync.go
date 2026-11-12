package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/statusline"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// ConvergeHerdrNames carries in-engine /rename (which fires no title hook)
// from the index to proved native seats: a Claude seat follows its
// transcript's custom title, an unnamed Codex seat its thread's indexed name. name-sync
// calls this only on --apply, through the very same naming path as DeliverName.
func ConvergeHerdrNames(ctx context.Context, db *store.Store, values paths.Values, stderr io.Writer) (ok bool) {
	ok = true
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: close native name state: %v\n", err)
			ok = false
		}
	}()
	seats, err := state.Seats(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: enumerate native names: %v\n", err)
		return false
	}
	cxNames, err := db.CxNames(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: read Codex thread names: %v\n", err)
		return false
	}
	for index := range seats {
		seat := &seats[index]
		if !convergibleSeat(*seat) {
			continue
		}
		var name string
		switch seat.Engine {
		case string(engine.Claude):
			indexed, found, err := db.Transcript(ctx, seat.SessionID)
			if err != nil {
				fmt.Fprintf(stderr, "pfm name-sync: read native name %s: %v\n", seat.SeatID, err)
				ok = false
				continue
			}
			if found {
				name = indexed.CustomTitle
			}
		case string(engine.Codex):
			// Codex's ledger also holds its automatic titles, indistinguishable
			// from a /rename: it names only an unnamed seat (compose/herdr.go's rule).
			if seat.Name == "" {
				name = naming.CxName(seat.SessionID, "", "", cxNames, "")
			}
		}
		if err := convergeSeatName(ctx, values, *seat, name); err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: native seat %s NOT renamed %q: %v\n", seat.SeatID, name, err)
			ok = false
		}
	}
	return ok
}

// A Claude /rename reaches the chat's statusline before any hook: the render
// names the native seat through this door (statusline cannot import chat).
func init() { statusline.RenameHerdrSeat = ConvergeHerdrSessionName }

// ConvergeHerdrSessionName carries one Claude chat's engine title to its proved
// native seat the moment the chat's statusline renders it: Claude fires no hook
// on /rename, and its statusline input carries the new session_name within one
// render. The name is the one the hooks give the seat (EngineTitle: a reserved
// title never reaches Herdr, pfm's clear name outranks a title Claude inherited
// through /clear). A session holding no live native seat changes nothing.
func ConvergeHerdrSessionName(ctx context.Context, values paths.Values, sessionID, title string) (returnErr error) {
	if err := naming.ValidateLabel(title); err != nil {
		obs.Logger(ctx).Info("engine title not used for the native seat", "session", sessionID,
			"reason", err.Error())
		title = ""
	}
	name, err := EngineTitle(ctx, values, sessionID, title)
	if err != nil {
		return fmt.Errorf("resolve the native seat name of %s: %w", sessionID, err)
	}
	if name == "" {
		return nil
	}
	state := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	if errors.Is(state.Degraded(), fleetdb.ErrAbsent) {
		return nil
	}
	seat, found, err := state.SeatBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("read the native seat of %s: %w", sessionID, err)
	}
	if !found || !convergibleSeat(seat) {
		return nil
	}
	return convergeSeatName(ctx, values, seat, name)
}

// EngineTitle is the name a Claude chat's engine title gives its seat: the
// title itself, or pfm's clear name while Claude still shows the title the
// cleared conversation inherited. Seeing a title other than the inherited one
// confirms the user's rename for good (naming.ClearName.ObserveTitle).
func EngineTitle(ctx context.Context, values paths.Values, sessionID, title string) (name string, returnErr error) {
	state := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	if errors.Is(state.Degraded(), fleetdb.ErrAbsent) {
		return title, nil
	}
	decision, found, err := state.ClearName(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return title, nil
	}
	if observed := decision.ObserveTitle(title); observed != decision {
		writable := fleetdb.OpenSharedState(ctx, values)
		err := writable.PutClearName(ctx, sessionID, observed, clock.Real.Now().Unix())
		if err = errors.Join(err, writable.Close()); err != nil {
			return "", fmt.Errorf("confirm engine title: %w", err)
		}
		decision = observed
	}
	return decision.Title(title), nil
}

// convergibleSeat is a seat whose name follows its engine: a live native seat
// with no end pending.
func convergibleSeat(seat fleetdb.Seat) bool {
	return seat.Backend == spawn.BackendHerdr && seat.State == "live" && !seat.EndPending
}

// convergeSeatName renames a convergible seat through the naming door
// DeliverName uses; an empty name or the seat's own changes nothing.
func convergeSeatName(ctx context.Context, values paths.Values, seat fleetdb.Seat, name string) error {
	if name == "" || name == seat.Name {
		return nil
	}
	target := headless.Chat{SeatID: seat.SeatID, ID: seat.SessionID, Socket: seat.HerdrSocket, Pane: seat.HerdrPane}
	return renameHerdr(ctx, target, name, values)
}

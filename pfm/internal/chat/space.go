package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// spaceDoors are the two reads and moves a space change makes on Herdr: the
// fleet scan its placement files chats by, and the placement of the one chat.
type spaceDoors struct {
	places func(context.Context, *pfmconfig.Runtime) (*place.Fleet, error)
	move   func(context.Context, paths.Values, string, place.Fleet, string) (place.Place, error)
}

// Space is `pfm chat space [name]`: the calling chat records name as its
// space, and moves into the Herdr workspace labelled name (created, unfocused,
// when none wears the label); an empty name clears the space and the chat
// returns to its folder's place. The space is saved per chat, so a reload,
// resume, restore or reconcile keeps the chat in it. It returns the one line
// saying where the chat now sits; a chat outside Herdr records the space and
// says it did not move. A caller that cannot be identified, or a Herdr that
// refuses the move, is an error naming the reason (a refused move leaves the
// space recorded, the next birth's placement retries it).
func Space(ctx context.Context, runtime *pfmconfig.Runtime, name string) (string, error) {
	loaded, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return "", fmt.Errorf("load runtime: %w", err)
	}
	identity, err := localCaller(ctx, &loaded)
	if err != nil {
		return "", fmt.Errorf("identify the calling chat: %w", err)
	}
	return spaceCaller(ctx, loaded, identity, name, spaceDoors{places: HerdrPlaces, move: spawn.PlaceHerdrChat})
}

// spaceCaller is Space for an identified caller.
func spaceCaller(
	ctx context.Context,
	runtime pfmconfig.Runtime,
	identity resolve.Identity,
	name string,
	doors spaceDoors,
) (line string, returnErr error) {
	name = strings.TrimSpace(name)
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("the space name %q must be one printable line", name)
	}
	if name == place.HomeLabel {
		return "", fmt.Errorf("the space name %q is home's place; pick another", name)
	}
	state := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() {
		if err := state.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close state: %w", err))
		}
	}()
	id, socket := identity.ID, ""
	if identity.Backend == spawn.BackendHerdr {
		// The seat's own session id is the chat's id in every row, whichever
		// conversation segment its engine runs now.
		seat, found, err := state.Seat(ctx, identity.SeatID)
		if err == nil && !found {
			err = errors.New("no such seat")
		}
		if err != nil {
			return "", fmt.Errorf("read the calling chat's seat %q: %w", identity.SeatID, err)
		}
		id, socket = seat.SessionID, seat.HerdrSocket
	}
	if id == "" {
		return "", errors.New("the calling chat has no session id yet; send it a first message")
	}
	if err := state.SetSpace(ctx, id, name, clock.Real.Now().Unix()); err != nil {
		return "", fmt.Errorf("record the space: %w", err)
	}
	if identity.Backend != spawn.BackendHerdr {
		return notInHerdrSpaceLine(name), nil
	}
	places, err := doors.places(ctx, &runtime)
	if err != nil {
		return "", fmt.Errorf("space %q is recorded; scan the fleet to place the chat: %w", name, err)
	}
	filed, err := doors.move(ctx, runtime.Paths, socket, *places, id)
	if err != nil {
		return "", fmt.Errorf("space %q is recorded; Herdr did not move the chat: %w", name, err)
	}
	switch {
	case name == "" && filed.Root == "":
		return "chat space cleared: its folder files under no place, so the chat stays where it sits", nil
	case name == "":
		return fmt.Sprintf("chat space cleared: the chat is back in its folder place, Herdr workspace %q",
			filed.Label), nil
	case filed.Root != place.SpaceRoot(name):
		// The scan files a chat by its space only once the index holds the chat
		// (store.Store.ChatSpaces): until then it stays in its folder's place.
		return "", fmt.Errorf("space %q is recorded, but the fleet scan does not hold chat %s yet, so it stays "+
			"in Herdr workspace %q; the next placement of the chat moves it", name, id, filed.Label)
	}
	return fmt.Sprintf("chat is in space %q, Herdr workspace %q", name, filed.Label), nil
}

// notInHerdrSpaceLine words a space change for a chat no Herdr holds: nothing
// moved, the picker's rail files the chat by it.
func notInHerdrSpaceLine(name string) string {
	if name == "" {
		return "chat space cleared: the chat is not in Herdr, so nothing moved; the picker files it by its folder"
	}
	return fmt.Sprintf("chat space %q recorded: the chat is not in Herdr, so nothing moved; "+
		"the picker files it under %q", name, name)
}

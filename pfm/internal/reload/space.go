package reload

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// inheritSpace records, for the session a native `--new` reboot starts in the
// seat at socket and pane, the space of the chat it leaves behind, through
// spawn.BirthSpace, the door every new chat's space is decided by. It runs
// before the seat names born, so the seat still names the chat left behind.
// The pane already sits in that space's workspace, so nothing moves. A failure
// is a warning: the reboot goes on, the new chat filed by its folder.
func inheritSpace(ctx context.Context, values paths.Values, socket, pane, born string, stderr io.Writer) {
	choice := spawn.BackendChoice{
		Backend: spawn.BackendHerdr, HerdrSocket: socket, HerdrPane: pane,
		Places: func(ctx context.Context) (fleet *place.Fleet, returnErr error) {
			database, err := store.Open(store.WithWarningWriter(io.Discard))
			if err != nil {
				return nil, fmt.Errorf("open fleet database: %w", err)
			}
			defer func() { returnErr = errors.Join(returnErr, database.Close()) }()
			spaces, err := database.ChatSpaces(ctx)
			if err != nil {
				return nil, err
			}
			return &place.Fleet{Spaces: spaces}, nil
		},
	}
	if _, err := spawn.BirthSpace(ctx, values, &choice, born, ""); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload --new: the new chat %s does NOT keep the space of the chat left behind "+
			"(pfm chat space sets it again): %v\n", born, err)
	}
}

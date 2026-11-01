package spawn

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	BackendAuto  = "auto"
	BackendTmux  = "tmux"
	BackendHerdr = "herdr"
)

// BackendChoice is the immutable birth location chosen for a chat. A Herdr
// choice names the caller's socket, pane and workspace: an in-place seat takes
// over that pane, while a new tab is born in its place's workspace, which
// SpawnHerdr finds from the fleet Places scans; only a new tab scans.
type BackendChoice struct {
	Backend, HerdrSocket, HerdrPane, HerdrWorkspace string
	InPlace                                         bool
	Places                                          func(context.Context) (*place.Fleet, error)
}

// ErrNoCaller is the caller error of a door that opens for no terminal (a
// timer, a daemon, an MCP open with no carried caller): its chat is placed by
// its subject, never by the process that runs the door.
var ErrNoCaller = errors.New("no caller: placed by the chat's own host")

// Place chooses a birth from the caller's proven identity; it never probes or
// turns an unresolved caller into a tmux birth. With no caller (ErrNoCaller)
// the subject decides: a chat with a recorded Herdr socket revives there, any
// other revive (a stored chat that is no Herdr seat) in tmux, and only a new
// chat falls to requested (the configured chat.backend), then tmux. A Herdr
// choice leaves Places for the door to fill.
func Place(
	ctx context.Context,
	door, requested, recorded string,
	revive bool,
	caller resolve.Identity,
	callerErr error,
) (choice BackendChoice, err error) {
	if requested == "" {
		requested = BackendAuto
	}
	defer func() {
		callerError := ""
		if callerErr != nil {
			callerError = callerErr.Error()
		}
		fields := []any{
			"door", door, "requested", requested, "caller_backend", caller.Backend,
			"caller_socket", caller.SocketPath, "caller_pane", caller.Pane, "caller_workspace", caller.HerdrWorkspace,
			"caller_fronted", caller.Fronted, "caller_source", caller.Source, "caller_err", callerError,
			"recorded_socket", recorded, "revive", revive, "chosen", choice.Backend, "in_place", choice.InPlace,
		}
		if err != nil {
			fields = append(fields, "place_err", err.Error())
		}
		obs.Logger(ctx).InfoContext(ctx, "chat.placement", fields...)
	}()
	if requested != BackendAuto && requested != BackendTmux && requested != BackendHerdr {
		return BackendChoice{}, fmt.Errorf("backend %q: want auto, tmux or herdr", requested)
	}
	if errors.Is(callerErr, ErrNoCaller) {
		switch {
		case recorded != "":
			// A new tab on the chat's own server: Places finds its workspace.
			return BackendChoice{Backend: BackendHerdr, HerdrSocket: recorded}, nil
		case revive:
			// A stored chat that is no Herdr seat: its home is tmux.
			return BackendChoice{Backend: BackendTmux}, nil
		case requested == BackendHerdr:
			return BackendChoice{}, fmt.Errorf(
				"herdr backend needs a caller in a Herdr pane or a recorded Herdr host: %w",
				callerErr,
			)
		}
		return BackendChoice{Backend: BackendTmux}, nil
	}
	if requested == BackendTmux {
		return BackendChoice{Backend: BackendTmux}, nil
	}
	if callerErr != nil && !errors.Is(callerErr, resolve.ErrNotInChat) &&
		!errors.Is(callerErr, resolve.ErrNotRegistered) {
		return BackendChoice{}, fmt.Errorf("place chat: caller unresolved: %w", callerErr)
	}
	if caller.Backend == BackendHerdr && !errors.Is(callerErr, resolve.ErrNotInChat) {
		if caller.SocketPath == "" || caller.Pane == "" || caller.HerdrWorkspace == "" {
			return BackendChoice{}, fmt.Errorf("place chat: caller has incomplete Herdr location")
		}
		return BackendChoice{
			Backend:        BackendHerdr,
			HerdrSocket:    caller.SocketPath,
			HerdrPane:      caller.Pane,
			HerdrWorkspace: caller.HerdrWorkspace,
			InPlace:        errors.Is(callerErr, resolve.ErrNotRegistered) && caller.Fronted,
		}, nil
	}
	if requested == BackendHerdr && recorded != "" {
		// Configured Herdr from outside Herdr: a new tab on the chat's own host.
		return BackendChoice{Backend: BackendHerdr, HerdrSocket: recorded}, nil
	}
	if requested == BackendHerdr {
		reason := callerErr
		if reason == nil {
			reason = fmt.Errorf("caller backend %q", caller.Backend)
		}
		return BackendChoice{}, fmt.Errorf("herdr backend needs a caller in a Herdr pane: %w", reason)
	}
	return BackendChoice{Backend: BackendTmux}, nil
}

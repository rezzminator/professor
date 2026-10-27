package spawn

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	BackendAuto  = "auto"
	BackendTmux  = "tmux"
	BackendHerdr = "herdr"
)

// BackendChoice is the immutable birth location chosen for a chat.
type BackendChoice struct {
	Backend, HerdrSocket, HerdrPane, HerdrWorkspace string
	InPlace                                         bool
}

// Place chooses a birth from the caller's proven identity; it never probes or
// turns an unresolved caller into a tmux birth.
func Place(
	ctx context.Context,
	door, requested string,
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
			"chosen", choice.Backend, "in_place", choice.InPlace,
		}
		if err != nil {
			fields = append(fields, "place_err", err.Error())
		}
		obs.Logger(ctx).InfoContext(ctx, "chat.placement", fields...)
	}()
	if requested != BackendAuto && requested != BackendTmux && requested != BackendHerdr {
		return BackendChoice{}, fmt.Errorf("backend %q: want auto, tmux or herdr", requested)
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
	if requested == BackendHerdr {
		reason := callerErr
		if reason == nil {
			reason = fmt.Errorf("caller backend %q", caller.Backend)
		}
		return BackendChoice{}, fmt.Errorf("herdr backend needs a caller in a Herdr pane: %w", reason)
	}
	return BackendChoice{Backend: BackendTmux}, nil
}

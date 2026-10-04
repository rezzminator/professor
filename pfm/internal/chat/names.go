package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// NameResolver is the fleet roster rung of inject's target resolution, shared
// by `pfm chat inject`, its preview, and MCP chat_inject/capture: one read of
// the composed fleet, reduced to addressable live seats. A roster miss is
// inject.CodeUnknown so the engine continues through the raw pane fallbacks a
// fresh Codex seat needs before it writes its first rollout; a scan that could
// not run is an error, never a miss.
type NameResolver struct {
	Runtime *pfmconfig.Runtime
}

// ResolveName matches name against the live roster (exact before prefix, a
// genuine collision refused as ambiguous); a requiredEngine keeps only that
// engine's seats.
func (resolver NameResolver) ResolveName(
	ctx context.Context,
	name, requiredEngine string,
) (inject.Target, int, string, error) {
	rows, err := Rows(ctx, io.Discard, resolver.Runtime)
	if err != nil {
		return inject.Target{}, inject.CodeUndelivered, "", fmt.Errorf("resolve roster name %q: %w", name, err)
	}
	values, err := resolver.paths()
	if err != nil {
		return inject.Target{}, inject.CodeUndelivered, "", fmt.Errorf("resolve roster name %q: %w", name, err)
	}
	return rosterTarget(values, rows, requiredEngine, name)
}

// rosterTarget is ResolveName over an already-read roster. Only a live seat is
// a target; a name only dead rows answer to is still a miss — the raw pane
// rungs may yet find a fresh seat — but the miss names the newest dead chat
// and says it is dead, so it never reads as a chat that does not exist.
func rosterTarget(
	values paths.Values,
	rows []compose.Row,
	requiredEngine, name string,
) (inject.Target, int, string, error) {
	target, code, detail, err := seatTarget(values, liveSeats(rows, requiredEngine), name)
	if err != nil || code != inject.CodeUnknown {
		return target, code, detail, err
	}
	engineRows := make([]compose.Row, 0, len(rows))
	for index := range rows {
		if requiredEngine == "" || string(compose.EngineForKind(rows[index].Kind)) == requiredEngine {
			engineRows = append(engineRows, rows[index])
		}
	}
	dead, found, err := Match(engineRows, name)
	if err != nil {
		var ambiguous *resolve.RosterAmbiguityError
		if errors.As(err, &ambiguous) {
			return inject.Target{}, inject.CodeUnknown, fmt.Sprintf(
				"%q matched no live chat; its dead matches are tied: %s", name, ambiguous.Error(),
			), nil
		}
		return inject.Target{}, inject.CodeUndelivered, "", fmt.Errorf("resolve dead roster name %q: %w", name, err)
	}
	if !found || dead.Live {
		return inject.Target{}, inject.CodeUnknown, "", nil
	}
	socket := dead.Socket
	if socket == "" {
		socket = "none"
	}
	return inject.Target{}, inject.CodeUnknown, fmt.Sprintf(
		"%q matched no live chat; its newest match is dead: thread id %s (socket %s, name %q)",
		name, dead.ID, socket, dead.Name,
	), nil
}

// paths is the runtime's resolved paths — the ones its scan read — or the
// environment's when the resolver carries no runtime.
func (resolver NameResolver) paths() (paths.Values, error) {
	if resolver.Runtime != nil {
		return resolver.Runtime.Paths, nil
	}
	return paths.Resolve()
}

// seatTarget is ResolveName over an already-read roster of live seats; the
// seat's socket is addressed under values' tmux directory.
func seatTarget(values paths.Values, seats []compose.Row, name string) (inject.Target, int, string, error) {
	chat, found, err := Match(seats, name)
	if err != nil {
		var ambiguous *resolve.RosterAmbiguityError
		if errors.As(err, &ambiguous) {
			return inject.Target{}, inject.CodeAmbiguous, ambiguous.Error(), nil
		}
		return inject.Target{}, inject.CodeUndelivered, "", err
	}
	if !found {
		return inject.Target{}, inject.CodeUnknown, "", nil
	}
	socketPath, err := values.SocketUnder(chat.Socket)
	if err != nil {
		return inject.Target{}, inject.CodeUndelivered, "", fmt.Errorf("roster seat %q socket: %w", chat.Name, err)
	}
	pane := chat.Pane
	if pane == "" {
		pane = chat.Session
	}
	return inject.Target{
		SocketPath: socketPath,
		Pane:       pane,
		Engine:     string(chat.Engine),
		Name:       chat.Name,
		ID:         chat.ID,
		Session:    chat.Session,
	}, 0, "", nil
}

// SenderName is the roster read backwards for the chat at identity's seat —
// the name a peer's inject resolves first. found=false is an answer: the seat
// is not in the roster, or its rows disagree.
func (resolver NameResolver) SenderName(
	ctx context.Context,
	identity resolve.Identity,
) (string, bool, error) {
	rows, err := Rows(ctx, io.Discard, resolver.Runtime)
	if err != nil {
		return "", false, fmt.Errorf("name sender seat %s: %w", identity.Session, err)
	}
	name, found := resolve.ResolveRosterSeat(RosterCandidates(liveSeats(rows, "")), identity)
	return name, found, nil
}

// liveSeats reduces the composed fleet to addressable live seats: live, not
// killed, on a socket, with a pane or session to type into. A killed seat
// never answers to its name — the name usually lives on in its replacement,
// and a tombstone must not turn that into an ambiguity. A requiredEngine
// keeps only that engine's rows.
func liveSeats(rows []compose.Row, requiredEngine string) []compose.Row {
	seats := make([]compose.Row, 0, len(rows))
	for index := range rows {
		row := rows[index]
		if !row.Kind.IsAddressable() || row.Killed || row.Socket == "" ||
			(row.PaneID == "" && row.SessionName == "") ||
			(requiredEngine != "" && string(compose.EngineForKind(row.Kind)) != requiredEngine) {
			continue
		}
		seats = append(seats, row)
	}
	return seats
}

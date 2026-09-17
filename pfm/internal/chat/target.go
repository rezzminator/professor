// Package chat is the verb layer: one operation on one chat, reachable from
// the CLI, the MCP server, the picker and the daemon as a typed call — never
// as argv into package main. A verb takes a typed request and returns a typed
// result, or an error whose kind each surface maps onto its own contract: a
// *TargetError wrapping ErrUnknownChat when nothing answers to the target,
// ErrNoTranscript and ErrNoAnswer when the chat exists but has nothing to
// read, and any other error when a step could not run.
package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/store"
)

var (
	// ErrUnknownChat: nothing in the fleet answers to the target.
	ErrUnknownChat = errors.New("chat not found")
	// ErrNoTranscript: the chat exists but has not written a transcript yet.
	ErrNoTranscript = errors.New("chat has not written a transcript")
	// ErrNoAnswer: the chat's transcript holds no assistant turn yet.
	ErrNoAnswer = errors.New("chat has not answered")
)

// TargetError is a verb's failure to resolve its target. Err is
// ErrUnknownChat when nothing answers to Name; any other Err means the fleet
// scan could not look — never the same answer as "no such chat".
type TargetError struct {
	Name string
	Err  error
}

func (failure *TargetError) Error() string {
	if errors.Is(failure.Err, ErrUnknownChat) {
		return fmt.Sprintf("no chat named %q", failure.Name)
	}
	return failure.Err.Error()
}

func (failure *TargetError) Unwrap() error { return failure.Err }

// Target resolves a verb's target to exactly one chat, or a *TargetError.
func Target(ctx context.Context, name string, runtime *pfmconfig.Runtime) (headless.Chat, error) {
	found, ok, err := Resolve(ctx, name, io.Discard, runtime)
	if err != nil {
		return headless.Chat{}, &TargetError{Name: name, Err: err}
	}
	if !ok {
		return headless.Chat{}, &TargetError{Name: name, Err: ErrUnknownChat}
	}
	return found, nil
}

// Resolve finds a chat by name, id, or socket over a read-only fleet scan —
// the same rows the picker shows, so a chat the user can see is a chat every
// verb can address. "self" and "me" are the caller's own chat. Ambiguity is
// refused rather than guessed at. warn is where gather's probe warnings go:
// the read verbs pass io.Discard, since they answer about ONE chat and a
// warning about somebody else's dead socket is noise in a machine-facing
// answer; `chat new` keeps them.
func Resolve(
	ctx context.Context,
	name string,
	warn io.Writer,
	runtime *pfmconfig.Runtime,
) (headless.Chat, bool, error) {
	if name == "self" || name == "me" {
		identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
		if err != nil {
			return headless.Chat{}, false, err
		}
		identity, err := identifier.Identify(ctx)
		if err != nil {
			seat, found := SeatIdentity(ctx, runtime)
			if !found {
				return headless.Chat{}, false, err
			}
			identity = seat
		}
		switch {
		case identity.ID != "":
			name = identity.ID
		case identity.SocketName != "":
			name = identity.SocketName
		default:
			name = identity.Session
		}
	}
	rows, err := Rows(ctx, warn, runtime)
	if err != nil {
		return headless.Chat{}, false, err
	}
	return Match(rows, name)
}

// Rows is one read-only scan of the whole fleet (the all view).
func Rows(ctx context.Context, warn io.Writer, runtime *pfmconfig.Runtime) (rows []compose.Row, returnErr error) {
	database, err := store.Open(store.WithWarningWriter(warn))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close fleet database: %w", err))
		}
	}()
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: compose.AllView, ReadOnly: true, Runtime: runtime,
	}, warn)
	if err != nil {
		return nil, err
	}
	return scan.Output.Rows, nil
}

// RosterCandidates projects composed rows onto the roster matching rule's input.
func RosterCandidates(rows []compose.Row) []resolve.RosterCandidate {
	candidates := make([]resolve.RosterCandidate, 0, len(rows))
	for index := range rows {
		row := rows[index]
		candidates = append(candidates, resolve.RosterCandidate{
			Name: row.Name, ID: row.ID, Socket: row.Socket,
			Session: row.SessionName, Pane: row.PaneID,
			Engine: string(compose.EngineForKind(row.Kind)), Live: row.Kind.IsAddressable(),
		})
	}
	return candidates
}

// Match applies the roster matching rule to rows: exact before prefix, live
// before resumable, a genuine collision refused as ambiguous.
func Match(rows []compose.Row, name string) (headless.Chat, bool, error) {
	match, found, err := resolve.ResolveRosterName(RosterCandidates(rows), name)
	if err != nil || !found {
		return headless.Chat{}, false, err
	}
	for index := range rows {
		row := rows[index]
		if row.ID == match.ID && row.Socket == match.Socket &&
			row.PaneID == match.Pane && row.Name == match.Name {
			return FromRow(row), true, nil
		}
	}
	return headless.Chat{}, false, fmt.Errorf("resolved roster row disappeared from the same snapshot")
}

// FromRow is a composed row in the shape every verb operates on.
func FromRow(row compose.Row) headless.Chat {
	return headless.Chat{
		Name:    row.Name,
		ID:      row.ID,
		Engine:  compose.EngineForKind(row.Kind),
		Path:    row.Path,
		CWD:     row.CWD,
		Socket:  row.Socket,
		Session: row.SessionName,
		Pane:    row.PaneID,
		Live:    row.Kind.IsAddressable(),
	}
}

// SeatIdentity is the last identity rung for a Codex tool shell under `codex
// app-server` instead of in its own pane. That server is reparented to init and
// serves every seat from one process, so a tool shell it spawns has no tmux
// anywhere in its ancestry — the walk has nothing to find, and the seat's
// messages went out UNSIGNED though the seat itself is plainly addressable.
//
// The shell does carry CODEX_THREAD_ID, and the fleet already binds a thread to
// the socket hosting it: this is that lookup, and nothing more. It runs only
// after the tmux rungs fail, because CODEX_THREAD_ID is INHERITED — a process
// with a pane of its own must never be renamed by an id it merely inherited.
func SeatIdentity(ctx context.Context, runtime *pfmconfig.Runtime) (resolve.Identity, bool) {
	thread := os.Getenv(resolve.CodexThreadEnv)
	if thread == "" || os.Getenv(resolve.ClaudeSessionEnv) != "" {
		return resolve.Identity{}, false
	}
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		return resolve.Identity{}, false
	}
	defer func() {
		if err := database.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "chat identity: close fleet database: %v\n", err)
		}
	}()
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: compose.AllView, ReadOnly: true, Runtime: runtime,
	}, io.Discard)
	if err != nil {
		return resolve.Identity{}, false
	}
	for index := range scan.Output.Rows {
		row := scan.Output.Rows[index]
		if row.ID != thread || row.Socket == "" {
			continue
		}
		// A seat with no live socket is no identity: better to say the sender
		// is underivable than to hand back a handle nobody can reply to.
		return resolve.Identity{
			Session:    row.Socket,
			SocketPath: filepath.Join(scan.Env.Paths.TmuxDir, row.Socket),
			SocketName: row.Socket,
			Engine:     string(pfmengine.Codex),
			ID:         thread,
			Source:     "codex-thread",
			Recovered:  true,
		}, true
	}
	return resolve.Identity{}, false
}

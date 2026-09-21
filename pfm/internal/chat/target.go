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

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type resolvedSelfKey struct{}

// WithResolvedSelf binds an immutable, request-local caller seat. A shared MCP
// daemon uses it to resolve self without consulting process-global identity.
func WithResolvedSelf(ctx context.Context, self headless.Chat) context.Context {
	return context.WithValue(ctx, resolvedSelfKey{}, self)
}

// ScopedSelf returns the exact request-local caller seat, when one was bound.
func ScopedSelf(ctx context.Context) (headless.Chat, bool) {
	if ctx == nil {
		return headless.Chat{}, false
	}
	self, ok := ctx.Value(resolvedSelfKey{}).(headless.Chat)
	return self, ok
}

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

// DeliverName sends a rename command to one live chat. A concrete pane is an
// immutable scoped target; a pane-less aggregate must pass through the normal
// resolution ladder so a split socket is refused as ambiguous.
func DeliverName(
	ctx context.Context,
	chat headless.Chat,
	name string,
	runtime *pfmconfig.Runtime,
) (int, string, error) {
	engine, err := NewInjectEngine(false, runtime)
	if err != nil {
		return 1, "", err
	}
	request := inject.Request{
		Target: PaneTarget(chat), Message: "/rename " + name,
	}
	if chat.Pane == "" {
		result, injectErr := engine.Inject(ctx, request)
		return result.Code, result.Message, injectErr
	}
	values, err := targetPaths(runtime)
	if err != nil {
		return 1, "", err
	}
	socketPath, err := values.SocketUnder(chat.Socket)
	if err != nil {
		return 1, "", err
	}
	target := inject.Target{
		SocketPath: socketPath, Pane: chat.Pane, Engine: string(chat.Engine),
		Name: chat.Name, ID: chat.ID, Session: chat.Session,
	}
	request.Target = chat.Pane
	result, err := engine.InjectTarget(ctx, target, request)
	return result.Code, result.Message, err
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
	normalized := resolve.NormalizeTarget(name)
	selfTarget := normalized == "self" || normalized == "me"
	if selfTarget {
		if self, ok := ScopedSelf(ctx); ok {
			return self, true, nil
		}
	}
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	if err != nil {
		return headless.Chat{}, false, err
	}
	raw, err := rawResolver(runtime)
	if err != nil {
		return headless.Chat{}, false, err
	}
	values, err := targetPaths(runtime)
	if err != nil {
		return headless.Chat{}, false, err
	}
	var requestIdentity *resolve.Identity
	if resolve.IsRawPane(normalized) {
		if scoped, ok := ScopedSelf(ctx); ok {
			identity := resolve.Identity{}
			if scoped.Socket != "" {
				identity.SocketPath, err = values.SocketUnder(scoped.Socket)
				if err != nil {
					return headless.Chat{}, false, fmt.Errorf("resolve scoped caller socket: %w", err)
				}
			}
			requestIdentity = &identity
		}
	}
	var rosterMatch headless.Chat
	roster := resolve.RosterFunc(func(
		ctx context.Context,
		name, requiredEngine string,
	) (resolve.Seat, int, string, error) {
		rows, rowsErr := Rows(ctx, warn, runtime)
		if rowsErr != nil {
			return resolve.Seat{}, resolve.CodeUndelivered, "", rowsErr
		}
		matched, found, matchErr := Match(rows, name)
		if matchErr != nil {
			var ambiguity *resolve.RosterAmbiguityError
			if errors.As(matchErr, &ambiguity) {
				return resolve.Seat{}, resolve.CodeAmbiguous, ambiguity.Error(), nil
			}
			return resolve.Seat{}, resolve.CodeUndelivered, "", matchErr
		}
		if !found || (requiredEngine != "" && string(matched.Engine) != requiredEngine) {
			return resolve.Seat{}, resolve.CodeUnknown, "", nil
		}
		rosterMatch = matched
		socketPath := ""
		if matched.Socket != "" {
			socketPath, matchErr = values.SocketUnder(matched.Socket)
			if matchErr != nil {
				return resolve.Seat{}, resolve.CodeUndelivered, "", matchErr
			}
		}
		pane := matched.Pane
		if pane == "" {
			pane = matched.Session
		}
		return resolve.Seat{
			SocketPath: socketPath, Pane: pane, Engine: string(matched.Engine),
			Name: matched.Name, ID: matched.ID, Session: matched.Session,
		}, 0, "", nil
	})
	seat, code, detail, err := (resolve.Ladder{
		Roster: roster, Raw: raw, Self: identifier,
		CodexSelf:       codexSeatIdentifier{runtime: runtime},
		RequestIdentity: requestIdentity,
		Env:             paths.OSEnv{}, Session: inject.TmuxInjector{},
	}).Resolve(ctx, name, resolve.LadderOptions{})
	if err != nil {
		return headless.Chat{}, false, err
	}
	switch code {
	case 0:
		if rosterMatch != (headless.Chat{}) {
			return rosterMatch, true, nil
		}
		resolved := headless.Chat{
			Name: seat.Name, ID: seat.ID, Engine: pfmengine.ID(seat.Engine),
			Socket: filepath.Base(seat.SocketPath), Session: seat.Session,
			Pane: seat.Pane, Live: true,
		}
		if selfTarget {
			rows, rowsErr := Rows(ctx, warn, runtime)
			if rowsErr != nil {
				return headless.Chat{}, false, rowsErr
			}
			candidates := []string{seat.ID}
			if seat.ID == "" {
				candidates = append(candidates, filepath.Base(seat.SocketPath), seat.Session)
			}
			enriched := false
			for _, candidate := range candidates {
				if candidate == "" {
					continue
				}
				indexed, found, matchErr := Match(rows, candidate)
				if matchErr != nil {
					return headless.Chat{}, false, matchErr
				}
				if found {
					resolved = enrichResolvedSelf(resolved, indexed)
					enriched = true
					break
				}
			}
			if !enriched && seat.ID != "" {
				resolved, rowsErr = enrichRecordedSelf(ctx, resolved, warn)
				if rowsErr != nil {
					return headless.Chat{}, false, rowsErr
				}
			}
		}
		return resolved, true, nil
	case resolve.CodeUnknown:
		return headless.Chat{}, false, nil
	case resolve.CodeAmbiguous:
		return headless.Chat{}, false, errors.New(detail)
	default:
		return headless.Chat{}, false, fmt.Errorf("resolve target %q failed with code %d: %s", name, code, detail)
	}
}

func enrichResolvedSelf(resolved, indexed headless.Chat) headless.Chat {
	if indexed.Live && indexed.Pane != "" {
		return indexed
	}
	resolved.Name = indexed.Name
	resolved.ID = indexed.ID
	resolved.Engine = indexed.Engine
	resolved.Path = indexed.Path
	resolved.CWD = indexed.CWD
	return resolved
}

func enrichRecordedSelf(
	ctx context.Context,
	resolved headless.Chat,
	warn io.Writer,
) (result headless.Chat, returnErr error) {
	database, err := store.Open(store.WithWarningWriter(warn))
	if err != nil {
		return headless.Chat{}, err
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close fleet database: %w", err))
		}
	}()
	if resolved.Engine != pfmengine.Claude {
		return resolved, nil
	}
	transcript, found, err := database.Transcript(ctx, resolved.ID)
	if err != nil {
		return headless.Chat{}, fmt.Errorf("resolve self transcript %q: %w", resolved.ID, err)
	}
	if !found {
		return resolved, nil
	}
	resolved.Name = naming.DisplayName(transcript.CustomTitle, transcript.AITitle, transcript.FirstPrompt)
	resolved.Path = transcript.Path
	resolved.CWD = transcript.CWD
	return resolved, nil
}

func rawResolver(runtime *pfmconfig.Runtime) (*resolve.Resolver, error) {
	binaries := resolve.Binaries{Values: map[pfmengine.ID]string{}}
	if runtime != nil {
		binaries.Values[pfmengine.Claude] = runtime.Config.Claude.Binary
		binaries.Values[pfmengine.Codex] = runtime.Config.Codex.Binary
		binaries.Values[pfmengine.OpenCode] = runtime.Config.OpenCode.Binary
		for _, account := range runtime.Config.Accounts {
			if emoji := runtime.Config.EmojiFor(account.ID); emoji != "" && emoji != "·" {
				binaries.AccountEmojis = append(binaries.AccountEmojis, emoji)
			}
		}
	}
	return resolve.New(nil, binaries)
}

func targetPaths(runtime *pfmconfig.Runtime) (paths.Values, error) {
	if runtime != nil {
		return runtime.Paths, nil
	}
	return paths.Resolve()
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
	env := paths.OSEnv{}
	thread := env.Get(resolve.CodexThreadEnv)
	if thread == "" || env.Get(resolve.ClaudeSessionEnv) != "" {
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

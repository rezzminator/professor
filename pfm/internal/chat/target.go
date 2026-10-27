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
	"path/filepath"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
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
	// ErrTurnInProgress: the chat's newest Codex turn is still running, so
	// what it said last is commentary, not an answer. Never ErrNoAnswer, and
	// never an older turn's answer served in its place.
	ErrTurnInProgress = errors.New("mid-turn: no final answer yet")
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
	return TargetIn(ctx, name, func(ctx context.Context) ([]compose.Row, error) {
		return Rows(ctx, io.Discard, runtime)
	}, runtime)
}

// TargetIn is Target over a scan the caller already holds (ResolveIn).
func TargetIn(
	ctx context.Context,
	name string,
	rows func(context.Context) ([]compose.Row, error),
	runtime *pfmconfig.Runtime,
) (headless.Chat, error) {
	found, ok, err := ResolveIn(ctx, name, rows, io.Discard, runtime)
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
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\x00\r\n") {
		return 1, "", errors.New("chat name must be nonempty and contain no NUL or newline")
	}
	if err := naming.ValidateLabel(name); err != nil {
		return 1, "", err
	}
	engine, err := NewCallEngine(false, runtime)
	if err != nil {
		return 1, "", err
	}
	request := pfmcall.Request{
		Target: PaneTarget(chat), Message: "/rename " + name,
	}
	if chat.Pane == "" {
		result, callErr := engine.Call(ctx, request)
		return result.Code, result.Message, callErr
	}
	values, err := targetPaths(runtime)
	if err != nil {
		return 1, "", err
	}
	socketPath, err := targetSocket(values, chat.Backend, chat.Socket)
	if err != nil {
		return 1, "", err
	}
	target := pfmcall.Target{
		Backend: chat.Backend, SeatID: chat.SeatID, HerdrWorkspace: chat.HerdrWorkspace,
		SocketPath: socketPath, Pane: chat.Pane, Engine: string(chat.Engine),
		Name: chat.Name, ID: chat.ID, Session: chat.Session,
	}
	request.Target = chat.Pane
	result, err := engine.CallTarget(ctx, target, request)
	if err == nil && result.Code == 0 && chat.Backend == spawn.BackendHerdr {
		err = renameHerdr(ctx, chat, name, values)
		if err != nil {
			return 1, result.Message, err
		}
	}
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
	return ResolveIn(ctx, name, func(ctx context.Context) ([]compose.Row, error) {
		return Rows(ctx, warn, runtime)
	}, warn, runtime)
}

// ResolveIn is Resolve over a scan the caller already holds: rows answers the
// roster (and the self enrichment) instead of a fresh fleet scan each time, so
// a watch resolving every target of one poll pays for one scan. The ladder,
// its rungs and its refusals are Resolve's own.
func ResolveIn(
	ctx context.Context,
	name string,
	rows func(context.Context) ([]compose.Row, error),
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
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: CodexSeats(runtime)})
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
			identity := resolve.Identity{
				Backend:        scoped.Backend,
				SeatID:         scoped.SeatID,
				HerdrWorkspace: scoped.HerdrWorkspace,
			}
			if scoped.Socket != "" {
				identity.SocketPath, err = targetSocket(values, scoped.Backend, scoped.Socket)
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
		roster, rowsErr := rows(ctx)
		if rowsErr != nil {
			return resolve.Seat{}, resolve.CodeUndelivered, "", rowsErr
		}
		matched, found, matchErr := Match(roster, name)
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
			socketPath, matchErr = targetSocket(values, matched.Backend, matched.Socket)
			if matchErr != nil {
				return resolve.Seat{}, resolve.CodeUndelivered, "", matchErr
			}
		}
		pane := matched.Pane
		if pane == "" {
			pane = matched.Session
		}
		return resolve.Seat{
			Backend:        matched.Backend,
			SeatID:         matched.SeatID,
			HerdrWorkspace: matched.HerdrWorkspace,
			SocketPath:     socketPath,
			Pane:           pane,
			Engine:         string(matched.Engine),
			Name:           matched.Name,
			ID:             matched.ID,
			Session:        matched.Session,
		}, 0, "", nil
	})
	seat, code, detail, err := (resolve.Ladder{
		Roster: roster, Raw: raw, Self: identifier,
		RequestIdentity: requestIdentity,
		Env:             paths.OSEnv{}, Session: pfmcall.TmuxCaller{},
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
			Backend: seat.Backend, SeatID: seat.SeatID, HerdrWorkspace: seat.HerdrWorkspace,
			Name: seat.Name, ID: seat.ID, Engine: pfmengine.ID(seat.Engine),
			Socket: seatSocket(seat.Backend, seat.SocketPath), Session: seat.Session,
			Pane: seat.Pane, Live: true,
		}
		if selfTarget {
			roster, rowsErr := rows(ctx)
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
				indexed, found, matchErr := Match(roster, candidate)
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
func Rows(ctx context.Context, warn io.Writer, runtime *pfmconfig.Runtime) ([]compose.Row, error) {
	scan, err := ScanRows(ctx, warn, runtime)
	if err != nil {
		return nil, err
	}
	return scan.Rows, nil
}

// FleetScan is one read-only scan of the whole fleet: its rows (the all view),
// and each chat socket whose tmux server it could not read, with why. A seat
// on such a socket is missing from Rows because the scan failed to look, not
// because it ended.
type FleetScan struct {
	Rows       []compose.Row
	Unreadable map[string]string
}

// ScanRows is Rows with the sockets the scan could not read.
func ScanRows(ctx context.Context, warn io.Writer, runtime *pfmconfig.Runtime) (result FleetScan, returnErr error) {
	database, err := store.Open(store.WithWarningWriter(warn))
	if err != nil {
		return FleetScan{}, err
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
		return FleetScan{}, err
	}
	return FleetScan{Rows: scan.Output.Rows, Unreadable: scan.Live.UnreadableSockets}, nil
}

// RosterCandidates projects composed rows onto the roster matching rule's input.
func RosterCandidates(rows []compose.Row) []resolve.RosterCandidate {
	candidates := make([]resolve.RosterCandidate, 0, len(rows))
	for index := range rows {
		row := rows[index]
		candidates = append(candidates, resolve.RosterCandidate{
			Backend: row.Backend, SeatID: row.SeatID, HerdrWorkspace: row.HerdrWorkspace,
			Name: row.Name, ID: row.ID, Socket: row.Socket,
			Session: row.SessionName, Pane: row.PaneID,
			Engine: string(compose.EngineForKind(row.Kind)),
			Live:   row.Kind.IsAddressable() && !row.Killed, ActivityNS: row.ActivityNS,
		})
	}
	return candidates
}

// Match applies the roster matching rule to rows: exact before prefix, a live
// seat before any dead or killed row, the newest dead row when none is live,
// a genuine collision of live seats refused as ambiguous.
func Match(rows []compose.Row, name string) (headless.Chat, bool, error) {
	match, found, err := resolve.ResolveRosterName(RosterCandidates(rows), name)
	if err != nil || !found {
		return headless.Chat{}, false, err
	}
	for index := range rows {
		row := rows[index]
		if row.SeatID == match.SeatID && row.ID == match.ID && row.Socket == match.Socket &&
			row.PaneID == match.Pane && row.Name == match.Name {
			return FromRow(row), true, nil
		}
	}
	return headless.Chat{}, false, fmt.Errorf("resolved roster row disappeared from the same snapshot")
}

// FromRow is a composed row in the shape every verb operates on.
func FromRow(row compose.Row) headless.Chat {
	return headless.Chat{
		Backend: row.Backend, SeatID: row.SeatID, HerdrWorkspace: row.HerdrWorkspace,
		Name:    row.Name,
		ID:      row.ID,
		Engine:  compose.EngineForKind(row.Kind),
		Path:    row.Path,
		CWD:     row.CWD,
		Socket:  row.Socket,
		Session: row.SessionName,
		Pane:    row.PaneID,
		Live:    row.Kind.IsAddressable(),
		PanePID: panePID(row.PanePIDs),
	}
}

// panePID is a row's one pane root, or 0 when it has none or several.
func panePID(pids []int) int {
	if len(pids) != 1 {
		return 0
	}
	return pids[0]
}

// CodexSeats supplies the live fleet rows for Identify's final Codex-thread rung.
func CodexSeats(runtime *pfmconfig.Runtime) func(context.Context) ([]resolve.CodexSeat, error) {
	return func(ctx context.Context) (seats []resolve.CodexSeat, returnErr error) {
		database, err := store.Open(store.WithWarningWriter(io.Discard))
		if err != nil {
			return nil, fmt.Errorf("open Codex identity store: %w", err)
		}
		defer func() {
			if err := database.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close Codex identity store: %w", err))
			}
		}()
		scan, err := fleet.Scan(
			ctx,
			database,
			fleet.Request{View: compose.AllView, ReadOnly: true, Runtime: runtime},
			io.Discard,
		)
		if err != nil {
			return nil, fmt.Errorf("scan Codex identity seats: %w", err)
		}
		return codexSeatsFromRows(scan.Output.Rows, scan.Env.Paths)
	}
}

func codexSeatsFromRows(rows []compose.Row, values paths.Values) ([]resolve.CodexSeat, error) {
	seats := []resolve.CodexSeat{}
	for index := range rows {
		row := &rows[index]
		if row.Kind != compose.LiveCodex || row.Killed || row.Socket == "" {
			continue
		}
		socket, err := targetSocket(values, row.Backend, row.Socket)
		if err != nil {
			return nil, fmt.Errorf("resolve Codex seat %s socket: %w", row.ID, err)
		}
		backend := row.Backend
		if backend == "" {
			backend = spawn.BackendTmux
		}
		session := row.SessionName
		if backend == spawn.BackendTmux && session == "" {
			session = row.Socket
		}
		seats = append(
			seats,
			resolve.CodexSeat{
				ThreadID:       row.ID,
				Backend:        backend,
				SeatID:         row.SeatID,
				SessionName:    session,
				SocketPath:     socket,
				Pane:           row.PaneID,
				HerdrWorkspace: row.HerdrWorkspace,
				BackendError:   row.BackendError,
			},
		)
	}
	return seats, nil
}

// targetSocket keeps native addresses absolute; only tmux sockets use the fleet root.
func targetSocket(values paths.Values, backend, socket string) (string, error) {
	if backend == spawn.BackendHerdr {
		if !filepath.IsAbs(socket) {
			return "", fmt.Errorf("herdr socket must be absolute: %q", socket)
		}
		return socket, nil
	}
	return values.SocketUnder(socket)
}

func seatSocket(backend, socket string) string {
	if backend == spawn.BackendHerdr {
		return socket
	}
	return filepath.Base(socket)
}

package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type callService interface {
	Resolve(context.Context, string) (pfmcall.Target, int, string, error)
	ResolveEngine(context.Context, string, string) (pfmcall.Target, int, string, error)
	Capture(context.Context, string, int) (pfmcall.Target, string, int, string, error)
	Call(context.Context, pfmcall.Request) (pfmcall.Result, error)
}

type backend struct {
	clock                clock.Clock
	database             *store.Store
	sharedState          *fleetdb.Store
	callEngine           callService
	resolver             resolve.Resolver
	chat                 ChatVerbs
	dispatch             Dispatch
	paths                paths.Values
	warnings             io.Writer
	allowAmbientIdentity bool
	runtimeIdentity      string
}

func newBackendConfigured(warnings io.Writer, runtime Runtime) (*backend, error) {
	if runtime.Paths.TmuxDir == "" {
		return nil, fmt.Errorf("configure chat MCP backend: tmux directory is empty")
	}
	if runtime.Clock == nil {
		runtime.Clock = clock.Real
	}
	if len(runtime.Accounts) == 0 {
		machine := pfmconfig.Defaults(runtime.Paths.Home, runtime.Paths.Roots[pfmengine.Claude])
		runtime.Accounts = machine.Accounts
	}
	if runtime.CodexBinary == "" {
		runtime.CodexBinary = pfmengine.MustLookup(pfmengine.Codex).Binary
	}
	if runtime.ClaudeBinary == "" {
		runtime.ClaudeBinary = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	if runtime.OpenCodeBinary == "" {
		runtime.OpenCodeBinary = pfmengine.MustLookup(pfmengine.OpenCode).Binary
	}
	runtimeIdentity, err := deriveChatRuntimeIdentity(runtime)
	if err != nil {
		return nil, fmt.Errorf("configure chat MCP backend: %w", err)
	}
	database, err := store.Open(store.WithWarningWriter(warnings))
	if err != nil {
		return nil, err
	}
	sharedState := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	resolver, err := resolve.New(nil, resolve.Binaries{
		Values:        engineBinaries(runtime),
		AccountEmojis: accountEmojis(runtime.Accounts),
	})
	if err != nil {
		_ = database.Close()
		_ = sharedState.Close()
		return nil, err
	}
	caller, err := pfmcall.New(pfmcall.Dependencies{
		Resolver:       resolver,
		Names:          runtime.Names,
		Spawner:        pfmcall.CommandThenSpawner{ConfigPath: runtime.ConfigPath},
		ClaudeBinary:   runtime.ClaudeBinary,
		CodexBinary:    runtime.CodexBinary,
		OpenCodeBinary: runtime.OpenCodeBinary,
		AccountEmojis:  accountEmojis(runtime.Accounts),
		Recorder:       sharedState.RecordComms,
		WarningWriter:  warnings,
	})
	if err != nil {
		_ = database.Close()
		_ = sharedState.Close()
		return nil, err
	}
	return &backend{
		clock:                runtime.Clock,
		database:             database,
		sharedState:          sharedState,
		callEngine:           caller,
		resolver:             *resolver,
		chat:                 runtime.Chat,
		dispatch:             runtime.Dispatch,
		paths:                runtime.Paths,
		warnings:             warnings,
		allowAmbientIdentity: runtime.AllowAmbientIdentity,
		runtimeIdentity:      runtimeIdentity,
	}, nil
}

func (current *backend) close() error {
	return errors.Join(current.database.Close(), current.sharedState.Close())
}

// engineBinaries is the MCP process's configured engine executables, one entry
// per REGISTERED engine. The shared resolver and the call engine read the
// same map: the resolver used to be built from a Claude+Codex literal while
// pfmcall.New built its own from all three, so an OpenCode pane was resolved
// here against a table that did not know the engine existed — and the next
// engine would land half-configured the same way.
func engineBinaries(runtime Runtime) map[pfmengine.ID]string {
	return map[pfmengine.ID]string{
		pfmengine.Claude:   runtime.ClaudeBinary,
		pfmengine.Codex:    runtime.CodexBinary,
		pfmengine.OpenCode: runtime.OpenCodeBinary,
	}
}

func accountEmojis(accounts []pfmconfig.Account) []string {
	result := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.Emoji != "" && account.Emoji != "·" {
			result = append(result, account.Emoji)
		}
	}
	return result
}

// defaultChatLSLimit keeps a full killed history (hundreds of rows) from
// blowing the caller's tool-result budget in one answer; maxChatLSLimit is the
// ceiling an explicit caller may raise it to.
const (
	defaultChatLSLimit = 200
	maxChatLSLimit     = 1000
)

// list is ls: chat.List under the tool's payload contract, projected
// onto the wire row.
func (current *backend) list(ctx context.Context, input LSInput) (LSOutput, error) {
	return current.listProjected(ctx, input, true, "")
}

// lsScope is public ls's repository scope: the repository of the caller
// _meta resolves, else none — and the scope line says which, and why.
func (current *backend) lsScope(
	ctx context.Context,
	meta map[string]any,
	input LSInput,
) (repo, scope string, err error) {
	if input.All {
		return "", "all repos", nil
	}
	caller, err := current.callerForRequest(ctx, meta)
	if err != nil {
		return "", "", fmt.Errorf("ls: resolve caller scope: %w", err)
	}
	if !caller.valid || strings.TrimSpace(caller.row.Dir) == "" {
		scope = "all repos — caller cwd unknown"
		if caller.detail != "" {
			scope += " (" + caller.detail + ")"
		}
		return "", scope, nil
	}
	repo = gitroot.RepoRoot(caller.row.Dir)
	return repo, repo, nil
}

// listUnbounded projects every row for an internal identity lookup. Public
// ls keeps its payload cap; resolving a caller must not turn a row beyond
// that presentation boundary into an absence.
func (current *backend) listUnbounded(ctx context.Context, input LSInput) (LSOutput, error) {
	return current.listProjected(ctx, input, false, "")
}

func (current *backend) listProjected(
	ctx context.Context,
	input LSInput,
	defaultLimit bool,
	repo string,
) (LSOutput, error) {
	if current.chat == nil {
		return LSOutput{}, fmt.Errorf("ls verb is not configured")
	}
	if input.All && input.Killed {
		return LSOutput{}, fmt.Errorf("all and killed are mutually exclusive")
	}
	limit := input.Limit
	if limit == 0 && defaultLimit {
		limit = defaultChatLSLimit
	}
	if limit < 0 || limit > maxChatLSLimit {
		return LSOutput{}, fmt.Errorf("limit must be between 1 and %d", maxChatLSLimit)
	}
	view := compose.DefaultView
	if input.All {
		view = compose.AllView
	} else if input.Killed {
		view = compose.KilledView
	}
	listed, err := current.chat.List(
		ctx,
		chat.ListRequest{View: view, Project: input.Project, Limit: limit, Repo: repo},
	)
	if err != nil {
		return LSOutput{}, err
	}
	rows := make([]ChatRow, 0, len(listed.Rows))
	for index := range listed.Rows {
		row := &listed.Rows[index]
		session := row.SessionName
		if session == "" {
			session = row.ID
		}
		account := row.Account
		if account == 0 && len(row.Accounts) > 0 {
			account = row.Accounts[0]
		}
		rows = append(rows, ChatRow{
			Backend:        row.Backend,
			SeatID:         row.SeatID,
			HerdrWorkspace: row.HerdrWorkspace,
			BackendError:   row.BackendError,
			LostNote:       row.LostNote,
			Session:        session,
			ID:             row.ID,
			Engine:         compose.EngineForKind(row.Kind),
			State:          chatRowState(*row),
			Dir:            row.CWD,
			Project:        row.Project,
			Name:           row.Name,
			Account:        account,
			Kind:           row.Kind.String(),
			Killed:         row.Killed,
			Socket:         row.Socket,
			Pane:           row.PaneID,
			Herdr:          listed.PresenceOf(index),
			transcriptPath: row.Path,
		})
	}
	scope := "all repos"
	if repo != "" {
		scope = repo
	}
	return LSOutput{
		Errors: listed.Errors,
		Rows:   rows, Count: len(rows), Matched: listed.Matched,
		Truncated: listed.Truncated, KilledCount: listed.KilledCount,
		Filter: input.Project, Scope: scope, Elsewhere: listed.Elsewhere,
	}, nil
}

// chatRowState is the one place a ls row's state is decided.
//
// The killed-but-live arm is the honesty half of the kill fix: a kill that
// closed nothing still wrote its tombstone, so a row came back asserting BOTH
// things at once — killed:true beside state "idle" and kind "live-claude" —
// and no caller could tell a real kill from a de-listing. A contradiction is
// reported as a contradiction, never smoothed into one of its two halves.
func chatRowState(row compose.Row) string {
	if row.BackendError != "" {
		return "error"
	}
	if row.Backend == "herdr" && row.LostNote != "" && !row.Kind.IsAddressable() {
		return "lost"
	}
	switch {
	case row.Killed && row.Kind.IsAddressable():
		return "killed-but-live"
	case row.Kind == compose.Booting:
		// A booting chat HAS a socket and already answers call by
		// name. Excluding it made ls report a chat that exists as
		// simply absent for its first minute.
		return "booting"
	case row.Kind.IsAddressable():
		return "idle"
	default:
		return "resumable"
	}
}

type callerIdentity struct {
	identity resolve.Identity
	row      ChatRow
	present  bool
	valid    bool
	detail   string
	err      error
	absent   bool
}

func (current *backend) proxySeatMatches(proxy ProxyIdentity, row ChatRow) (selected bool, detail string) {
	if row.Kind == compose.LiveSplit.String() {
		return current.proxySocketMatches(proxy, row)
	}
	conflicts := make([]string, 0, 4)

	if proxy.Pane != "" && row.Pane != "" {
		if row.Pane == proxy.Pane {
			selected = true
		} else {
			conflicts = append(conflicts, fmt.Sprintf("pane %q does not match live pane %q", proxy.Pane, row.Pane))
		}
	}
	socketSelected, socketDetail := current.proxySocketMatches(proxy, row)
	selected = selected || socketSelected
	if socketDetail != "" {
		conflicts = append(conflicts, socketDetail)
	}
	return selected, strings.Join(conflicts, "; ")
}

func (current *backend) proxySocketMatches(proxy ProxyIdentity, row ChatRow) (selected bool, detail string) {
	if row.Socket == "" {
		return false, ""
	}
	conflicts := make([]string, 0, 2)
	if proxy.SocketName != "" {
		if row.Socket == proxy.SocketName ||
			(row.Backend == spawn.BackendHerdr && filepath.Base(row.Socket) == proxy.SocketName) {
			selected = true
		} else {
			conflicts = append(conflicts, fmt.Sprintf(
				"socketName %q does not match live socket %q",
				proxy.SocketName,
				row.Socket,
			))
		}
	}
	if proxy.SocketPath != "" {
		rowSocketPath, err := current.rowSocketPath(row)
		switch {
		case err != nil:
			conflicts = append(conflicts, fmt.Sprintf(
				"socketPath %q cannot be verified against live socket %q: %v",
				proxy.SocketPath,
				row.Socket,
				err,
			))
		case filepath.Clean(proxy.SocketPath) == filepath.Clean(rowSocketPath):
			selected = true
		default:
			conflicts = append(conflicts, fmt.Sprintf(
				"socketPath %q does not match live socket path %q",
				proxy.SocketPath,
				rowSocketPath,
			))
		}
	}
	return selected, strings.Join(conflicts, "; ")
}

func (current *backend) proxySessionMatches(
	proxy ProxyIdentity,
	proxyEngine pfmengine.ID,
	row ChatRow,
) (matched bool, detail string) {
	seatSelected, seatDetail := current.proxySeatMatches(proxy, row)
	rowBackend := row.Backend
	if rowBackend == "" {
		rowBackend = spawn.BackendTmux
	}
	if row.Session == proxy.Session && (proxy.Backend != "" || proxy.SocketPath != "" || proxy.Pane != "") {
		path, err := current.rowSocketPath(row)
		if err != nil {
			return false, fmt.Sprintf("MCP caller live location: %v", err)
		}
		if (proxy.Backend != "" && proxy.Backend != rowBackend) ||
			(proxy.SocketPath != "" && filepath.Clean(proxy.SocketPath) != filepath.Clean(path)) ||
			(row.Kind != compose.LiveSplit.String() && proxy.Pane != "" && proxy.Pane != row.Pane) {
			return false, fmt.Sprintf(
				"MCP caller location disagrees with its live chat: payload %s %s %s, row %s %s %s",
				proxy.Backend,
				proxy.SocketPath,
				proxy.Pane,
				rowBackend,
				path,
				row.Pane,
			)
		}
	}

	if proxy.SeatID != "" && proxy.SeatID != row.SeatID {
		if row.Session == proxy.Session {
			return false, fmt.Sprintf("MCP caller seat %q disagrees with live seat %q", proxy.SeatID, row.SeatID)
		}
		return false, ""
	}
	sessionSelected := row.Session != "" && row.Session == proxy.Session
	targeted := sessionSelected
	if row.Kind == compose.LiveSplit.String() && (proxy.SocketName != "" || proxy.SocketPath != "") {
		targeted = seatSelected || seatDetail != ""
	}
	if !targeted {
		return false, ""
	}
	conflicts := make([]string, 0, 2)
	if seatDetail != "" {
		conflicts = append(conflicts, seatDetail)
	}
	if proxyEngine != "" && row.Engine != "" && row.Engine != proxyEngine {
		conflicts = append(conflicts, fmt.Sprintf(
			"engine %q does not match live engine %q",
			proxy.Engine,
			row.Engine,
		))
	}
	if len(conflicts) > 0 {
		return false, "MCP _meta.pfmProxy conflicts with live chat: " + strings.Join(conflicts, "; ")
	}
	if row.Kind == compose.LiveSplit.String() {
		return seatSelected, ""
	}
	return true, ""
}

func (current *backend) callerForRequest(
	ctx context.Context,
	meta map[string]any,
) (caller callerIdentity, returnErr error) {
	defer func() {
		if caller.present && !caller.valid && caller.err == nil && caller.detail != "" {
			caller.err = errors.New(caller.detail)
		}
	}()
	raw, present := meta["threadId"]
	if !present {
		proxyRaw, proxyPresent := meta["pfmProxy"]
		if !proxyPresent {
			return callerIdentity{}, nil
		}
		proxy, detail, err := parseProxyIdentity(proxyRaw)
		if err != nil {
			return callerIdentity{}, err
		}
		caller := callerIdentity{present: true, detail: detail}
		if detail != "" {
			caller.err = fmt.Errorf("%s", detail)
			return caller, nil
		}
		if proxy.Error != nil {
			caller.err = proxy.Error
			caller.detail = proxy.Error.Message
			caller.absent = proxy.Error.Kind == proxyNotInChat
			if proxy.Error.Kind == statusNotRegistered {
				caller.identity = resolve.Identity{
					Backend:        proxy.Backend,
					SocketPath:     proxy.SocketPath,
					SocketName:     proxy.SocketName,
					Pane:           proxy.Pane,
					HerdrWorkspace: proxy.HerdrWorkspace,
				}
			}
			return caller, nil
		}
		var proxyEngine pfmengine.ID
		if proxy.Engine != "" {
			parsedEngine, parseErr := pfmengine.Parse(proxy.Engine)
			if parseErr != nil {
				caller.detail = fmt.Sprintf("MCP _meta.pfmProxy engine %q is invalid: %v", proxy.Engine, parseErr)
				return caller, nil
			}
			proxyEngine = parsedEngine
		}
		listed, err := current.listUnbounded(ctx, LSInput{All: true})
		if err != nil {
			return caller, fmt.Errorf(
				"resolve MCP proxy session %q: list live chats: %w",
				proxy.Session,
				err,
			)
		}
		matches := make([]ChatRow, 0, 1)
		conflictDetail := ""
		for index := range listed.Rows {
			row := &listed.Rows[index]
			if row.Killed || (row.State != "idle" && row.State != "booting" && row.BackendError == "") {
				continue
			}
			matched, rowDetail := current.proxySessionMatches(proxy, proxyEngine, *row)
			if rowDetail != "" && conflictDetail == "" {
				conflictDetail = rowDetail
			}
			if !matched {
				continue
			}
			if row.BackendError != "" {
				caller.detail = fmt.Sprintf("MCP caller backend failed: %s", row.BackendError)
				caller.err = fmt.Errorf("%s", caller.detail)
				return caller, nil
			}
			matches = append(matches, *row)
		}
		if len(matches) == 0 {
			if conflictDetail != "" {
				caller.detail = conflictDetail
				caller.err = fmt.Errorf("%s", conflictDetail)
			} else {
				caller.detail = fmt.Sprintf("MCP _meta.pfmProxy session %q matched no live chat", proxy.Session)
				caller.err = resolve.ErrNotInChat
			}
			return caller, nil
		}
		if len(matches) != 1 {
			caller.err = resolve.ErrAmbiguous
			caller.detail = fmt.Sprintf(
				"MCP _meta.pfmProxy session %q matched %d live chats",
				proxy.Session,
				len(matches),
			)
			return caller, nil
		}
		caller.row = matches[0]
		if caller.row.Kind == compose.LiveSplit.String() {
			if proxy.ID == "" {
				caller.row = ChatRow{}
				caller.detail = fmt.Sprintf(
					"MCP _meta.pfmProxy session %q is split and requires a transcript id",
					proxy.Session,
				)
				return caller, nil
			}
			if proxy.Pane == "" {
				caller.row = ChatRow{}
				caller.detail = fmt.Sprintf(
					"MCP _meta.pfmProxy session %q is split and requires an exact pane binding",
					proxy.Session,
				)
				return caller, nil
			}
			if !filepath.IsAbs(current.paths.SIDDir) {
				return caller, fmt.Errorf(
					"resolve MCP proxy split pane %q: breadcrumb directory is not absolute",
					proxy.Pane,
				)
			}
			boundID, _, bindErr := reload.SessionFromPaneCrumb(
				current.paths.SIDDir,
				caller.row.Socket,
				proxy.Pane,
			)
			if bindErr != nil {
				if errors.Is(bindErr, reload.ErrInvalidPaneCrumb) {
					caller.row = ChatRow{}
					caller.detail = fmt.Sprintf(
						"MCP _meta.pfmProxy split pane %q has an invalid exact breadcrumb binding",
						proxy.Pane,
					)
					return caller, nil
				}
				return caller, fmt.Errorf(
					"resolve MCP proxy split pane %q breadcrumb: %w",
					proxy.Pane,
					bindErr,
				)
			}
			if boundID == "" {
				caller.row = ChatRow{}
				caller.detail = fmt.Sprintf(
					"MCP _meta.pfmProxy split pane %q has no exact transcript binding",
					proxy.Pane,
				)
				return caller, nil
			}
			if boundID != proxy.ID {
				caller.row = ChatRow{}
				caller.detail = fmt.Sprintf(
					"MCP _meta.pfmProxy split pane %q is bound to transcript %q, not %q",
					proxy.Pane,
					boundID,
					proxy.ID,
				)
				return caller, nil
			}
			if current.database == nil {
				return caller, fmt.Errorf("resolve MCP proxy split transcript %q: store is not configured", proxy.ID)
			}
			transcript, found, err := current.database.Transcript(ctx, proxy.ID)
			if err != nil {
				return caller, fmt.Errorf("resolve MCP proxy split transcript %q: %w", proxy.ID, err)
			}
			if !found || strings.TrimSpace(transcript.CWD) == "" {
				caller.row = ChatRow{}
				caller.detail = fmt.Sprintf(
					"MCP _meta.pfmProxy split transcript %q has no indexed working directory",
					proxy.ID,
				)
				return caller, nil
			}
			caller.row.ID = transcript.UUID
			caller.row.Session = proxy.Session
			caller.row.Dir = transcript.CWD
			caller.row.Name = naming.LiveFallback(
				naming.DisplayName(transcript.CustomTitle, transcript.AITitle, transcript.FirstPrompt),
				"",
				"",
				"",
				transcript.LastPrompt,
				true,
			)
			caller.row.Pane = proxy.Pane
		}
		caller.identity, err = current.identityFromRow(caller.row)
		if err != nil {
			return caller, fmt.Errorf("resolve MCP proxy session %q: %w", proxy.Session, err)
		}
		if proxy.ID != "" {
			caller.identity.ID = proxy.ID
		}
		caller.identity.Source = "mcp-proxy"

		caller.valid = true
		return caller, nil
	}
	caller = callerIdentity{present: true}
	threadID, ok := raw.(string)
	if !ok || strings.TrimSpace(threadID) == "" ||
		strings.TrimSpace(threadID) != threadID || containsControl(threadID) {
		caller.detail = "MCP _meta.threadId must be a non-empty thread id without whitespace or control characters"
		return caller, nil
	}
	listed, err := current.listUnbounded(ctx, LSInput{All: true})
	if err != nil {
		return caller, fmt.Errorf("resolve MCP caller thread %q: list live chats: %w", threadID, err)
	}
	seats := current.codexSeatsFromRows(listed.Rows)
	identity, seatErr := resolve.CodexThreadSeat(threadID, seats)
	if seatErr != nil {
		caller.err = seatErr
		caller.detail = seatErr.Error()
		return caller, nil
	}
	for index := range listed.Rows {
		row := &listed.Rows[index]
		if row.ID == threadID && row.Engine == pfmengine.Codex && !row.Killed &&
			row.Kind == compose.LiveCodex.String() {
			caller.row = *row
			break
		}
	}
	caller.identity, err = current.identityFromRow(caller.row)
	if err != nil {
		return caller, fmt.Errorf("resolve MCP caller thread %q: %w", threadID, err)
	}
	caller.identity.Recovered = identity.Recovered
	caller.identity.Source = "mcp-thread-meta"

	caller.valid = true
	return caller, nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func (current *backend) identityFromRow(row ChatRow) (resolve.Identity, error) {
	socketPath, err := current.rowSocketPath(row)
	if err != nil {
		return resolve.Identity{}, fmt.Errorf("live chat %q socket: %w", row.Session, err)
	}
	backend := row.Backend
	if backend == "" {
		backend = spawn.BackendTmux
	}
	session := row.Session
	if session == "" {
		session = row.SeatID
	}
	return resolve.Identity{
		Backend:        backend,
		SeatID:         row.SeatID,
		HerdrWorkspace: row.HerdrWorkspace,
		Session:        session,
		SocketPath:     socketPath,
		SocketName:     filepath.Base(socketPath),
		Pane:           row.Pane,
		Engine:         string(row.Engine),
		ID:             row.ID,
	}, nil
}

func (current *backend) codexSeats(ctx context.Context) ([]resolve.CodexSeat, error) {
	listed, err := current.listUnbounded(ctx, LSInput{All: true})
	if err != nil {
		return nil, fmt.Errorf("list live Codex seats: %w", err)
	}
	return current.codexSeatsFromRows(listed.Rows), nil
}

func (current *backend) codexSeatsFromRows(rows []ChatRow) []resolve.CodexSeat {
	seats := make([]resolve.CodexSeat, 0, len(rows))
	for index := range rows {
		row := &rows[index]
		if row.Engine != pfmengine.Codex || row.Killed || row.Kind != compose.LiveCodex.String() {
			continue
		}
		socketPath, err := current.rowSocketPath(*row)
		backendError := row.BackendError
		if err != nil {
			backendError = fmt.Sprintf("%s live chat %q socket: %v", backendError, row.Session, err)
		}
		backend := row.Backend
		if backend == "" {
			backend = spawn.BackendTmux
		}
		seats = append(
			seats,
			resolve.CodexSeat{
				ThreadID:       row.ID,
				Backend:        backend,
				SeatID:         row.SeatID,
				SessionName:    row.Session,
				SocketPath:     socketPath,
				Pane:           row.Pane,
				HerdrWorkspace: row.HerdrWorkspace,
				BackendError:   backendError,
			},
		)
	}
	return seats
}

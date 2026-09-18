package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"hostops/pfm/internal/chat"
	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/store"
)

type injectionService interface {
	Resolve(context.Context, string) (inject.Target, int, string, error)
	ResolveEngine(context.Context, string, string) (inject.Target, int, string, error)
	Capture(context.Context, string, int) (inject.Target, string, int, string, error)
	Inject(context.Context, inject.Request) (inject.Result, error)
	ScheduleAfterCurrentTurn(context.Context, inject.Request) (inject.Result, error)
	ScheduleSelfCompact(ctx context.Context, focus string, then []string) (inject.Result, error)
}

type backend struct {
	clock                clock.Clock
	database             *store.Store
	sharedState          *fleetdb.Store
	injector             injectionService
	resolver             resolve.Resolver
	chat                 ChatVerbs
	dispatch             Dispatch
	paths                paths.Values
	warnings             io.Writer
	allowAmbientIdentity bool
}

func newBackendConfigured(warnings io.Writer, runtime Runtime) (*backend, error) {
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
	database, err := store.Open(store.WithWarningWriter(warnings))
	if err != nil {
		return nil, err
	}
	sharedState := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	resolver, err := resolve.New(nil, resolve.Binaries{
		Values: map[pfmengine.ID]string{
			pfmengine.Claude: runtime.ClaudeBinary,
			pfmengine.Codex:  runtime.CodexBinary,
		},
		AccountEmojis: accountEmojis(runtime.Accounts),
	})
	if err != nil {
		_ = database.Close()
		_ = sharedState.Close()
		return nil, err
	}
	injector, err := inject.New(inject.Dependencies{
		Resolver:       resolver,
		Names:          runtime.Names,
		Spawner:        inject.CommandThenSpawner{ConfigPath: runtime.ConfigPath},
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
		injector:             injector,
		resolver:             *resolver,
		chat:                 runtime.Chat,
		dispatch:             runtime.Dispatch,
		paths:                runtime.Paths,
		warnings:             warnings,
		allowAmbientIdentity: runtime.AllowAmbientIdentity,
	}, nil
}

func (current *backend) close() error {
	return errors.Join(current.database.Close(), current.sharedState.Close())
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

// list is chat_ls: chat.List under the tool's payload contract, projected
// onto the wire row.
func (current *backend) list(ctx context.Context, input LSInput) (LSOutput, error) {
	if current.chat == nil {
		return LSOutput{}, fmt.Errorf("chat_ls verb is not configured")
	}
	if input.All && input.Killed {
		return LSOutput{}, fmt.Errorf("all and killed are mutually exclusive")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultChatLSLimit
	}
	if limit < 1 || limit > maxChatLSLimit {
		return LSOutput{}, fmt.Errorf("limit must be between 1 and %d", maxChatLSLimit)
	}
	view := compose.DefaultView
	if input.All {
		view = compose.AllView
	} else if input.Killed {
		view = compose.KilledView
	}
	listed, err := current.chat.List(ctx, chat.ListRequest{View: view, Project: input.Project, Limit: limit})
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
			Session: session, ID: row.ID, Engine: compose.EngineForKind(row.Kind),
			State: chatRowState(*row), Dir: row.CWD, Project: row.Project, Name: row.Name,
			Account: account, Kind: row.Kind.String(), Killed: row.Killed,
			Socket: row.Socket, Pane: row.PaneID,
		})
	}
	return LSOutput{
		Rows: rows, Count: len(rows), Matched: listed.Matched,
		Truncated: listed.Truncated, KilledCount: listed.KilledCount,
		Filter: input.Project,
	}, nil
}

// chatRowState is the one place a chat_ls row's state is decided.
//
// The killed-but-live arm is the honesty half of the kill fix: a kill that
// closed nothing still wrote its tombstone, so a row came back asserting BOTH
// things at once — killed:true beside state "idle" and kind "live-claude" —
// and no caller could tell a real kill from a de-listing. A contradiction is
// reported as a contradiction, never smoothed into one of its two halves.
func chatRowState(row compose.Row) string {
	switch {
	case row.Killed && row.Kind.IsAddressable():
		return "killed-but-live"
	case row.Kind == compose.Booting:
		// A booting chat HAS a socket and already answers chat_inject by
		// name. Excluding it made chat_ls report a chat that exists as
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
}

func (current *backend) callerForRequest(
	ctx context.Context,
	meta map[string]any,
) (callerIdentity, error) {
	raw, present := meta["threadId"]
	if !present {
		return callerIdentity{}, nil
	}
	caller := callerIdentity{present: true}
	threadID, ok := raw.(string)
	if !ok || strings.TrimSpace(threadID) == "" ||
		strings.TrimSpace(threadID) != threadID || containsControl(threadID) {
		caller.detail = "MCP _meta.threadId must be a non-empty thread id without whitespace or control characters"
		return caller, nil
	}
	listed, err := current.list(ctx, LSInput{All: true})
	if err != nil {
		return caller, fmt.Errorf("resolve MCP caller thread %q: list live chats: %w", threadID, err)
	}
	var match ChatRow
	count := 0
	for index := range listed.Rows {
		row := &listed.Rows[index]
		if row.ID != threadID || row.Engine != pfmengine.Codex || row.Killed ||
			row.Kind != compose.LiveCodex.String() ||
			row.Session == "" || row.Socket == "" || row.Pane == "" {
			continue
		}
		match = *row
		count++
	}
	if count == 0 {
		caller.detail = fmt.Sprintf("MCP _meta.threadId %q has no live Codex tmux seat", threadID)
		return caller, nil
	}
	if count != 1 {
		caller.detail = fmt.Sprintf("MCP _meta.threadId %q matched %d live Codex tmux seats", threadID, count)
		return caller, nil
	}
	socketPath, err := current.paths.SocketUnder(match.Socket)
	if err != nil {
		caller.detail = fmt.Sprintf("MCP _meta.threadId %q has an invalid tmux socket: %v", threadID, err)
		return caller, nil
	}
	caller.row = match
	caller.identity = resolve.Identity{
		Session:    match.Session,
		SocketPath: socketPath,
		SocketName: filepath.Base(socketPath),
		Pane:       match.Pane,
		Engine:     string(match.Engine),
		ID:         match.ID,
		Source:     "mcp-thread-meta",
	}
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

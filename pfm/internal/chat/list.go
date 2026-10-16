package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/tmux"
)

// ListRequest selects fleet rows: one compose view, an optional Project filter
// — a case-insensitive substring of a row's project label OR its directory, so
// "professor" and "/home/x/.professor" select the same rows — and a row cap
// (0 keeps every matched row).
//
// Repo, when set, is a repository root (gitroot.RepoRoot): only rows whose cwd
// resolves to it are listed, the rest counted as Elsewhere — so a chat in a
// linked worktree lists from the repo root and the reverse. LiveOnly keeps
// addressable, unkilled chats and counts the killed ones in KilledCount.
// ReadOnly scans without the picker's reconciling writes.
type ListRequest struct {
	View     compose.View
	Project  string
	Limit    int
	Repo     string
	LiveOnly bool
	ReadOnly bool
}

// ListResult is the listed rows plus the counts a capped answer must still
// report: Matched is every row the view and filters selected, Truncated says
// Rows stopped short of it, KilledCount is the scan's killed tally (under
// LiveOnly, the killed live chats skipped), Elsewhere the rows outside Repo.
// Home is the scanned home directory, for rendering a cwd. Herdr holds one
// presence string per row of Rows, in order (herdr.Probe.Presence), which List
// fills and a caller that builds a result by hand may leave empty.
type ListResult struct {
	Rows        []compose.Row
	Herdr       []string
	Matched     int
	Truncated   bool
	KilledCount int
	Elsewhere   int
	Home        string
}

// List is the one fleet listing, behind MCP ls and `pfm chat ls`; each
// entry point only parses its input into a ListRequest and renders the result.
// A writing scan acts like the picker's — a /clear observed here reconciles
// the Codex pane it moved.
func List(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	request ListRequest,
	warn io.Writer,
) (result ListResult, returnErr error) {
	database, err := store.Open(store.WithWarningWriter(warn))
	if err != nil {
		return ListResult{}, fmt.Errorf("chat list: open fleet database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close fleet database: %w", err))
		}
	}()
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: request.View, Runtime: runtime, ReadOnly: request.ReadOnly,
	}, warn)
	if err != nil {
		return ListResult{}, fmt.Errorf("chat list: scan fleet: %w", err)
	}
	result = Select(scan.Output.Rows, scan.Output.KilledCount, request)
	result.Home = scan.Env.Paths.Home
	result.Herdr = rowsPresence(ctx, realHerdrProbe(warn), scan.Env.Paths, result.Rows)
	return result, nil
}

// realHerdrProbe is the probe on the host's own environment, tmux and Herdr
// sockets; every failed lookup is also written to warn.
func realHerdrProbe(warn io.Writer) herdr.Probe {
	return herdr.Probe{
		Getenv:   paths.OSEnv{}.Get,
		LookPath: deps.RealRunner{}.LookPath,
		Tmux:     runTmux,
		Call:     herdr.Call,
		Warn:     warn,
	}
}

// runTmux runs one read against a chat's tmux server within
// gather.ProbeTimeout, as the fleet scan's probe does: a stopped or wedged
// server is this row's error, never a hang of the whole listing.
func runTmux(ctx context.Context, socketPath string, args ...string) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, gather.ProbeTimeout)
	defer cancel()
	var stderr strings.Builder
	command := tmux.Exec(readCtx, "", socketPath, args...)
	command.Stderr = &stderr
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil && ctx.Err() == nil && errors.Is(readCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf(
			"tmux %s: %w within %s: %s", strings.Join(args, " "), gather.ErrServerUnresponsive, gather.ProbeTimeout,
			socketPath,
		)
	}
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}

// rowsPresence is the Herdr presence of each row's chat: the row's tmux server
// socket under values' tmux directory and its session. A row with no socket has
// no live tmux server; a socket that cannot be addressed is the row's error.
func rowsPresence(ctx context.Context, probe herdr.Probe, values paths.Values, rows []compose.Row) []string {
	seats := make([]herdr.Seat, len(rows))
	for index := range rows {
		row := &rows[index]
		if row.Socket == "" {
			continue
		}
		socketPath, err := values.SocketUnder(row.Socket)
		if err != nil {
			seats[index].Err = fmt.Errorf("resolve tmux socket %q of %s: %w", row.Socket, row.Name, err)
			continue
		}
		seats[index] = herdr.Seat{SocketPath: socketPath, Session: row.SessionName, Pane: row.PaneID}
	}
	return probe.Presence(ctx, seats)
}

// PresenceOf is row index's Herdr presence. A result whose presence was never
// read says so: an empty string would read as "no Herdr".
func (result ListResult) PresenceOf(index int) string {
	if index < 0 || index >= len(result.Herdr) {
		return "herdr error: the presence of row " + strconv.Itoa(index) + " was not read"
	}
	return result.Herdr[index]
}

// Select applies a ListRequest's filters to composed rows. The picker's "start
// a new chat" placeholders are never listed: no chat stands behind them. A
// Booting row is a real chat still coming up, and is.
func Select(rows []compose.Row, killedCount int, request ListRequest) ListResult {
	filter := strings.ToLower(strings.TrimSpace(request.Project))
	result := ListResult{KilledCount: killedCount}
	if request.LiveOnly {
		result.KilledCount = 0
	}
	roots := make(map[string]string)
	for index := range rows {
		row := rows[index]
		if placeholderRow(row.Kind) {
			continue
		}
		if request.LiveOnly {
			if !row.Kind.IsAddressable() {
				continue
			}
			if row.Killed || row.NameKilled {
				result.KilledCount++
				continue
			}
		}
		if request.Repo != "" {
			root, found := roots[row.CWD]
			if !found {
				root = gitroot.RepoRoot(row.CWD)
				roots[row.CWD] = root
			}
			if root != request.Repo {
				result.Elsewhere++
				continue
			}
		}
		if filter != "" && !strings.Contains(strings.ToLower(row.Project), filter) &&
			!strings.Contains(strings.ToLower(row.CWD), filter) {
			continue
		}
		result.Matched++
		if request.Limit > 0 && len(result.Rows) >= request.Limit {
			result.Truncated = true
			continue
		}
		result.Rows = append(result.Rows, row)
	}
	return result
}

// placeholderRow is the picker's "start a new chat" action row.
func placeholderRow(kind compose.Kind) bool {
	return kind == compose.NewClaude || kind == compose.NewCodex || kind == compose.NewOpenCode
}

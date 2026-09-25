package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/store"
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
// Home is the scanned home directory, for rendering a cwd.
type ListResult struct {
	Rows        []compose.Row
	Matched     int
	Truncated   bool
	KilledCount int
	Elsewhere   int
	Home        string
}

// List is the one fleet listing, behind MCP chat_ls and `pfm chat ls`; each
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
	return result, nil
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

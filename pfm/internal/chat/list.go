package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/store"
)

// ListRequest selects fleet rows: one compose view, an optional Project filter
// — a case-insensitive substring of a row's project label OR its directory, so
// "professor" and "/home/x/.professor" select the same rows — and a row cap
// (0 keeps every matched row).
type ListRequest struct {
	View    compose.View
	Project string
	Limit   int
}

// ListResult is the listed rows plus the counts a capped answer must still
// report: Matched is every row the view and filter selected, Truncated says
// Rows stopped short of it, KilledCount is the scan's killed tally.
type ListResult struct {
	Rows        []compose.Row
	Matched     int
	Truncated   bool
	KilledCount int
}

// List is the fleet listing behind MCP chat_ls. Its scan writes like the
// picker's — a /clear observed here reconciles the Codex pane it moved — and
// the picker's "start a new chat" placeholders are never listed: no chat
// stands behind them. A Booting row is a real chat still coming up, and is.
func List(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	request ListRequest,
	warn io.Writer,
) (result ListResult, returnErr error) {
	database, err := store.Open(store.WithWarningWriter(warn))
	if err != nil {
		return ListResult{}, err
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close fleet database: %w", err))
		}
	}()
	scan, err := fleet.Scan(ctx, database, fleet.Request{View: request.View, Runtime: runtime}, warn)
	if err != nil {
		return ListResult{}, err
	}
	filter := strings.ToLower(strings.TrimSpace(request.Project))
	result = ListResult{KilledCount: scan.Output.KilledCount}
	for index := range scan.Output.Rows {
		row := scan.Output.Rows[index]
		if placeholderRow(row.Kind) {
			continue
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
	return result, nil
}

// placeholderRow is the picker's "start a new chat" action row.
func placeholderRow(kind compose.Kind) bool {
	return kind == compose.NewClaude || kind == compose.NewCodex || kind == compose.NewOpencode
}

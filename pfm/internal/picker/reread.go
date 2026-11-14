package picker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// enrichFacts fills each row's model, effort and working state from its
// transcript, and names the rows whose facts could not be read.
func enrichFacts(request scanRequest, rows []compose.Row, nowNS int64) ([]compose.Row, string) {
	if request.Facts == nil {
		return rows, ""
	}
	rows, failures := request.Facts.Enrich(rows, nowNS)
	if len(failures) > 0 {
		return rows, fmt.Sprintf("%d unreadable, first: %v", len(failures), failures[0])
	}
	return rows, ""
}

// rereadFacts is the parked picker's busy/idle refresh: the last pass's
// snapshot with only the rows' facts read again. The rows are the composed
// output (so a fact that stopped being readable falls back to the composed
// value, as it does in a full pass) carrying the schedule flags the last pass
// read; the ledger, the schedule flags, the cosmos and the compose are the last
// pass's own. It costs a stat per transcript and reads no store.
func rereadFacts(ctx context.Context, environment fleet.Env, request scanRequest, last sentRefresh) ui.Snapshot {
	rows := slices.Clone(last.output.Rows)
	if len(last.snapshot.Rows) == len(rows) {
		for index := range rows {
			rows[index].ScheduleFired = last.snapshot.Rows[index].ScheduleFired
		}
	}
	snapshot := last.snapshot
	if hasLiveRow(rows) {
		snapshot.Rows, snapshot.FactsError = enrichFacts(request, rows, environment.NowNS)
		var blockedError string
		snapshot.Rows, blockedError = rereadBlocked(ctx, request, snapshot.Rows)
		snapshot.FactsError = strings.Join(
			slices.DeleteFunc([]string{snapshot.FactsError, blockedError}, func(text string) bool {
				return text == ""
			}),
			"; ",
		)
	}
	snapshot.ThemePalette, snapshot.ThemeError = pickerTheme(environment.Paths.Home)
	snapshot.NowNS = environment.NowNS
	return snapshot
}

// rereadBlocked refreshes each native Herdr row's blocked mark, one pane.list
// per socket: a question answered while the picker is parked drops its mark
// within a park poll, as a finished turn drops its working state. A socket
// that could not be read keeps its rows' composed marks and is named in the
// returned error text, never read as "not blocked".
func rereadBlocked(ctx context.Context, request scanRequest, rows []compose.Row) ([]compose.Row, string) {
	if request.HerdrBlocked == nil {
		return rows, ""
	}
	read := map[string]map[string]bool{}
	var failures []string
	out := slices.Clone(rows)
	for index := range out {
		row := &out[index]
		if !compose.NativeHerdr(*row) || row.Socket == "" || row.PaneID == "" {
			continue
		}
		panes, seen := read[row.Socket]
		if !seen {
			var err error
			panes, err = request.HerdrBlocked(ctx, row.Socket)
			if err != nil {
				failures = append(failures, fmt.Sprintf("herdr blocked marks %s: %v", row.Socket, err))
			}
			read[row.Socket] = panes
		}
		if panes != nil {
			row.Blocked = panes[row.PaneID]
		}
	}
	return out, strings.Join(failures, "; ")
}

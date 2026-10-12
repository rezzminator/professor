package picker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// buildSnapshot is the picker's view of one composed pass: rows, account
// chrome, and the cosmos over the comms ledger.
func buildSnapshot(
	ctx context.Context,
	environment fleet.Env,
	request scanRequest,
	output compose.Output,
) ui.Snapshot {
	cosmos := compose.BuildCosmos(output.Rows, nil, environment.NowNS, true)
	var cosmosEvents []fleetdb.CommsEvent
	if request.Comms != nil {
		events, err := request.Comms.CommsSince(
			ctx,
			environment.NowNS-int64(compose.CosmosWindow),
			compose.CosmosEventCap,
		)
		if err != nil {
			cosmos.Err = fmt.Errorf("read comms ledger: %w", err).Error()
		} else {
			cosmosEvents = events
			cosmos = compose.BuildCosmos(output.Rows, events, environment.NowNS, true)
			if len(events) == compose.CosmosEventCap {
				cosmos.Warnings = append(cosmos.Warnings, compose.CosmosTruncationWarning)
			}
		}
	}
	rows := output.Rows
	scheduleError := ""
	if request.Schedules != nil {
		unseen, err := request.Schedules.UnseenScheduleSessionIDs(ctx)
		if err != nil {
			scheduleError = fmt.Errorf("read schedule flags: %w", err).Error()
		} else {
			// Clone: output.Rows is shared with scanResult.Output.
			rows = slices.Clone(output.Rows)
			for index := range rows {
				if rows[index].ID != "" {
					rows[index].ScheduleFired = unseen[rows[index].ID]
				}
			}
		}
	}
	rows, factsError := enrichFacts(request, rows, environment.NowNS)
	machine := environment.Config
	cacheByAccount := make(map[int]bool)
	for _, account := range machine.AccountIDs() {
		cacheByAccount[account] = machine.EffectiveClaude(account).Cache1H
	}
	return ui.Snapshot{
		Rows:                   rows,
		ScheduleError:          scheduleError,
		FactsError:             factsError,
		View:                   request.View,
		KilledCount:            output.KilledCount,
		SuppressedCount:        output.SuppressedCount,
		PrimaryAccount:         environment.Primary,
		AccountIDs:             machine.AccountIDs(),
		AccountEmojis:          machine.AccountEmojis(),
		CodexPrimaryAccount:    machine.PrimaryCodexAccount(),
		CodexAccountIDs:        machine.CodexAccountIDs(),
		CodexAccountEmojis:     machine.CodexAccountEmojis(),
		OpenCodePrimaryAccount: machine.PrimaryOpenCodeAccount(),
		OpenCodeAccountIDs:     machine.OpenCodeAccountIDs(),
		Theme:                  machine.Theme,
		Home:                   environment.Paths.Home,
		ProjectDirs:            output.ProjectDirs,
		DirExists:              isDirectory,
		Cache1H:                machine.EffectiveClaude(environment.Primary).Cache1H,
		Cache1HByAccount:       cacheByAccount,
		NowNS:                  environment.NowNS,
		InitialQuery:           request.Query,
		NoSky:                  request.NoSky,
		CosmosSafe:             resolveCosmosSafe(request.Safe, (paths.OSEnv{}).Get("TERM_PROGRAM")),
		Cosmos:                 cosmos,
		CosmosEvents:           cosmosEvents,
	}
}

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
func rereadFacts(environment fleet.Env, request scanRequest, last sentRefresh) ui.Snapshot {
	rows := slices.Clone(last.output.Rows)
	if len(last.snapshot.Rows) == len(rows) {
		for index := range rows {
			rows[index].ScheduleFired = last.snapshot.Rows[index].ScheduleFired
		}
	}
	snapshot := last.snapshot
	snapshot.Rows, snapshot.FactsError = enrichFacts(request, rows, environment.NowNS)
	snapshot.NowNS = environment.NowNS
	return snapshot
}

// sentRefresh is one published pass: the composed output (kept so a parked
// poll can re-read row facts over it without a new gather) and the snapshot
// the picker received.
type sentRefresh struct {
	output   compose.Output
	snapshot ui.Snapshot
}

func sendRefresh(
	ctx context.Context,
	environment fleet.Env,
	request scanRequest,
	data fleet.Data,
	live gather.Snapshot,
	refreshing bool,
	updates chan<- ui.Snapshot,
) (sentRefresh, bool) {
	output := fleet.ComposeFleet(environment, request.View, data, live)
	snapshot := buildSnapshot(ctx, environment, request, output)
	snapshot.Refreshing = refreshing
	return sentRefresh{output: output, snapshot: snapshot}, publishSnapshot(ctx, snapshot, updates)
}

// publishSnapshot hands the picker one snapshot, or reports false when the
// picker is going away.
func publishSnapshot(ctx context.Context, snapshot ui.Snapshot, updates chan<- ui.Snapshot) bool {
	select {
	case updates <- snapshot:
		return true
	case <-ctx.Done():
		return false
	}
}

// sendRefreshFailure publishes a failed pass to the picker: a snapshot that
// carries no fleet, only the stage, the error and when it failed. The picker
// keeps its last good rows and marks them stale; the next snapshot that lands
// clears the mark. It reports false when the picker is going away.
func sendRefreshFailure(
	ctx context.Context,
	updates chan<- ui.Snapshot,
	stage string,
	err error,
	at time.Time,
) bool {
	snapshot := ui.Snapshot{RefreshFailure: &ui.RefreshFailure{
		Stage: strings.TrimSpace(stage),
		Err:   err.Error(),
		AtNS:  at.UnixNano(),
	}}
	return publishSnapshot(ctx, snapshot, updates)
}

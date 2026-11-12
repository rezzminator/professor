package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/tmux"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// BranchPlace places `pfm chat branch`'s fork before any host is made: Herdr
// unless tmux is asked for (chat.backend) or no Herdr server runs. A fork
// placed in Herdr gets its Claude session id fixed here, the id its seat is
// keyed by; only Claude takes one, so a Codex fork placed in Herdr is refused.
// A fork placed in tmux needs tmux on PATH.
func BranchPlace(ctx context.Context, requested string, engine pfmengine.ID) (spawn.BackendChoice, string, error) {
	choice, err := BirthBackend(ctx, "cli.branch", requested)
	if err != nil {
		return choice, "", err
	}
	if choice.Backend != spawn.BackendHerdr {
		if _, err := obs.Runner(deps.RealRunner{}).LookPath(tmux.Binary); err != nil {
			return choice, "", errors.New("tmux is not on PATH")
		}
		return choice, "", nil
	}
	if engine != pfmengine.Claude {
		return choice, "", spawn.Refusal("cli.branch", "a %s branch is not born in Herdr: only Claude fixes a fork's "+
			"session id before birth; set chat.backend to tmux to branch it in tmux", engine)
	}
	forkID, err := claudelaunch.NewSessionID()
	if err != nil {
		return choice, "", fmt.Errorf("fork session id: %w", err)
	}
	return choice, forkID, nil
}

// BranchHerdr births `pfm chat branch`'s fork as a native Herdr seat in a new
// tab of its place's workspace, never in the caller's pane. The plan is a
// Claude fork whose session id was fixed before birth
// (action.HeadlessForkRequest.ForkID), the id the seat is keyed by; its
// lineage is recorded under the seat id, as a tmux fork's is under its own.
// A live fork prints its one line to stdout.
func BranchHerdr(
	ctx context.Context,
	choice spawn.BackendChoice,
	plan action.HeadlessPlan,
	runtime *pfmconfig.Runtime,
	name, parent string,
	stdout, stderr io.Writer,
) (fleetdb.Seat, error) {
	if plan.Engine != pfmengine.Claude || plan.Record == nil || plan.SeatRun == nil {
		return fleetdb.Seat{}, errors.New("a Herdr branch needs a Claude fork with its session id fixed before birth")
	}
	choice.InPlace = false
	result, err := spawn.SpawnHerdr(ctx, spawn.HerdrRequest{
		Choice: choice,
		Paths:  runtime.Paths,
		Run:    *plan.SeatRun,
		Seat:   fleetdb.Seat{SessionID: plan.Record.SessionID, Engine: string(pfmengine.Claude), Name: name},
		Launch: plan.Record,
	})
	launchnotice.Print(stderr, "pfm chat branch", result.Warnings)
	if err != nil {
		return result.Seat, fmt.Errorf("born in Herdr: %w", err)
	}
	state := fleetdb.OpenSharedState(ctx, runtime.Paths)
	recordErr := state.RecordBranchSeat(ctx, result.Seat.SeatID, parent, clock.Real.Now().Unix())
	if err := errors.Join(recordErr, state.Close()); err != nil {
		return result.Seat, fmt.Errorf("fork is live in Herdr pane %s; record its lineage: %w",
			result.Seat.HerdrPane, err)
	}
	plan.Report(stderr, "pfm chat branch")
	fmt.Fprintf(stdout, "Branched %s… as %q into Herdr pane %s (socket %s) — live in pfm ls; "+
		"open it with pfm chat open %q.\n", transcript.Truncate(parent, 8), name, result.Seat.HerdrPane,
		result.Seat.HerdrSocket, name)
	return result.Seat, nil
}

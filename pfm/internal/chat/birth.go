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
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type (
	callerKey struct{}
	placedKey struct{}
)

type birthCaller struct {
	door     string
	identity resolve.Identity
	err      error
	local    bool // identify the process running the door at placement (WithLocalCaller)
}

// WithCaller carries a request's caller, including a failed lookup, to its birth.
func WithCaller(ctx context.Context, door string, identity resolve.Identity, err error) context.Context {
	return context.WithValue(ctx, callerKey{}, birthCaller{door: door, identity: identity, err: err})
}

// BirthBackend places a birth that has no recorded host (placeChat).
func BirthBackend(ctx context.Context, door, requested string) (spawn.BackendChoice, error) {
	return placeChat(ctx, door, requested, "", false, nil)
}

// placeChat is the one placement of every birth and revive: a placement the
// request already made is reused; otherwise the carried caller (with its door)
// or the local process, identified once with Codex seats, the chat's recorded
// Herdr host, whether it revives a stored chat and the Herdr server the
// door's environment names (WithHerdrServer, else this process's) decide
// through spawn.Place.
// A Herdr choice carries the scan of the fleet a new tab is filed under, run
// only when one is born.
func placeChat(
	ctx context.Context,
	door, requested, recorded string,
	revive bool,
	runtime *pfmconfig.Runtime,
) (spawn.BackendChoice, error) {
	if choice, placed := ctx.Value(placedKey{}).(spawn.BackendChoice); placed {
		return choice, nil
	}
	caller, carried := ctx.Value(callerKey{}).(birthCaller)
	if !carried || caller.local {
		caller.identity, caller.err = localCaller(ctx, runtime)
	}
	if caller.door != "" {
		door = caller.door
	}
	server, carried := ctx.Value(serverKey{}).(spawn.HerdrServer)
	if !carried {
		server = spawn.HerdrServerFrom(paths.OSEnv{})
	}
	choice, err := spawn.Place(ctx, door, requested, recorded, revive, caller.identity, caller.err, server)
	if choice.Backend == spawn.BackendHerdr {
		choice.Places = herdrPlacesFor(runtime)
	}
	return choice, err
}

// BirthHerdr creates and reports a native seat without fabricating a tmux handle.
// The boolean preserves launch prompt state when a pane exists or may exist.
func BirthHerdr(ctx context.Context, choice spawn.BackendChoice, plan action.HeadlessPlan, runtime *pfmconfig.Runtime,
	name, prompt string, await, attach bool, stdout, stderr io.Writer, awaitOptions ...headless.AwaitOptions,
) (bool, int) {
	if plan.SeatRun == nil {
		fmt.Fprintln(stderr, "pfm chat new: native birth has no structured recipe")
		return false, 1
	}
	engineID := plan.Engine
	id := ""
	if plan.Record != nil {
		id = plan.Record.SessionID
		if engineID == "" {
			engineID = plan.Record.Engine
		}
	}
	if _, err := pfmengine.Lookup(engineID); err != nil {
		fmt.Fprintf(stderr, "pfm chat new: native engine: %v\n", err)
		return false, 1
	}
	if id == "" {
		var err error
		id, err = claudelaunch.NewSessionID()
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat new: native session identity: %v\n", err)
			return false, 1
		}
	}

	recipe := *plan.SeatRun
	// Tmux birth submits Codex's prompt after launch; a native seat has no
	// tmux submission phase, so carry it in the engine's positional argv.
	if engineID == pfmengine.Codex && !plan.PromptOnCommandLine && prompt != "" {
		recipe.Args = append(append([]string(nil), recipe.Args...), "--", prompt)
	}
	result, err := spawn.SpawnHerdr(ctx, spawn.HerdrRequest{
		Choice: choice,
		Paths:  runtime.Paths,
		Run:    recipe,
		Seat:   fleetdb.Seat{SessionID: id, Engine: string(engineID), Name: name},
		Launch: plan.Record,
		Comms: &fleetdb.CommsEvent{
			AtNS:    clock.Real.Now().UnixNano(),
			Kind:    fleetdb.KindSpawn,
			Target:  name,
			Message: prompt,
		},
	})
	born := result.Seat.SeatID != ""
	launchnotice.Print(stderr, "pfm chat new", result.Warnings)
	if err = CommitWorkbenchLaunch(ctx, born, name, err); err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return born, 1
	}
	plan.Report(stderr, "pfm chat new")
	report := stdout
	if await {
		report = stderr
	}
	fmt.Fprintf(
		report,
		"%s\t%s\therdr\t%s\t%s\tstarting\n",
		engineID,
		name,
		result.Seat.HerdrSocket,
		result.Seat.HerdrPane,
	)
	if await {
		options := headless.AwaitOptions{}
		if len(awaitOptions) > 0 {
			options = awaitOptions[0]
		}
		turn, err := headless.Await(ctx, func(ctx context.Context) (headless.Chat, bool, error) {
			return nativeBirthTarget(ctx, result.Seat, runtime)
		}, options)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat new: native await: %v\n", err)
			switch {
			case errors.Is(err, headless.ErrTrustDialog):
				return true, 6
			case errors.Is(err, headless.ErrChatGone):
				return true, 3
			case errors.Is(err, headless.ErrAwaitTimeout):
				return true, 5
			default:
				return true, 1
			}
		}
		if turn.Error != "" {
			fmt.Fprintf(stderr, "pfm chat new: native turn: %s\n", turn.Error)
			return true, 1
		}
		if _, err := fmt.Fprintln(stdout, turn.Answer); err != nil {
			fmt.Fprintf(stderr, "pfm chat new: print native answer: %v\n", err)
			return true, 1
		}
	}
	if attach {
		if err := viewNative(ctx, result.Seat, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
			return true, 1
		}
	}
	RecordVerb(ctx, "new", 0)
	return true, 0
}

// LaunchSessionID preserves engines whose planner has no launch record.
func LaunchSessionID(record *fleetdb.Launch) string {
	if record == nil {
		return ""
	}
	return record.SessionID
}

// Resolve through the durable seat ID: reporters may replace a raw planner's
// provisional session identity before its first transcript appears.
func nativeBirthTarget(
	ctx context.Context,
	initial fleetdb.Seat,
	runtime *pfmconfig.Runtime,
) (target headless.Chat, found bool, returnErr error) {
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	seat, found, err := db.Seat(ctx, initial.SeatID)
	if err != nil || !found {
		return target, found, err
	}
	seat, alive, err := nativeAlive(ctx, seat, runtime.Paths)
	if errors.Is(err, gather.ErrHerdrProofPersistence) {
		// The launching seat-host may publish its birth during our proof.
		// Reprove the current row once; never turn a CAS refusal into death.
		seat, alive, err = nativeAlive(ctx, seat, runtime.Paths)
	}
	if err != nil {
		return target, false, fmt.Errorf("prove native await seat %s: %w", seat.SeatID, err)
	}
	target = headless.Chat{
		ID: seat.SessionID, SeatID: seat.SeatID, Engine: pfmengine.ID(seat.Engine),
		Backend: spawn.BackendHerdr, Name: seat.Name, Socket: seat.HerdrSocket,
		Pane: seat.HerdrPane, Live: alive, HerdrWorkspace: seat.HerdrWorkspace,
	}
	if err := CheckAwait(ctx, target, runtime); err != nil {
		return target, true, err
	}
	if !alive {
		return target, true, nil
	}
	if target.Engine == pfmengine.Claude {
		index, err := store.Open()
		if err != nil {
			return target, true, err
		}
		defer func() { returnErr = errors.Join(returnErr, index.Close()) }()
		record, indexed, err := index.Transcript(ctx, seat.SessionID)
		if err != nil {
			return target, true, err
		}
		if indexed {
			target.Path, target.CWD = record.Path, record.CWD
			return target, true, nil
		}
	}
	rows, err := Rows(ctx, io.Discard, runtime)
	if err != nil {
		return target, true, err
	}
	for i := range rows {
		row := &rows[i]
		if row.SeatID == seat.SeatID {
			target.Path, target.CWD = row.Path, row.CWD
			break
		}
	}
	// No transcript yet is a live startup seat, not an absent conversation.
	return target, true, nil
}

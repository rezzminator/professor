package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// BirthBackend adapts a verified request-local caller to the backend selector.
func BirthBackend(ctx context.Context, requested string, env paths.Env) (spawn.BackendChoice, error) {
	var requester *fleetdb.Seat
	if self, ok := ScopedSelf(ctx); ok {
		backend := self.Backend
		if backend == "" {
			backend = spawn.BackendTmux
		}
		requester = &fleetdb.Seat{
			Backend:        backend,
			HerdrSocket:    self.Socket,
			HerdrPane:      self.Pane,
			HerdrWorkspace: self.HerdrWorkspace,
		}
	}
	var environ []string
	for _, key := range []string{"TMUX", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		environ = append(environ, key+"="+env.Get(key))
	}
	return spawn.ChooseBackend(ctx, requested, spawn.BackendEnvironment(ctx, environ), requester)
}

// BirthHerdr creates and reports a native seat without fabricating a tmux handle.
// The boolean preserves launch prompt state when a pane exists or may exist.
func BirthHerdr(ctx context.Context, choice spawn.BackendChoice, plan action.HeadlessPlan, runtime *pfmconfig.Runtime,
	name, prompt string, await, attach bool, stdout, stderr io.Writer,
) (bool, int) {
	if await || attach {
		fmt.Fprintln(
			stderr,
			"pfm chat new: native Herdr --await/--attach are not supported in phase 1; omit them (the new tab stays unfocused)",
		)
		return false, 2
	}
	if plan.SeatRun == nil || plan.Record == nil {
		fmt.Fprintln(stderr, "pfm chat new: native birth has no structured recipe or session identity")
		return false, 1
	}
	if err := fleetdb.RecordLaunch(ctx, runtime.Paths, *plan.Record, clock.Real.Now().Unix()); err != nil {
		fmt.Fprintf(stderr, "pfm chat new: record native launch %s: %v\n", plan.Record.SessionID, err)
	}
	result, err := spawn.SpawnHerdr(ctx, spawn.HerdrRequest{
		Choice: choice, Paths: runtime.Paths, Run: *plan.SeatRun,
		Seat: fleetdb.Seat{SessionID: plan.Record.SessionID, Engine: string(plan.Record.Engine), Name: name},
	})
	born := result.Seat.SeatID != ""
	if err = CommitWorkbenchLaunch(ctx, born, name, err); err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return born, 1
	}
	state := fleetdb.OpenSharedState(ctx, runtime.Paths)
	err = errors.Join(state.RecordComms(ctx, fleetdb.CommsEvent{
		AtNS: clock.Real.Now().UnixNano(), Kind: fleetdb.KindSpawn, Target: name,
		ReceiverSocket: result.Seat.HerdrSocket, ReceiverPane: result.Seat.HerdrPane, Message: prompt,
	}), state.Close())
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: native seat is starting; comms ledger: %v\n", err)
	}
	plan.Report(stderr, "pfm chat new")
	fmt.Fprintf(
		stdout,
		"%s\t%s\therdr\t%s\t%s\tstarting\n",
		plan.Record.Engine,
		name,
		result.Seat.HerdrSocket,
		result.Seat.HerdrPane,
	)
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

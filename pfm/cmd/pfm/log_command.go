package main

import (
	"context"
	"io"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// openActivityLog is the process entry of the activity log: it names the verb
// being run and the build, hands over the machine's log policy, and returns
// the function run() calls with its exit code.
func openActivityLog(args []string, runtime commandRuntime, stderr io.Writer) func(exitCode int) {
	machine := runtime.Config.Log
	_, finish := obs.OpenLog(context.Background(), obs.Settings{
		Cmd: obs.Verb(
			args,
		),
		Version:    config.DisplayVersion(version),
		Level:      machine.Level,
		Components: machine.Components,
		KeepFiles:  machine.KeepFiles,
		MaxMB:      machine.MaxMB,
		KeepDays:   machine.KeepDays,
		Stderr:     stderr,
	})
	return finish
}

// runLog is `pfm log`: the filter over this home's activity file.
func runLog(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return obs.ReadActivity(context.Background(), args, stdout, stderr, runtime.Paths.LogFile, clock.Real)
}

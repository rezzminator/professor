package main

import (
	"context"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
)

// runCodex forwards `pfm codex ARGS…` to `engine-sync codex ARGS…` for the one
// release the forwarders live: same arguments, same output, same exit code,
// plus one stderr line naming the engine-sync command. engine-sync is the
// single writer of the Codex mirror and the global roles.
func runCodex(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return forwardEngineSync(enginesync.EngineCodex, args, stdout, stderr, runtime)
}

// forwardEngineSync runs engine-sync under this process's resolved home, so a
// forwarded --home default means what pfm's own default meant.
func forwardEngineSync(engine string, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	runner := enginesync.Runner{
		Home:     runtime.Paths.Home,
		ChildEnv: append(os.Environ(), "HOME="+runtime.Paths.Home),
	}
	return runner.Forward(context.Background(), "pfm "+engine, append([]string{engine}, args...), stdout, stderr)
}

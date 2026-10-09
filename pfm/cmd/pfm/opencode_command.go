package main

import (
	"io"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
)

// runOpenCode forwards `pfm opencode ARGS…` to `engine-sync opencode ARGS…`
// for the one release the forwarders live (runCodex).
func runOpenCode(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return forwardEngineSync(enginesync.EngineOpenCode, args, stdout, stderr, runtime)
}

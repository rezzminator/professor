package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// runInstallRollback is `pfm install --rollback ID [--force]`: it replays the
// install journal id backwards (installer.RollbackLayout). An unknown id is a
// usage error, exit 2; every refusal and failure exits 1.
func runInstallRollback(id string, force bool, stdout, stderr io.Writer, runtimes []commandRuntime) int {
	runtime, err := pfmconfig.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: resolve dependency config: %v\n", err)
		return 1
	}
	env, err := installer.NewLayoutEnv(runtime, paths.OSEnv{})
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: layout environment: %v\n", err)
		return 1
	}
	if err := installer.RollbackLayout(context.Background(), env, id, force, stdout); err != nil {
		fmt.Fprintf(stderr, "pfm install: rollback: %v\n", err)
		if strings.Contains(err.Error(), "unknown layout journal") {
			return 2
		}
		return 1
	}
	return 0
}

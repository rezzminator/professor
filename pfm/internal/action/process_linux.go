//go:build linux

package action

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// nativeProcesses is unreachable on Linux, where /proc always exists. It stays
// defined so process.go needs no build tag of its own.
func nativeProcesses(context.Context) ([]Process, error) {
	return nil, fmt.Errorf("read process table: %s is unavailable", paths.SystemProcRoot())
}

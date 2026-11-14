//go:build jail

package picker

import (
	"context"
	"fmt"

	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
)

// clearRetryIndexRunner fails the third index run, the one after a clear; only
// refresh_codex_clear_retry_jail_test.go drives it. It holds no test, so its
// jail tag needs no _jail_test.go name (test-kinds counts tests by file name).
type clearRetryIndexRunner struct{ calls int }

func (runner *clearRetryIndexRunner) Run(context.Context, fleetindex.Options) (fleetindex.Counters, error) {
	runner.calls++
	if runner.calls == 3 {
		return fleetindex.Counters{}, fmt.Errorf("injected post-clear index failure")
	}
	return fleetindex.Counters{}, nil
}

//go:build linux || darwin

package run

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
)

const processWaitAfterCancel = 500 * time.Millisecond

func runProcess(
	ctx context.Context,
	runner deps.Runner,
	argv []string,
	options deps.StartOptions,
) error {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	process, err := runner.Start(ctx, argv, options)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killErr := process.KillGroup()
		// RealRunner maps options.WaitDelay to exec.Cmd.WaitDelay. Waiting for
		// the command here is deliberate: the command closes inherited pipes
		// before Wait returns, so callers can read their buffers without racing
		// the os/exec copy goroutines. A runner that does not honor WaitDelay is
		// a broken process seam and must not make the caller read live buffers.
		waitErr := <-done
		if killErr != nil {
			return errors.Join(waitErr, fmt.Errorf("kill headless process group: %w", killErr))
		}
		return waitErr
	}
}

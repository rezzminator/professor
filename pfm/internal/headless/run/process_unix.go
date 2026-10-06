//go:build linux || darwin

package run

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const processWaitAfterCancel = 500 * time.Millisecond

func runProcess(
	ctx context.Context,
	runner deps.Runner,
	argv []string,
	options deps.StartOptions,
) (returnErr error) {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	guard, err := gather.AcquireAccountGuard(claudelaunch.ConfigDirFromEnv(options.Env), true)
	if err != nil {
		return err
	}
	process, err := runner.Start(ctx, argv, options)
	if err != nil {
		return errors.Join(err, guard.Abort(), guard.Close())
	}
	if err := guard.Record(process.Pid()); err != nil {
		killErr := process.KillGroup()
		if killErr == nil {
			return errors.Join(err, process.Wait(), guard.Abort(), guard.Close())
		}
		return errors.Join(err, killErr, guard.Close())
	}
	if err := guard.Close(); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, guard.Abort()) }()
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

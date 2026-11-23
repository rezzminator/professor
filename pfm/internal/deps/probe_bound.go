package deps

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// effectiveTimeout is the single truth for the bound a probe actually ran
// under, so the duration named in a timeout report is the one enforced rather
// than the one requested.
func effectiveTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return ProbeTimeout
	}
	return timeout
}

// boundedOutputWithEnvironment runs path+args through runner (nil defaults to
// a bare RealRunner{} — see ProbeOptions.Runner's doc comment for why this
// package cannot default to the observed door itself) and joins stdout with
// stderr, the same shape exec.Cmd.CombinedOutput gave every caller before
// this crossed the Runner seam. runner.Run folds an ordinary non-zero exit
// into RunResult.ExitCode rather than an error (deps.Runner's documented
// contract), so a completed-but-failing command is reported back here as a
// runnerExitStatus — the *exec.ExitError-shaped signal probeOne and
// probeSelfDoctor already classify on.
func boundedOutputWithEnvironment(
	parent context.Context,
	bound clock.Clock,
	timeout time.Duration,
	environment []string,
	runner Runner,
	path string,
	args ...string,
) ([]byte, error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if bound != nil {
		ctx, cancel = clock.WithTimeout(parent, bound, effectiveTimeout(timeout))
	} else {
		ctx, cancel = context.WithTimeout(parent, effectiveTimeout(timeout))
	}
	defer cancel()
	if runner == nil {
		runner = RealRunner{}
	}
	result, err := runner.Run(ctx, append([]string{path}, args...), RunOptions{Env: environment})
	output := make([]byte, 0, len(result.Stdout)+len(result.Stderr))
	output = append(output, result.Stdout...)
	output = append(output, result.Stderr...)
	if ctx.Err() != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return output, probeContextError{err: parentErr}
		}
		return output, probeContextError{err: context.DeadlineExceeded, ownTimeout: true}
	}
	if err != nil {
		return output, err
	}
	if result.ExitCode != 0 {
		return output, runnerExitStatus{exitCode: result.ExitCode}
	}
	return output, nil
}

type probeContextError struct {
	err        error
	ownTimeout bool
}

func (err probeContextError) Error() string { return err.err.Error() }

func (err probeContextError) Unwrap() error { return err.err }

func parentContextError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled by parent context"
	case errors.Is(err, context.DeadlineExceeded):
		return "parent context deadline exceeded before probe timeout"
	default:
		return fmt.Sprintf("probe stopped by parent context: %v", err)
	}
}

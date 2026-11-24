package reload

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// onFake gives options a fake clock unless the test already chose one, and
// returns it; a test that kept its own non-Fake clock is run straight through.
func onFake(options *Options) *clock.Fake {
	if options.Clock == nil {
		options.Clock = clock.NewFake(time.Unix(0, 0))
	}
	fake, _ := options.Clock.(*clock.Fake)
	return fake
}

// runOnFake is Run on a fake clock: every wait the reboot registers (the
// polls, the fixed render and submit pauses) is stepped by testwait.Drive, so
// no test waits a real interval for one.
func runOnFake(
	t *testing.T, ctx context.Context, request Request, options Options, tmux Tmux, proc Process, stderr io.Writer,
) (result Result, err error) {
	t.Helper()
	fake := onFake(&options)
	if fake == nil {
		return Run(ctx, request, options, tmux, proc, stderr)
	}
	testwait.Drive(t, "reload to run through its waits", fake, func() {
		result, err = Run(ctx, request, options, tmux, proc, stderr)
	})
	return result, err
}

// runHerdrOnFake is RunHerdr on a fake clock, as runOnFake is for Run.
func runHerdrOnFake(
	t *testing.T, ctx context.Context, request Request, options Options, stderr io.Writer,
) (result Result, err error) {
	t.Helper()
	fake := onFake(&options)
	if fake == nil {
		return RunHerdr(ctx, request, options, stderr)
	}
	testwait.Drive(t, "native reload to run through its waits", fake, func() {
		result, err = RunHerdr(ctx, request, options, stderr)
	})
	return result, err
}

// deliverThenOnFake is deliverThen on a fake clock, as runOnFake is for Run.
func deliverThenOnFake(
	t *testing.T, ctx context.Context, request Request, options Options, tmux Tmux, proc Process, stderr io.Writer,
) (err error) {
	t.Helper()
	fake := onFake(&options)
	if fake == nil {
		return deliverThen(ctx, request, options, tmux, proc, stderr)
	}
	testwait.Drive(t, "--then delivery to run through its waits", fake, func() {
		err = deliverThen(ctx, request, options, tmux, proc, stderr)
	})
	return err
}

package deps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestBoundedOutputEndsAtItsBoundOnTheGivenClock: a probe child that never
// answers is ended by the probe's own bound, timed on the clock handed in:
// not one step before the bound, and at it the error is the probe's own
// timeout, told apart from a parent that stopped first.
func TestBoundedOutputEndsAtItsBoundOnTheGivenClock(t *testing.T) {
	for _, parentStops := range []bool{false, true} {
		runner := &FakeRunner{}
		entered := make(chan struct{})
		runner.Handle([]string{"/bin/hung"}, func(ctx context.Context, _ RunCall) (RunResult, error) {
			close(entered)
			<-ctx.Done()
			return RunResult{ExitCode: -1}, ctx.Err()
		})
		fake := clock.NewFake(time.Unix(0, 0))
		parent, stopParent := context.WithCancel(context.Background())
		returned := make(chan error, 1)
		go func() {
			_, err := boundedOutputWithEnvironment(parent, fake, ProbeTimeout, nil, runner, "/bin/hung", "--version")
			returned <- err
		}()
		testwait.Receive(t, "the hung probe child to start", entered)
		fake.Advance(ProbeTimeout - time.Nanosecond)
		if len(returned) != 0 || fake.Pending() != 1 {
			t.Fatalf(
				"parent stops %v: the bound ended %s before %s (%s)", parentStops, time.Nanosecond, ProbeTimeout, fake,
			)
		}
		if parentStops {
			stopParent()
		} else {
			fake.Advance(time.Nanosecond)
		}
		err, _ := testwait.Receive(t, "the bounded probe to return", returned)
		stopParent()
		var termination probeContextError
		if !errors.As(err, &termination) || termination.ownTimeout == parentStops {
			t.Fatalf(
				"parent stops %v: err %#v, want probeContextError with ownTimeout %v", parentStops, err, !parentStops,
			)
		}
		want := context.DeadlineExceeded
		if parentStops {
			want = context.Canceled
		}
		if !errors.Is(termination.err, want) {
			t.Fatalf("parent stops %v: cause %v, want %v", parentStops, termination.err, want)
		}
	}
}

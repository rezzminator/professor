package installer

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestPluginCommandEndsAtItsDeadlineOnTheGivenTimer: a plugin command that
// never answers runs in its own process group with the kill grace, is ended at
// its deadline on the timer handed in and not one step before, and names the
// deadline; a caller that stops first is named a cancellation instead.
func TestPluginCommandEndsAtItsDeadlineOnTheGivenTimer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		parentStops bool
		want        string
	}{
		{"deadline", false, "plugin marketplace add x: timed out after 2m0s: context deadline exceeded"},
		{"caller stops", true, "plugin marketplace add x: cancelled: context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			entered := make(chan deps.RunOptions, 1)
			runner.Handle([]string{"claude"}, func(ctx context.Context, call deps.RunCall) (deps.RunResult, error) {
				entered <- call.Opts
				<-ctx.Done()
				return deps.RunResult{ExitCode: -1}, ctx.Err()
			})
			fake := clock.NewFake(time.Unix(0, 0))
			parent, stop := context.WithCancel(context.Background())
			defer stop()
			returned := make(chan error, 1)
			go func() {
				returned <- runPluginCommandWithin(parent, fake, runner,
					[]string{"claude", "plugin", "marketplace", "add", "x"}, deps.RunOptions{}, claudePluginCommandTimeout)
			}()
			options, _ := testwait.Receive(t, "the hung plugin command to start", entered)
			if !options.ProcessGroup || options.WaitDelay != claudePluginWaitDelay {
				t.Fatalf("run options = %+v, want its own process group and a %s grace", options, claudePluginWaitDelay)
			}
			fake.Advance(claudePluginCommandTimeout - time.Nanosecond)
			if len(returned) != 0 || fake.Pending() != 1 {
				t.Fatalf(
					"the deadline ended the command %s before %s (%s)",
					time.Nanosecond,
					claudePluginCommandTimeout,
					fake,
				)
			}
			if tc.parentStops {
				stop()
			} else {
				fake.Advance(time.Nanosecond)
			}
			err, _ := testwait.Receive(t, "the plugin command to return", returned)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

package hookentry

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// runningWatch is one watch process played in a goroutine with its own clock.
type runningWatch struct {
	done   chan struct{}
	rc     int
	stderr bytes.Buffer
	fake   *clock.Fake
	poll   time.Duration
}

// start plays the spawned argv the way the detached child runs it.
func (rig *watchRig) start(argv []string, poll time.Duration) *runningWatch {
	rig.t.Helper()
	if len(argv) < 3 || !slices.Equal(argv[:3], []string{"internal", "herdr-state", herdrWatchVerb}) {
		rig.t.Fatalf("spawned argv = %q, want internal herdr-state watch …", argv)
	}
	deps := rig.watchDeps()
	watch := &runningWatch{done: make(chan struct{}), fake: rig.fake, poll: poll}
	go func() {
		defer close(watch.done)
		watch.rc = watchForHerdr(argv[3:], &watch.stderr, deps, poll)
	}()
	return watch
}

// wait lets the watch poll on until it ends by itself.
func (watch *runningWatch) wait(t *testing.T) {
	t.Helper()
	testwait.Drive(t, "the watch to end", watch.fake, func() { <-watch.done })
}

// parked waits until the watch sleeps on its next poll, every check of the
// last pass done, or has ended, and reports whether it has.
func (watch *runningWatch) parked(t *testing.T) (ended bool) {
	t.Helper()
	testwait.Until(t, "the watch to sleep on its next poll or end", func() (bool, string) {
		select {
		case <-watch.done:
			ended = true
			return true, ""
		default:
		}
		return watch.fake.Pending() > 0, watch.fake.String()
	})
	return ended
}

// polls lets the watch run n polls and leaves it asleep on the next one (or ended).
func (watch *runningWatch) polls(t *testing.T, n int) {
	t.Helper()
	for range n {
		if watch.parked(t) {
			return
		}
		watch.fake.Advance(watch.poll)
	}
	watch.parked(t)
}

// pollsToEnd polls the watch on until it ends and returns how many polls that took.
func (watch *runningWatch) pollsToEnd(t *testing.T) int {
	t.Helper()
	polled := 0
	for !watch.parked(t) {
		watch.fake.Advance(watch.poll)
		polled++
	}
	return polled
}

// waitClaimed waits for the started watch to claim its record with its own
// pid and command (the rig's pid 100, pfm) and to sleep on its first poll: the
// claim is written under the activity lock, so a record on disk alone may
// still have the lock held, and a hook or holder taking it then would park on
// a retry no clock of the test moves. The poll's sleep is the only wait on
// rig.fake, so a pending one is the pass done and the lock let go.
func (rig *watchRig) waitClaimed(argv []string) {
	rig.t.Helper()
	claimed := flagValue(rig.t, argv, "--seq") + " 100 pfm"
	testwait.Until(rig.t, "the watch to claim its record as "+claimed+" and sleep on its poll", func() (bool, string) {
		data, err := os.ReadFile(rig.record())
		return err == nil && strings.TrimSpace(string(data)) == claimed && rig.fake.Pending() > 0,
			fmt.Sprintf("record = %q (%v); %s", data, err, rig.fake)
	})
}

// Package testwait is the one wait door of pfm's tests and the one place a
// test-support file reaches the real clock: every test that must wait for
// something outside its own goroutine waits here, on a named condition, and
// nowhere else.
//
//   - Until: poll a named predicate until it holds; UntilErr, the same for code
//     that holds no testing.TB, returning the failure as an error.
//   - Receive: receive from a channel, value or close.
//   - Blocked: a predicate for Until, true once a goroutine is blocked at a
//     named call: the presence that proves code reached the point where it
//     would have acted, beside an assertion that it did not act.
//   - Goroutines: a parsed stack dump, for Blocked, Drive and the
//     real-wait tripwire of internal/testjail.
//   - Drive: run a call that waits on a clock.Fake, advancing the fake to
//     each wait it parks on until the call returns.
//   - ProcessClock: run the whole process on a fake clock for one test, for
//     code under test that reads the process-wide clock.Real; SwapProcessClock,
//     the same swap for internal/testjail's real-wait tripwire.
//   - Context: a context for code under test that must not hang.
//   - Deadline: a context the test ends itself with context.DeadlineExceeded.
//   - Stopwatch: a real elapsed time, recorded and never compared.
//   - Expired: a context already past its deadline, for the code path a
//     caller's expired deadline takes, reached without waiting for one.
//   - Watchdog: the test process's own diagnosis timer, armed Margin before
//     the runner's deadline (internal/testjail's profiler alone).
//   - HungLimit, Margin: the one limit, and how far ahead of a runner
//     deadline the door acts.
//   - ProcessPace, ProcessTries: the pace and the number of tries a test
//     hands production code that polls a real process (a jailed tmux pane).
//
// One wall-clock limit bounds them all: HungLimit, cut short by the runner's
// own -timeout (t.Deadline). It turns a real hang into a failure naming the
// condition and the last state observed; it never judges speed, and no caller
// passes a duration of its own.
//
// This file holds every wait and the one limit; the other file:
//   - stacks.go: the parsed stack dump under Blocked, Drive's family rule
//     and Goroutines.
package testwait

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// HungLimit is the only wall-clock limit in pfm's tests: a wait that has not
// ended after it is hung, never slow.
const HungLimit = 2 * time.Minute

// ProcessPace and ProcessTries are the pace and the number of tries a test
// hands production code that polls a real process (a jailed tmux pane, a
// child): ProcessTries polls at ProcessPace fill HungLimit, so a wait that
// must succeed gives up only once the test is hung, never on a slow machine.
// A wait that must give up keeps its own small count, against a fixture that
// provably never answers.
const (
	ProcessPace  = 50 * time.Millisecond
	ProcessTries = int(HungLimit / ProcessPace)
)

// pollFirst and pollMost bound the pause between two looks of Until: the
// first look comes at once, then the pause doubles up to pollMost. They pace
// the polling only; neither decides an outcome.
const (
	pollFirst = time.Millisecond
	pollMost  = 50 * time.Millisecond
)

// deadliner is the part of *testing.T (and *testing.F) that knows the
// runner's -timeout; testing.TB does not carry it.
type deadliner interface {
	Deadline() (time.Time, bool)
}

// Until polls check until it reports met, failing t with the condition's name
// and the last state check returned once the hung-only limit passes. check
// runs on the caller's goroutine; Until must be called from the test's own
// goroutine, as t.Fatalf requires.
func Until(t testing.TB, condition string, check func() (met bool, state string)) {
	t.Helper()
	untilBounded(t, condition, check, limit(t, time.Now()))
}

func untilBounded(t testing.TB, condition string, check func() (bool, string), bound time.Duration) {
	t.Helper()
	poll(t, condition, func() (bool, bool, string) {
		met, state := check()
		return met, false, state
	}, bound, nil)
}

// UntilErr is Until for code that holds no testing.TB (a testscript command, a helper process): it
// polls check at the same pacing until it reports met, and returns an error naming the condition
// and the last state check returned once the hung-only limit passes; nil once met.
func UntilErr(condition string, check func() (met bool, state string)) error {
	return untilErrBounded(condition, check, HungLimit)
}

func untilErrBounded(condition string, check func() (bool, string), bound time.Duration) error {
	return pollErr(condition, func() (bool, bool, string) {
		met, state := check()
		return met, false, state
	}, bound, nil)
}

// poll is the loop under Until and Drive: pollErr, failing t with its error.
func poll(
	t testing.TB,
	condition string,
	check func() (met, moved bool, state string),
	bound time.Duration,
	wake <-chan struct{},
) {
	t.Helper()
	if err := pollErr(condition, check, bound, wake); err != nil {
		t.Fatalf("%s", err)
	}
}

// pollErr looks at once, then after a pause that doubles up to pollMost, held at pollFirst while a
// look reports the subject still moving, until a look reports met (nil) or bound passes (an error
// naming the condition, the bound and the last state). A receive on wake (nil: none) ends a pause
// early, so the look after it comes at once.
func pollErr(
	condition string,
	check func() (met, moved bool, state string),
	bound time.Duration,
	wake <-chan struct{},
) error {
	expiry := time.NewTimer(bound)
	defer expiry.Stop()
	pause := pollFirst
	for {
		met, moved, _ := check()
		if met {
			return nil
		}
		if moved {
			pause = pollFirst
		}
		select {
		case <-expiry.C:
			met, _, state := check()
			if met {
				return nil
			}
			return fmt.Errorf(
				"testwait: %s: not met within the hung-only limit (%s); last state: %s",
				condition,
				bound,
				state,
			)
		case <-time.After(pause):
		case <-wake:
		}
		pause = min(pause*2, pollMost)
	}
}

// Receive receives once from ch, a value or the channel's close, and returns
// what a comma-ok receive returns. It fails t with the condition's name once
// the hung-only limit passes with nothing received. Call it from the test's
// own goroutine.
func Receive[T any](t testing.TB, condition string, ch <-chan T) (T, bool) {
	t.Helper()
	return receiveBounded(t, condition, ch, limit(t, time.Now()))
}

func receiveBounded[T any](t testing.TB, condition string, ch <-chan T, bound time.Duration) (T, bool) {
	t.Helper()
	expiry := time.NewTimer(bound)
	defer expiry.Stop()
	select {
	case value, ok := <-ch:
		return value, ok
	case <-expiry.C:
		select {
		case value, ok := <-ch:
			return value, ok
		default:
		}
		t.Fatalf(
			"testwait: %s: not met within the hung-only limit (%s); last state: nothing received, channel open",
			condition,
			bound,
		)
		var zero T
		return zero, false
	}
}

// Drive runs call on a goroutine of its own and, until it returns, advances
// fake to the earliest wait registered on it, but only at a look where the
// family is idle: the family is the goroutine calling Drive (the test's own)
// and the call's, with every goroutine either started, at any depth, so a
// goroutine the test started before the call belongs too; idle is every
// member blocked, a member in a system call counting as working unless the
// call can only wait for an outside event (stacks.go's driveWaits), and no
// goroutine whose creator has exited, nor any it started, still working (one
// of those in a system call is not seen: stacks.go's working). Code under test that
// sleeps, polls or times out on fake then runs through every wait in order,
// its durations counted on fake alone, and a timer it selects on beside its
// own work never fires while that work runs. Drive cannot see an actor
// outside the family (a goroutine another test started, a child process): a
// wait the call parks on for such an actor is advanced as soon as the family
// is idle. It fails t with the condition's name and fake's state once the
// hung-only limit passes with the call still running. call must not call
// t.Fatal; call it from the test's own goroutine.
func Drive(t testing.TB, condition string, fake *clock.Fake, call func()) {
	t.Helper()
	driveBounded(t, condition, fake, call, limit(t, time.Now()))
}

func driveBounded(t testing.TB, condition string, fake *clock.Fake, call func(), bound time.Duration) {
	t.Helper()
	done := make(chan struct{})
	root := make(chan int64, 1)
	go func() {
		defer close(done)
		root <- currentGoroutine()
		call()
	}()
	family := &driveFamily{members: map[int64]bool{currentGoroutine(): true, <-root: true}}
	poll(t, condition, func() (bool, bool, string) {
		driveLooks.Add(1)
		select {
		case <-done:
			return true, false, ""
		default:
		}
		// A working family is looked at less often the longer it works: each
		// look stops the world to dump every goroutine, which slows that very
		// work; the call's return still ends the pause at once (done wakes it).
		if working := family.working(Goroutines(true)); working != "" {
			return false, false, "call still working (" + working + "); " + fake.String()
		}
		// A call that returned after the first check leaves an idle dump: its
		// goroutine closed done before it exited, so done tells the two apart.
		select {
		case <-done:
			return true, false, ""
		default:
		}
		if step, due := fake.NextDue(); due {
			fake.Advance(step)
		}
		// A call parked on the fake, or on an actor outside the family, is
		// about to work again: look again at the shortest pause.
		return false, true, "call still running; " + fake.String()
	}, bound, done)
}

// driveLooks counts every look of every Drive, for the door's own tests.
var driveLooks atomic.Int64

// ProcessClock installs fake as clock.Real, the clock every production
// caller without a Clock of its own reads, for the rest of t, and restores
// the one it replaced at cleanup. A test calling it reaches code with no
// Clock seam and must not run in parallel; it drives fake with Drive.
func ProcessClock(t testing.TB, fake *clock.Fake) {
	t.Helper()
	previous := *processClock
	*processClock = fake
	t.Cleanup(func() { *processClock = previous })
}

// SwapProcessClock installs c as clock.Real and returns the clock it
// replaced. internal/testjail's real-wait tripwire is its one caller: it
// wraps the real clock for a whole unit-test process. A test runs the
// process on a fake through ProcessClock instead.
func SwapProcessClock(c clock.Clock) clock.Clock {
	previous := *processClock
	*processClock = c
	return previous
}

// processClock is the process-wide clock ProcessClock and SwapProcessClock swap.
var processClock = &clock.Real

// Context is t.Context bounded by the hung-only limit: the context a test hands to code under test
// that could otherwise hang (a read, a dial, a child process). Like t.Context it ends, Canceled,
// when the test's function returns and before its Cleanups run: a Cleanup that still needs a live
// context (a teardown command) takes context.WithoutCancel(ctx), never a fresh bound of its own.
func Context(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), limit(t, time.Now()))
	t.Cleanup(cancel)
	return ctx
}

// Deadline is a context the test ends itself: expire ends it with context.DeadlineExceeded, at
// once, for code under test that tells a deadline from a cancel, reached without waiting for one.
// Until expire it is Context: its Deadline is the hung-only limit's, in the future, so a dialer or
// an HTTP transport that reads it arms no deadline already past, and it ends with the test as
// Context does. It is a clock.WithTimeout on a fake the test never sees, which expire advances.
func Deadline(t testing.TB) (ctx context.Context, expire func()) {
	t.Helper()
	bound := limit(t, time.Now())
	at := clock.NewFake(time.Now())
	ctx, cancel := clock.WithTimeout(Context(t), at, bound)
	t.Cleanup(cancel)
	return ctx, func() { at.Advance(bound) }
}

// Stopwatch starts a reading of real elapsed time now and returns the func that gives it: the one
// way a test or its support code records a duration. A reading is written to a report, never
// compared: a test that judged it would pass or fail by the host's speed.
func Stopwatch() func() time.Duration {
	start := time.Now()
	return func() time.Duration { return time.Since(start) }
}

// Expired is a context whose deadline has already passed: Done is closed and
// Err is context.DeadlineExceeded, as code under test meets them when its
// caller's deadline runs out. A test reaching that path builds this instead
// of a short timeout it would wait out.
func Expired() context.Context { return expired{} }

// expired is Expired's context; its values come from context.Background.
type expired struct{ context.Context }

// closedDone is the Done channel of every Expired context.
var closedDone = func() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

func (expired) Deadline() (time.Time, bool) { return time.Unix(0, 0), true }
func (expired) Done() <-chan struct{}       { return closedDone }
func (expired) Err() error                  { return context.DeadlineExceeded }
func (expired) Value(key any) any           { return context.Background().Value(key) }

// Watchdog calls fire once, delay from now (at once when delay is not
// positive), so a test process about to be killed at its runner's deadline can
// leave its diagnosis first. It returns the timer, whose Stop keeps fire from running.
// internal/testjail's profiler is its one caller, arming it Margin
// before that deadline: a test bounds its waits through Until, Receive or
// Context, never through this.
func Watchdog(delay time.Duration, fire func()) *time.Timer {
	return time.AfterFunc(max(delay, 0), fire)
}

// Margin is how long before a runner deadline the door acts, so its failure
// or diagnosis lands before the runner's panic: a tenth of what remains,
// between one and fifteen seconds.
func Margin(remaining time.Duration) time.Duration {
	return min(max(remaining/10, time.Second), 15*time.Second)
}

// limit is how long a wait begun at now may last: HungLimit, or less when the
// runner's own deadline, less its Margin, comes sooner. Never negative.
func limit(t testing.TB, now time.Time) time.Duration {
	d, ok := t.(deadliner)
	if !ok {
		return HungLimit
	}
	deadline, set := d.Deadline()
	return limitAt(now, deadline, set)
}

// limitAt is limit's arithmetic, apart from the testing.TB it reads.
func limitAt(now, deadline time.Time, set bool) time.Duration {
	if !set {
		return HungLimit
	}
	remaining := deadline.Sub(now)
	return max(min(HungLimit, remaining-Margin(remaining)), 0)
}

package testwait_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// recorder is a testing.TB whose Fatalf records the message and ends its
// goroutine, as the real one does.
type recorder struct {
	testing.TB
	fatal string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// onGoroutine runs call with a recorder on a goroutine of its own, so the
// recorder's Fatalf can end it, and returns the fatal message ("" for none).
func onGoroutine(t *testing.T, call func(testing.TB)) string {
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(r)
	}()
	<-done
	return r.fatal
}

func TestUntilPollsTheNamedConditionToTheHungOnlyLimit(t *testing.T) {
	cases := []struct {
		name      string
		within    time.Duration
		metOnLook int // the look that first reports met; 0 never
		wantLooks int // -1: any count
		wantFatal string
	}{
		{"met on the first look", testwait.HungLimit, 1, 1, ""},
		{"met on the third look", testwait.HungLimit, 3, 3, ""},
		{"met by the look at the limit", 0, 2, 2, ""},
		{
			"never met", 0, 0, -1,
			"testwait: the gate to open: not met within the hung-only limit (0s); last state: gate file present",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			looks := 0
			fatal := onGoroutine(t, func(tb testing.TB) {
				testwait.UntilWithin(tb, "the gate to open", func() (bool, string) {
					looks++
					return looks == tc.metOnLook || tc.metOnLook > 0 && looks > tc.metOnLook, "gate file present"
				}, tc.within)
			})
			if fatal != tc.wantFatal {
				t.Errorf("fatal = %q, want %q", fatal, tc.wantFatal)
			}
			if tc.wantLooks >= 0 && looks != tc.wantLooks {
				t.Errorf("looks = %d, want %d", looks, tc.wantLooks)
			}
			errLooks := 0
			err := testwait.UntilErrWithin("the gate to open", func() (bool, string) {
				errLooks++
				return errLooks == tc.metOnLook || tc.metOnLook > 0 && errLooks > tc.metOnLook, "gate file present"
			}, tc.within)
			if got := fmt.Sprint(err); err == nil && tc.wantFatal != "" || err != nil && got != tc.wantFatal {
				t.Errorf("UntilErr = %v, want %q", err, tc.wantFatal)
			}
			if tc.wantLooks >= 0 && errLooks != tc.wantLooks {
				t.Errorf("UntilErr looks = %d, want %d", errLooks, tc.wantLooks)
			}
		})
	}
}

func TestReceiveTakesAValueOrACloseAndNamesAHang(t *testing.T) {
	ready := make(chan int, 1)
	ready <- 7
	closed := make(chan int)
	close(closed)
	cases := []struct {
		name      string
		ch        chan int
		within    time.Duration
		wantValue int
		wantOK    bool
		wantFatal string
	}{
		{"a value", ready, testwait.HungLimit, 7, true, ""},
		{"a close", closed, testwait.HungLimit, 0, false, ""},
		{
			"nothing", make(chan int), 0, 0, false,
			"testwait: the worker's answer: not met within the hung-only limit (0s); " +
				"last state: nothing received, channel open",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value int
			var ok bool
			fatal := onGoroutine(t, func(tb testing.TB) {
				value, ok = testwait.ReceiveWithin(tb, "the worker's answer", tc.ch, tc.within)
			})
			if value != tc.wantValue || ok != tc.wantOK || fatal != tc.wantFatal {
				t.Errorf("Receive = (%d, %v), fatal %q; want (%d, %v), fatal %q",
					value, ok, fatal, tc.wantValue, tc.wantOK, tc.wantFatal)
			}
		})
	}
}

func TestReceiveTakesASendFromAnotherGoroutine(t *testing.T) {
	answer := make(chan string)
	go func() { answer <- "done" }()
	if got, ok := testwait.Receive(t, "the sender's answer", answer); got != "done" || !ok {
		t.Errorf("Receive = (%q, %v), want (\"done\", true)", got, ok)
	}
}

func TestLimitAtIsTheHungOnlyLimitCutShortByTheRunnersDeadline(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	cases := []struct {
		name      string
		remaining time.Duration
		set       bool
		want      time.Duration
	}{
		{"no runner deadline", 0, false, testwait.HungLimit},
		{"a deadline an hour away", time.Hour, true, testwait.HungLimit},
		{
			"a deadline whose margin leaves just over the limit",
			2*time.Minute + 15*time.Second,
			true,
			testwait.HungLimit,
		},
		{"a deadline whose margin leaves exactly the limit", 400 * time.Second / 3, true, testwait.HungLimit},
		{"a deadline two minutes away", 2 * time.Minute, true, 108 * time.Second},
		{"a deadline half a second away", 500 * time.Millisecond, true, 0},
		{"a deadline passed", -time.Second, true, 0},
	}
	for _, tc := range cases {
		if got := testwait.LimitAt(now, now.Add(tc.remaining), tc.set); got != tc.want {
			t.Errorf("%s: LimitAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMarginIsATenthBetweenOneAndFifteenSeconds(t *testing.T) {
	cases := []struct{ remaining, want time.Duration }{
		{5 * time.Second, time.Second},
		{10 * time.Second, time.Second},
		{11 * time.Second, 1100 * time.Millisecond},
		{149 * time.Second, 14900 * time.Millisecond},
		{150 * time.Second, 15 * time.Second},
		{151 * time.Second, 15 * time.Second},
	}
	for _, tc := range cases {
		if got := testwait.Margin(tc.remaining); got != tc.want {
			t.Errorf("Margin(%v) = %v, want %v", tc.remaining, got, tc.want)
		}
	}
}

func TestContextIsBoundedByTheLimitAndEndsWithTheTest(t *testing.T) {
	var ctx context.Context
	var inCleanup, withoutCancel error
	before := time.Now()
	t.Run("holder", func(t *testing.T) {
		ctx = testwait.Context(t)
		// The trap the door documents: a Cleanup meets the context already ended, and keeps a
		// live one through context.WithoutCancel.
		t.Cleanup(func() { inCleanup, withoutCancel = ctx.Err(), context.WithoutCancel(ctx).Err() })
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(before) || deadline.After(before.Add(testwait.HungLimit).Add(time.Second)) {
			t.Errorf("Context deadline = (%v, %v), want within %v of %v", deadline, ok, testwait.HungLimit, before)
		}
		if err := ctx.Err(); err != nil {
			t.Errorf("Context during the test: %v, want nil", err)
		}
	})
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("Context after the test: %v, want %v", err, context.Canceled)
	}
	if inCleanup != context.Canceled || withoutCancel != nil {
		t.Errorf("in a Cleanup: Context Err = %v, WithoutCancel Err = %v; want %v, nil",
			inCleanup, withoutCancel, context.Canceled)
	}
}

func TestDeadlineEndsWithDeadlineExceededWhenTheTestExpiresIt(t *testing.T) {
	before := time.Now()
	ctx, expire := testwait.Deadline(t)
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.After(before) || deadline.After(before.Add(testwait.HungLimit).Add(time.Second)) {
		t.Errorf("Deadline before expire = (%v, %v), want after %v and within %v of it",
			deadline, ok, before, testwait.HungLimit)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("Err before expire = %v, want nil", err)
	}
	expire()
	if err, cause := ctx.Err(), context.Cause(
		ctx,
	); err != context.DeadlineExceeded ||
		cause != context.DeadlineExceeded {
		t.Errorf("Err, Cause after expire = %v, %v; want %v twice", err, cause, context.DeadlineExceeded)
	}

	var unexpired context.Context
	t.Run("holder", func(t *testing.T) { unexpired, _ = testwait.Deadline(t) })
	if err := unexpired.Err(); err != context.Canceled {
		t.Errorf("Deadline after its test ended unexpired: %v, want %v", err, context.Canceled)
	}
}

func TestStopwatchReadsTheElapsedTimeSinceItStarted(t *testing.T) {
	elapsed := testwait.Stopwatch()
	first := elapsed()
	second := elapsed()
	if first < 0 || second < first {
		t.Errorf("readings = %v then %v, want a non-negative reading that never runs backwards", first, second)
	}
}

func TestExpiredIsAContextPastItsDeadline(t *testing.T) {
	ctx := testwait.Expired()
	deadline, ok := ctx.Deadline()
	if !deadline.Equal(time.Unix(0, 0)) || !ok {
		t.Errorf("Deadline = (%v, %v), want (%v, true)", deadline, ok, time.Unix(0, 0))
	}
	if err := ctx.Err(); err != context.DeadlineExceeded {
		t.Errorf("Err = %v, want %v", err, context.DeadlineExceeded)
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("Done is open, want closed")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := child.Err(); err != context.DeadlineExceeded {
		t.Errorf("a child's Err = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestWatchdogFiresAtItsInstantAndStopKeepsItFromFiring(t *testing.T) {
	fired := make(chan string, 2)
	testwait.Watchdog(0, func() { fired <- "past" })
	if got, _ := testwait.Receive(t, "the watchdog armed in the past to fire", fired); got != "past" {
		t.Errorf("fired %q, want past", got)
	}
	later := testwait.Watchdog(time.Hour, func() { fired <- "later" })
	if !later.Stop() {
		t.Error("Stop of a watchdog an hour ahead = false, want true: it fired")
	}
	if len(fired) != 0 {
		t.Errorf("a stopped watchdog fired: %q", <-fired)
	}
}

// blockedReceiving and blockedSpinning are the named calls TestBlocked finds
// on, or misses from, a goroutine's stack.
//
//go:noinline
func blockedReceiving(release <-chan struct{}) { <-release }

//go:noinline
func blockedSpinning(stop *atomic.Bool) {
	for !stop.Load() {
	}
}

// blockedReading sits in a read system call on fd until a byte arrives.
//
//go:noinline
func blockedReading(fd int) { _, _ = syscall.Read(fd, make([]byte, 1)) }

func TestBlockedFindsAGoroutineWaitingAtANamedCallAndNoOther(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		blockedReceiving(release)
	}()
	testwait.Until(t, "a goroutine blocked in blockedReceiving", func() (bool, string) {
		return testwait.Blocked(
			"testwait_test.blockedReceiving",
			"testwait_test.TestBlockedFindsAGoroutineWaitingAtANamedCallAndNoOther.func1",
		)
	})
	if met, state := testwait.Blocked("testwait_test.blockedReceiving", "testwait_test.blockedSpinning"); met {
		t.Fatalf("Blocked(blockedReceiving <- blockedSpinning) = true (%s), want false: no goroutine holds it", state)
	}
	close(release)
	testwait.Receive(t, "the released goroutine to return", done)
	const released = "0 goroutines hold testwait_test.blockedReceiving, none blocked []"
	if met, state := testwait.Blocked("testwait_test.blockedReceiving"); met || state != released {
		t.Fatalf("Blocked after release = %v, %q; want false, %q", met, state, released)
	}

	var stop atomic.Bool
	spun := make(chan struct{})
	go func() {
		defer close(spun)
		blockedSpinning(&stop)
	}()
	testwait.Until(t, "the spinning goroutine on its stack", func() (bool, string) {
		met, state := testwait.Blocked("testwait_test.blockedSpinning")
		return strings.HasPrefix(state, "1 goroutines hold"), fmt.Sprintf("%v %s", met, state)
	})
	if met, state := testwait.Blocked("testwait_test.blockedSpinning"); met {
		t.Fatalf("Blocked(blockedSpinning) = true (%s), want false: the goroutine runs", state)
	}
	stop.Store(true)
	testwait.Receive(t, "the spinning goroutine to stop", spun)
}

func TestDriveAdvancesTheFakeThroughEachWaitUntilTheCallReturns(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		sleeps    int
		hang      bool
		within    time.Duration
		wantNow   time.Time
		wantFatal string
	}{
		{"no wait", 0, false, testwait.HungLimit, epoch, ""},
		{"three hour-long sleeps", 3, false, testwait.HungLimit, epoch.Add(3 * time.Hour), ""},
		{
			"a call blocked off the clock", 0, true, 0, epoch,
			"testwait: the poller to give up: not met within the hung-only limit (0s); " +
				"last state: call still running; clock.Fake(now=2026-01-01 00:00:00 +0000 UTC, pending=0)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake(epoch)
			release := make(chan struct{})
			defer close(release)
			fatal := onGoroutine(t, func(tb testing.TB) {
				testwait.DriveWithin(tb, "the poller to give up", fake, func() {
					for range tc.sleeps {
						_ = fake.Sleep(context.Background(), time.Hour)
					}
					if tc.hang {
						<-release
					}
				}, tc.within)
			})
			if fatal != tc.wantFatal {
				t.Errorf("fatal = %q, want %q", fatal, tc.wantFatal)
			}
			if got := fake.Now(); !got.Equal(tc.wantNow) {
				t.Errorf("fake now = %v, want %v", got, tc.wantNow)
			}
		})
	}
}

func TestDriveNeverAdvancesUnderAGoroutineOfTheCallStillWorking(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Each call arms an hour-long give-up timer, then waits for work done on
	// a goroutine of its family until the test releases it; the work must win
	// over the timer. before starts the work on the test's own goroutine
	// before the call, else the call starts it after arming its timer.
	spinning := func(nest int) (func(), func(chan<- string)) {
		var stop atomic.Bool
		return func() { stop.Store(true) }, func(result chan<- string) {
			if nest == 0 {
				blockedSpinning(&stop)
				result <- "work"
				return
			}
			started := make(chan struct{})
			go func() {
				if nest == 1 {
					close(started)
					blockedSpinning(&stop)
					result <- "work"
					return
				}
				go func() {
					blockedSpinning(&stop)
					result <- "work"
				}()
				close(started)
			}()
			<-started
		}
	}
	cases := []struct {
		name   string
		before bool
		setup  func(t *testing.T) (release func(), work func(result chan<- string))
	}{
		{"the call's own goroutine", false, func(*testing.T) (func(), func(chan<- string)) {
			return spinning(0)
		}},
		{"a goroutine the call started", false, func(*testing.T) (func(), func(chan<- string)) {
			return spinning(1)
		}},
		{"a goroutine started by one that has returned", false, func(*testing.T) (func(), func(chan<- string)) {
			return spinning(2)
		}},
		{"a goroutine the test started before the call", true, func(*testing.T) (func(), func(chan<- string)) {
			return spinning(0)
		}},
		{"a goroutine of the call busy in a system call", false, func(t *testing.T) (func(), func(chan<- string)) {
			var fds [2]int
			if err := syscall.Pipe(fds[:]); err != nil {
				t.Fatalf("syscall.Pipe: %v", err)
			}
			t.Cleanup(func() {
				_ = syscall.Close(fds[0])
				_ = syscall.Close(fds[1])
			})
			return func() { _, _ = syscall.Write(fds[1], []byte{1}) }, func(result chan<- string) {
				go func() {
					blockedReading(fds[0])
					result <- "work"
				}()
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake(epoch)
			release, work := tc.setup(t)
			release = sync.OnceFunc(release)
			defer release()
			var got string
			armed := make(chan struct{})
			driven := make(chan string, 1)
			go func() {
				driven <- onGoroutine(t, func(tb testing.TB) {
					result := make(chan string, 1)
					if tc.before {
						go work(result)
					}
					testwait.Drive(tb, "the work to beat its give-up timer", fake, func() {
						timer := fake.NewTimer(time.Hour)
						close(armed)
						if !tc.before {
							work(result)
						}
						select {
						case got = <-result:
						case <-timer.C():
							got = "timeout"
						}
					})
				})
			}()
			testwait.Receive(t, "the call to arm its give-up timer", armed)
			base := testwait.DriveLooks.Load()
			testwait.Until(t, "ten looks of Drive with the timer armed, or its return", func() (bool, string) {
				looks := testwait.DriveLooks.Load()
				return looks >= base+10 || len(driven) == 1, fmt.Sprintf("looks %d from %d; %s", looks, base, fake)
			})
			if pending := fake.Pending(); pending != 1 {
				t.Errorf("pending waits while the call works = %d, want 1: Drive fired the timer under it", pending)
			}
			release()
			if fatal, _ := testwait.Receive(t, "Drive to return", driven); fatal != "" {
				t.Fatalf("Drive failed: %s", fatal)
			}
			if got != "work" {
				t.Errorf("the call saw %q, want \"work\"", got)
			}
			if now := fake.Now(); !now.Equal(epoch) {
				t.Errorf("fake now = %v, want %v: Drive advanced while the call worked", now, epoch)
			}
		})
	}
	t.Run("a goroutine the call started sleeps on the fake", func(t *testing.T) {
		fake := clock.NewFake(epoch)
		var got string
		testwait.Drive(t, "the started sleeper to wake", fake, func() {
			result := make(chan string)
			go func() {
				_ = fake.Sleep(context.Background(), time.Hour)
				result <- "slept"
			}()
			got = <-result
		})
		if got != "slept" || !fake.Now().Equal(epoch.Add(time.Hour)) {
			t.Errorf("got %q at %v, want \"slept\" at %v", got, fake.Now(), epoch.Add(time.Hour))
		}
	})
}

func TestProcessClockRunsTheProcessOnTheFakeUntilCleanup(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := *testwait.Process
	fake := clock.NewFake(epoch)
	t.Run("installed", func(t *testing.T) {
		testwait.ProcessClock(t, fake)
		if *testwait.Process != clock.Clock(fake) {
			t.Fatalf("process clock = %v, want the installed %v", *testwait.Process, fake)
		}
		if got := (*testwait.Process).Now(); !got.Equal(epoch) {
			t.Fatalf("process clock now = %v, want the fake's %v", got, epoch)
		}
	})
	if *testwait.Process != before {
		t.Fatalf("process clock after cleanup = %v, want the clock it replaced %v", *testwait.Process, before)
	}
}

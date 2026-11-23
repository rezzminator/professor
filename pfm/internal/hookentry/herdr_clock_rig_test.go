package hookentry

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// fakeClock puts the jail's reporters on a fake clock: their session-lock
// retries and the hook's deadline then pass only when the test advances it
// (testwait.Drive for a lock held until the deadline, lockRetryOnRelease for a
// lock another reporter is about to release).
// The fake starts at the wall time: a socket call under the hook's deadline sets a kernel deadline
// from the context's, which must not already be past.
func (jail *herdrJail) fakeClock() *clock.Fake {
	fake := clock.NewFake(time.Now())
	jail.clock = fake
	return fake
}

// expireHeldWait runs a call that may meet a lock held past its deadline: once
// the call is parked on a retry of the lock (its next wait on the fake is within
// poll), the fake is advanced by the whole deadline, so the call's own deadline
// passes before the retry; a call that ends without parking is left alone. The
// deadline is never driven step by step: the call's other work (dials, reads
// from a server goroutine) takes its own time, and only the parked retry may
// pass the deadline.
func expireHeldWait(t *testing.T, fake *clock.Fake, poll, deadline time.Duration, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
	}()
	testwait.Until(t, "the call to end or park on its next retry of a held lock", func() (bool, string) {
		select {
		case <-done:
			return true, ""
		default:
		}
		next, ok := fake.NextDue()
		return ok && next <= poll, fake.String()
	})
	fake.Advance(deadline)
	testwait.Receive(t, "the call to end at its deadline", done)
}

// lockFree reports whether nobody holds the flock on path: it takes the lock
// and gives it back at once.
func lockFree(t *testing.T, path string) bool {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return true
}

// lockRetryArmed reports whether a reporter waiting on a held lock has armed
// its retry on the fake. The lock makes its directory and opens its file before
// it arms one, and the deadline every reporter arms is due later than a retry,
// so the earliest timer being one retry away is exactly that reporter waiting.
func lockRetryArmed(fake *clock.Fake) bool {
	due, ok := fake.NextDue()
	return ok && due == herdrLockPoll
}

// lockRetryOnRelease is one step of a wait for reporters contending on the
// lock at path: when a reporter has armed its retry and the holder has
// released the lock, it advances the fake by one retry, so the waiting reporter
// takes it. It reports whether it advanced; a held lock is left alone.
func lockRetryOnRelease(t *testing.T, fake *clock.Fake, path string) bool {
	t.Helper()
	if !lockRetryArmed(fake) || !lockFree(t, path) {
		return false
	}
	fake.Advance(herdrLockPoll)
	return true
}

// testHerdrDeps is realHerdrDeps on a clock nobody advances: the reporter's deadline and lock
// retries never pass, so a test that wants one to pass takes fakeClock.
func testHerdrDeps() herdrDeps {
	deps := realHerdrDeps()
	deps.clock = clock.NewFake(time.Now())
	return deps
}

// idleProcessClock puts the process clock on a fake nobody advances for the rest of t: a
// deadline the code under test bounds a call with on clock.Real never passes, as it never
// does when the call answers.
func idleProcessClock(t *testing.T) {
	t.Helper()
	testwait.ProcessClock(t, clock.NewFake(time.Now()))
}

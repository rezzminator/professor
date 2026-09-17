package clock

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is a deterministic Clock: time moves only when a test calls Advance,
// and every Sleep/After/Timer/Ticker registered against it fires in
// due-time order (registration order breaks a tie) as Advance crosses each
// one's deadline — the same lossy, buffer-of-one delivery a real
// time.Ticker gives a slow reader, so a caller written against the real
// Clock behaves identically against this one.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeWaiter
	nextID  uint64
}

type fakeWaiter struct {
	id      uint64
	due     time.Time
	period  time.Duration // 0 for a one-shot Sleep/After/Timer.
	channel chan time.Time
	active  bool
}

// NewFake returns a Fake clock reading start. A caller that only compares
// durations between its own events can pass any fixed value; hostfixture
// pins one so every fixture's clock reads the same epoch.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Sleep blocks until Advance carries the fake clock past d, or until ctx is
// done, whichever happens first — a goroutine calling this must be released
// by an Advance from another goroutine, exactly the shape a test drives a
// blocked worker with.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	channel := f.After(d)
	select {
	case <-channel:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerLocked(d, 0).channel
}

func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fakeTimer{clock: f, waiter: f.registerLocked(d, 0)}
}

// NewTicker registers a repeating waiter, firing every d starting d after
// this call. d must be positive — the same refusal time.NewTicker makes for
// a non-positive interval.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fakeTicker{clock: f, waiter: f.registerLocked(d, d)}
}

func (f *Fake) registerLocked(delay, period time.Duration) *fakeWaiter {
	f.nextID++
	waiter := &fakeWaiter{
		id:      f.nextID,
		due:     f.now.Add(delay),
		period:  period,
		channel: make(chan time.Time, 1),
		active:  true,
	}
	f.waiters = append(f.waiters, waiter)
	if delay <= 0 {
		// A zero or negative delay fires immediately, the same way
		// time.After(0)/time.NewTimer(0) need no external nudge — a Fake
		// standing in for Real must not force every caller's zero-wait
		// path through an explicit Advance(0) it would never make on Real.
		f.fireLocked(waiter, f.now)
	}
	return waiter
}

func (f *Fake) fireLocked(waiter *fakeWaiter, at time.Time) {
	select {
	case waiter.channel <- at:
	default:
		// The previous tick/fire is still unread: drop this one, matching
		// time.Ticker's own documented behavior for a slow receiver.
	}
	if waiter.period > 0 {
		waiter.due = at.Add(waiter.period)
	} else {
		waiter.active = false
	}
}

// Advance moves the fake clock forward by d, firing every sleep, timer and
// ticker whose deadline falls at or before the new time, earliest deadline
// first (registration order breaks a tie) — a ticker whose period elapsed
// more than once during d reschedules and fires again within this same
// call, catching up the way a real, undrained time.Ticker does.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := f.now.Add(d)
	for {
		due := f.earliestDueLocked(target)
		if due == nil {
			break
		}
		f.now = due.due
		f.fireLocked(due, f.now)
	}
	f.now = target
}

func (f *Fake) earliestDueLocked(target time.Time) *fakeWaiter {
	var earliest *fakeWaiter
	for _, waiter := range f.waiters {
		if !waiter.active || waiter.due.After(target) {
			continue
		}
		if earliest == nil || waiter.due.Before(earliest.due) ||
			(waiter.due.Equal(earliest.due) && waiter.id < earliest.id) {
			earliest = waiter
		}
	}
	return earliest
}

// Pending reports how many sleeps, timers and tickers are still registered
// and active — a case that has not Advanced far enough to release every
// blocked caller must see itself counted here, or the fixture is lying
// about what it is still blocking on.
func (f *Fake) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, waiter := range f.waiters {
		if waiter.active {
			count++
		}
	}
	return count
}

// String renders the fake clock's own time, for a test failure message that
// needs to say what "now" was at the point it failed.
func (f *Fake) String() string {
	return fmt.Sprintf("clock.Fake(now=%s, pending=%d)", f.Now(), f.Pending())
}

type fakeTimer struct {
	clock  *Fake
	waiter *fakeWaiter
}

func (t *fakeTimer) C() <-chan time.Time { return t.waiter.channel }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.waiter.active
	t.waiter.active = false
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.waiter.active
	t.waiter.due = t.clock.now.Add(d)
	t.waiter.active = true
	return wasActive
}

type fakeTicker struct {
	clock  *Fake
	waiter *fakeWaiter
}

func (t *fakeTicker) C() <-chan time.Time { return t.waiter.channel }

func (t *fakeTicker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.waiter.active = false
}

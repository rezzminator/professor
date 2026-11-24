package clock

import (
	"context"
	"sync"
	"time"
)

// WithTimeout is production's one way to bound work by a context on the clock seam: a child of
// parent that ends with context.DeadlineExceeded once c's timer fires d after this call. On the real
// clock it is context.WithTimeout(parent, d) exactly. On any other clock (a Fake, or the real-wait
// tripwire's recorder, which waits on the real clock and is therefore not the real clock here) it
// ends with context.DeadlineExceeded when c's timer fires after d, never before; Deadline reports
// c.Now().Add(d); the parent's end carries the parent's error and cause; cancel ends it with
// context.Canceled and stops the timer. On a Fake the end lands inside the Advance that fires it, so
// a test reads Err as soon as Advance returns. A nil c is Real, read at the call.
func WithTimeout(parent context.Context, c Clock, d time.Duration) (context.Context, context.CancelFunc) {
	if c == nil {
		c = Real
	}
	if _, isReal := c.(realClock); isReal {
		return context.WithTimeout(parent, d)
	}
	bound := &clockBound{parent: parent, deadline: c.Now().Add(d), done: make(chan struct{})}
	// The returned context is the standard library's own, so its children, its Cause and every
	// caller that inspects it behave as under context.WithTimeout; it ends with bound, synchronously,
	// through bound's AfterFunc. It attaches before anything below can end bound: the context
	// package registers the child's cancel holding the child's lock, the lock that cancel takes, so
	// an end landing inside that registration would hang the caller for good.
	ctx, cancel := context.WithCancel(bound)
	stopParent := func() bool { return false }
	if err := parent.Err(); err != nil {
		bound.end(err)
	} else if parent.Done() != nil {
		stopParent = context.AfterFunc(parent, func() { bound.end(parent.Err()) })
	}
	switch {
	case bound.Err() != nil:
		// The parent had already ended: no timer to arm.
	case d <= 0:
		bound.end(context.DeadlineExceeded)
	case isFake(c):
		bound.armed(c.(*Fake).afterFunc(d, func() { bound.end(context.DeadlineExceeded) }))
	default:
		timer := c.NewTimer(d)
		bound.armed(timer)
		go func() {
			select {
			case <-timer.C():
				bound.end(context.DeadlineExceeded)
			case <-bound.done:
			}
		}()
	}
	return ctx, func() {
		cancel()
		stopParent()
		bound.end(context.Canceled)
	}
}

func isFake(c Clock) bool {
	_, fake := c.(*Fake)
	return fake
}

// clockBound is WithTimeout's deadline on a clock other than the real one: its Err is the first
// end it meets (the timer's DeadlineExceeded, the parent's error, or Canceled), its Value is the
// parent's, so context.Cause finds the parent's cause, and its AfterFunc runs the standard
// library's child cancel inside the end itself.
type clockBound struct {
	parent   context.Context
	deadline time.Time
	done     chan struct{}
	mu       sync.Mutex
	err      error
	after    []*func()
	timer    Timer // the armed timer, stopped by the first end
}

// armed records the bound's timer, stopping it at once when the bound has already ended.
func (b *clockBound) armed(timer Timer) {
	b.mu.Lock()
	ended := b.err != nil
	if !ended {
		b.timer = timer
	}
	b.mu.Unlock()
	if ended {
		timer.Stop()
	}
}

func (b *clockBound) Deadline() (time.Time, bool) { return b.deadline, true }
func (b *clockBound) Done() <-chan struct{}       { return b.done }
func (b *clockBound) Value(key any) any           { return b.parent.Value(key) }

func (b *clockBound) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// end records err as the bound's end unless it has already ended, closes Done, stops the armed
// timer and runs every AfterFunc callback still registered, in registration order.
func (b *clockBound) end(err error) {
	b.mu.Lock()
	if b.err != nil {
		b.mu.Unlock()
		return
	}
	b.err = err
	close(b.done)
	after, timer := b.after, b.timer
	b.after, b.timer = nil, nil
	b.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	for _, f := range after {
		(*f)()
	}
}

// AfterFunc is the context package's afterFuncer: the standard library child WithTimeout returns
// registers its cancel here, before anything can end the bound, so the child ends inside end, never
// on a goroutine of its own. On a bound that has already ended f runs on a goroutine of its own,
// as context.AfterFunc's does: the caller may hold a lock f takes. stop reports whether it kept f
// from running.
func (b *clockBound) AfterFunc(f func()) (stop func() bool) {
	b.mu.Lock()
	if b.err != nil {
		b.mu.Unlock()
		go f()
		return func() bool { return false }
	}
	entry := &f
	b.after = append(b.after, entry)
	b.mu.Unlock()
	return func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, registered := range b.after {
			if registered == entry {
				b.after = append(b.after[:i], b.after[i+1:]...)
				return true
			}
		}
		return false
	}
}

// afterFunc runs call inside the Advance that carries the fake past d, after the fake unlocks, so a
// context WithTimeout bounds on the fake has ended when that Advance returns; the returned Timer's
// Stop keeps call from running. A non-positive d runs call at once.
func (f *Fake) afterFunc(d time.Duration, call func()) Timer {
	f.mu.Lock()
	if d <= 0 {
		f.mu.Unlock()
		call()
		return &fakeTimer{clock: f, waiter: &fakeWaiter{}}
	}
	f.nextID++
	waiter := &fakeWaiter{id: f.nextID, due: f.now.Add(d), active: true, call: call}
	f.waiters = append(f.waiters, waiter)
	f.mu.Unlock()
	return &fakeTimer{clock: f, waiter: waiter}
}

// Package clock is the one time seam: every bare time.Now/time.Sleep/
// time.After/time.NewTimer/time.NewTicker in non-test code routes through a
// Clock instead (the unit-test law's three seams). Real drives the wall clock exactly as the standard
// library always has; Fake drives a deterministic one a test advances by
// hand, so a sleep, a timer or a ticker fires on the test's own schedule
// instead of the real one.
//
// Today's per-package clock styles (installer.Options.Now/Sleep,
// kill.Dependencies.Now, reap.Dependencies.Now, picker.ActivityClock,
// statusline.Runtime.Now) stay as they are in this batch — additive only,
// no caller migrates yet — but they name the shape a thin adapter over
// Clock takes when a later wave folds them in: one clock, never five.
package clock

import (
	"context"
	"time"
)

// Clock is the seam every timing door in pfm crosses instead of the bare
// time package: Now for a timestamp, Sleep for a bounded, cancellable wait,
// After/NewTimer/NewTicker for the standard library's three channel-based
// primitives.
type Clock interface {
	// Now reports the current time.
	Now() time.Time
	// Sleep blocks for d, or until ctx is done — whichever comes first —
	// returning ctx.Err() only in the latter case. Unlike time.Sleep, a
	// cancelled caller is never left blocked past its own deadline.
	Sleep(ctx context.Context, d time.Duration) error
	// After is time.After: a channel that receives once, d after this call.
	After(d time.Duration) <-chan time.Time
	// NewTimer is time.NewTimer, returned through the Timer seam.
	NewTimer(d time.Duration) Timer
	// NewTicker is time.NewTicker, returned through the Ticker seam.
	NewTicker(d time.Duration) Ticker
}

// Timer is time.Timer's seam: a channel plus Stop/Reset, so a caller holding
// a Clock never touches *time.Timer directly.
type Timer interface {
	// C is the channel the timer fires on — a method, not a field, because
	// an interface cannot expose time.Timer's bare C field.
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// Ticker is time.Ticker's seam: a repeating channel plus Stop.
type Ticker interface {
	// C is the channel the ticker fires on repeatedly.
	C() <-chan time.Time
	Stop()
}

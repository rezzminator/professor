package clock

import (
	"context"
	"time"
)

// Real is the Clock every production caller gets by default: it drives the
// wall clock exactly the way a bare time.Now/time.Sleep/time.After/
// time.NewTimer/time.NewTicker call always has.
var Real Clock = realClock{}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (realClock) NewTimer(d time.Duration) Timer { return &realTimer{timer: time.NewTimer(d)} }

func (realClock) NewTicker(d time.Duration) Ticker { return &realTicker{ticker: time.NewTicker(d)} }

type realTimer struct{ timer *time.Timer }

func (r *realTimer) C() <-chan time.Time        { return r.timer.C }
func (r *realTimer) Stop() bool                 { return r.timer.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.timer.Reset(d) }

type realTicker struct{ ticker *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.ticker.C }
func (r *realTicker) Stop()               { r.ticker.Stop() }

package clock

import (
	"context"
	"testing"
	"time"
)

func TestRealNowAdvancesWithTheWallClock(t *testing.T) {
	first := Real.Now()
	time.Sleep(time.Millisecond)
	second := Real.Now()
	if !second.After(first) {
		t.Fatalf("Real.Now() did not advance: first=%v second=%v", first, second)
	}
}

func TestRealSleepReturnsWhenCtxIsCancelledBeforeTheDuration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Real.Sleep(ctx, time.Hour)
	if err == nil {
		t.Fatal("Sleep with an already-cancelled ctx returned nil error, want ctx.Err()")
	}
}

func TestRealSleepReturnsAfterItsDurationOnAnUncancelledCtx(t *testing.T) {
	if err := Real.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("Sleep() error = %v", err)
	}
}

func TestRealTimerFiresOnItsOwnChannel(t *testing.T) {
	timer := Real.NewTimer(time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C():
	case <-time.After(time.Second):
		t.Fatal("real timer never fired within 1s")
	}
}

func TestRealTickerFiresRepeatedly(t *testing.T) {
	ticker := Real.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-ticker.C():
		case <-time.After(time.Second):
			t.Fatalf("real ticker did not fire tick %d within 1s", i)
		}
	}
}

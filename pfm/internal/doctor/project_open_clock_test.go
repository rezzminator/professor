package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// The ledger's context ends at openProjectDeadline on openProjectTimer: open
// one step before it, ended with context.DeadlineExceeded at it.
func TestOpenProjectContextEndsAtTheDeadlineOnItsTimer(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	t.Cleanup(swapOpenProjectTimer(fake))
	ctx, cancel := openProjectContext()
	defer cancel()
	fake.Advance(openProjectDeadline - time.Nanosecond)
	if err := ctx.Err(); err != nil {
		t.Fatalf("one step before the %s deadline: ctx.Err() = %v, want nil; %s", openProjectDeadline, err, fake)
	}
	fake.Advance(time.Nanosecond)
	select {
	case <-ctx.Done():
	default:
		t.Fatalf("at the %s deadline: ctx still open; %s", openProjectDeadline, fake)
	}
	if err := ctx.Err(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("at the %s deadline: ctx.Err() = %v, want %v", openProjectDeadline, err, context.DeadlineExceeded)
	}
}

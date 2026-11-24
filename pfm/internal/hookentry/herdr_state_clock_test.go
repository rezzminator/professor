package hookentry

import (
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestHerdrReporterClockDefaultsToRealAndBoundsTheDeadlineOnItsOwn(t *testing.T) {
	if got := (&herdrReporter{}).clock(); got != clock.Real {
		t.Fatalf("reporter clock without deps.clock = %v, want clock.Real", got)
	}
	fake := clock.NewFake(time.Now())
	reporter := &herdrReporter{deps: herdrDeps{clock: fake}}
	if got := reporter.clock(); got != clock.Clock(fake) {
		t.Fatalf("reporter clock = %v, want the injected fake", got)
	}
	ctx, cancel := reporter.deadlineContext()
	defer cancel()
	fake.Advance(herdrDeadline - time.Nanosecond)
	if err := ctx.Err(); err != nil {
		t.Fatalf("context one step before the deadline: err = %v, want nil", err)
	}
	fake.Advance(time.Nanosecond)
	if err := ctx.Err(); err == nil {
		t.Fatal("context at the deadline: err = nil, want context deadline exceeded")
	}
}

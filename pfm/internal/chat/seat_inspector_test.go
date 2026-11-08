package chat

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// One watch poll proves all its native seats through the one shield the poll
// holds: herdr-shield runs once per socket per poll, and a new poll lists again.
func TestSeatInspectorSweepHoldsOneHerdrShieldListingPerPoll(t *testing.T) {
	const socket = "/run/fixture/herdr/herdr.sock"
	runs := testjail.StageHerdrShield(t, testjail.HerdrShieldListing(5, 2, 10, 3, "w1:p1", socket), 0)
	inspector := NewSeatInspector(&pfmconfig.Runtime{Paths: paths.Values{ProcRoot: t.TempDir()}})
	fake := clock.NewFake(time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC))
	inspector.shield.Clock = fake
	for poll, wantRuns := range []int{1, 2} {
		inspector.Sweep()
		for seat := range 3 {
			fake.Advance(3 * time.Second) // a slow inspection: a listing is stale before the next seat
			if _, err := inspector.proofs.Shield.Running(context.Background(), socket); err != nil {
				t.Fatalf("poll %d seat %d: %v", poll, seat, err)
			}
		}
		if got := runs(); got != wantRuns {
			t.Fatalf("after poll %d herdr-shield ran %d times; want %d", poll, got, wantRuns)
		}
	}
}

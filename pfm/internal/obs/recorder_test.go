package obs

import (
	"log/slog"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestTestRecorderCapturesFieldsAndRestoresTheProcessLogger(t *testing.T) {
	before := processScope()
	ctx, recorder := Test(t)
	if processScope() == before {
		t.Fatal("obs.Test did not install its recorder as the process logger")
	}
	Logger(ctx).Warn("probe", FieldChat, "cc-1")
	records := recorder.Records()
	if len(records) != 1 || records[0].Message != "probe" || records[0].Level != slog.LevelWarn.String() {
		t.Fatalf("records = %+v, want one WARN probe", records)
	}
	if chat, found := records[0].Field(FieldChat); !found || chat != "cc-1" {
		t.Fatalf("chat field = %v (found %t)", chat, found)
	}
	if _, found := records[0].Field("nothing-logged-this"); found {
		t.Fatal("Field reported a key the record never carried")
	}
}

// TestTestWithTestClockScopesTheClockEveryDoorReadsThroughObsClock is the
// tmux-door scoped-clock gap named on the testing-foundation train:
// obs.Test used to hardcode clock.Real, so no test could ever tell a
// scoped fake clock from the wall clock — a door that reads its clock
// through ctx (obs.Clock, tmux/exec.go's `clock: obs.Clock(ctx)` among
// them) could never be driven deterministically from a test. WithTestClock
// closes that gap; a real tmux/exec_test.go pin is I1's follow-up
// (internal/tmux is not this wave's file).
func TestTestWithTestClockScopesTheClockEveryDoorReadsThroughObsClock(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	ctx, recorder := Test(t, WithTestClock(fake))
	if got := Clock(ctx); got != clock.Clock(fake) {
		t.Fatalf("Clock(ctx) = %v, want the fake clock obs.Test was given", got)
	}
	// Span, the middleware timing doors are all built on (tmux/exec.go's
	// `clock: obs.Clock(ctx)` among them), reads dur_ms from the SAME clock —
	// advancing it, not waiting on real time, must move the recorded duration.
	end := Span(ctx, "probe")
	fake.Advance(250 * time.Millisecond)
	end(nil)
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want probe.start + probe.end: %s", len(records), recorder.Raw())
	}
	wantField(t, records[1], FieldDur, float64(250))
}

func TestRecorderReportsAnUndecodableLineAsEvidence(t *testing.T) {
	recorder := &Recorder{}
	if _, err := recorder.Write([]byte("{not json}\n")); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Message == "" {
		t.Fatal("an undecodable line was dropped instead of reported")
	}
}

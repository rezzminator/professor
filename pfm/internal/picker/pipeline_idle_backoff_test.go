package picker

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

func TestRefreshCadenceGrowsWhileUntouched(t *testing.T) {
	clock := ui.NewActivityClock(time.Now())
	cadence := newRefreshCadence(clock)

	if cadence.interval != fleetRefreshInterval {
		t.Fatalf(
			"opening interval = %s, want %s",
			cadence.interval, fleetRefreshInterval,
		)
	}

	// growth is steep enough that ONE untouched pass already crosses
	// fleetRefreshParkThreshold (5s * 13 = 65s > 60s) — at several
	// CPU-seconds a pass on a real fleet (~50 tmux sockets, ~1950
	// processes measured on devbox 2026-09-03), even a gentle multi-step
	// ramp risks a pass landing inside any given 30s measurement window.
	if got := cadence.next(); got != fleetRefreshParkThreshold {
		t.Fatalf("interval after 1 untouched pass = %s, want the park threshold %s",
			got, fleetRefreshParkThreshold)
	}
	if got := cadence.next(); got != fleetRefreshParkThreshold {
		t.Fatalf("interval after 2 untouched passes = %s, want it to stay at %s",
			got, fleetRefreshParkThreshold)
	}
}

func TestRefreshCadenceResetsOnInteraction(t *testing.T) {
	clock := ui.NewActivityClock(time.Now())
	cadence := newRefreshCadence(clock)
	for range 20 {
		cadence.next()
	}
	if cadence.interval <= fleetRefreshInterval {
		t.Fatalf(
			"interval after 20 untouched passes = %s, want > %s",
			cadence.interval, fleetRefreshInterval,
		)
	}

	clock.Stamp(time.Now().Add(time.Second))
	if got := cadence.next(); got != fleetRefreshInterval {
		t.Fatalf(
			"interval after a keystroke = %s, want %s: a picker under an "+
				"active user must snap back to full cadence",
			got, fleetRefreshInterval,
		)
	}
}

func TestRefreshCadenceCapsAndNilClockNeverBacksOff(t *testing.T) {
	cadence := newRefreshCadence(ui.NewActivityClock(time.Now()))
	for range 500 {
		if got := cadence.next(); got > fleetRefreshParkThreshold {
			t.Fatalf("interval %s exceeded the cap %s",
				got, fleetRefreshParkThreshold)
		}
	}
	if cadence.interval != fleetRefreshParkThreshold {
		t.Fatalf("interval after 500 passes = %s, want the cap %s",
			cadence.interval, fleetRefreshParkThreshold)
	}

	// A nil clock is every non-interactive caller: no presence signal must
	// ever be READ as "nobody is there".
	nilCadence := newRefreshCadence(nil)
	for range 50 {
		if got := nilCadence.next(); got != fleetRefreshInterval {
			t.Fatalf("nil-clock interval = %s, want a steady %s",
				got, fleetRefreshInterval)
		}
	}
}

// TestPickerRefreshStreamParksThenWakesOnKeystroke pins the fix in the LIVE
// loop, not just in the cadence arithmetic. A picker used to refresh on a
// fixed ticker for its whole life, and one pass costs several CPU-seconds on
// a real fleet — a tmux fork+exec per live socket plus a full store read and
// a per-process scan for every engine detector — so a picker left in a pane
// nobody watched ground over half a core indefinitely (2026-09-03 real-box
// measurement, 1741 ticks/30s).
//
// The unit tests above prove next() computes the right numbers. Only this
// one proves the loop actually STOPS asking: it must send exactly the two
// passes the transition into park allows (the initial synchronous pass, then
// the one loop-driven pass that crosses fleetRefreshParkThreshold), then go
// silent — no third pass, ever, until the activity clock moves — and a
// keystroke must wake it within one park-poll interval, not a stale
// already-scheduled multi-minute timer.
func TestPickerRefreshStreamParksThenWakesOnKeystroke(t *testing.T) {
	shortenRefreshIntervals(t)
	jailTest(t)

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan ui.Snapshot, 1)
	var stderr bytes.Buffer
	runner := &immediateIndexRunner{}
	clock := ui.NewActivityClock(time.Now())
	go streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) {
				return runner, nil
			},
			activity: clock,
		},
	)

	completed := 0
	starve := time.After(20 * time.Second)
	for completed < 2 {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatalf("refresh stream closed after %d of 2 passes: %s", completed, stderr.String())
			}
			if !snapshot.Refreshing {
				completed++
			}
		case <-starve:
			t.Fatalf("refresh stream produced %d of 2 passes in 20s: %s", completed, stderr.String())
		}
	}

	// Parked: no further pass for well over several fleetRefreshParkPollInterval
	// ticks proves the loop actually stopped, not merely slowed to something
	// this test's patience could still outlast.
	select {
	case snapshot, ok := <-updates:
		if ok {
			t.Fatalf("a third pass arrived while parked and untouched: %#v", snapshot)
		}
	case <-time.After(50 * time.Millisecond):
	}

	// A keystroke must wake it inside a poll or two — not wait on an
	// already-scheduled timer that was never armed for anything this soon.
	clock.Stamp(time.Now())
	select {
	case _, ok := <-updates:
		if !ok {
			t.Fatal("refresh stream closed instead of waking on a keystroke")
		}
	case <-time.After(time.Second):
		t.Fatal("no pass arrived within 1s of a keystroke while parked")
	}
	cancel()
	for range updates {
	}
}

// The observer samples Now before notifying the test. Gating Timer.C keeps
// each key at a known select boundary; a second boundary proves a fresh key
// finished without starting a pass, rather than relying on a quiet interval.
type refreshObservedClock struct {
	*clock.Fake
	ctx       context.Context
	reads     chan time.Time
	selecting chan chan struct{}
}

func (observed *refreshObservedClock) Now() time.Time {
	now := observed.Fake.Now()
	select {
	case observed.reads <- now:
	case <-observed.ctx.Done():
	}
	return now
}

func (observed *refreshObservedClock) NewTimer(d time.Duration) clock.Timer {
	return &refreshObservedTimer{Timer: observed.Fake.NewTimer(d), observed: observed}
}

type refreshObservedTimer struct {
	clock.Timer
	observed *refreshObservedClock
}

func (timer *refreshObservedTimer) C() <-chan time.Time {
	resume := make(chan struct{})
	select {
	case timer.observed.selecting <- resume:
		select {
		case <-resume:
		case <-timer.observed.ctx.Done():
		}
	case <-timer.observed.ctx.Done():
	}
	return timer.Timer.C()
}

func (observed *refreshObservedClock) waitNow(t *testing.T, want time.Time) {
	t.Helper()
	select {
	case got := <-observed.reads:
		if !got.Equal(want) {
			t.Fatalf("refresh clock read = %v, want %v", got, want)
		}
	case <-observed.selecting:
		t.Fatal("stream went idle instead of reading the clock for this key's pass")
	case <-time.After(20 * time.Second):
		t.Fatal("stream never read the clock for this key's pass")
	}
}

func (observed *refreshObservedClock) waitSelect(t *testing.T) chan struct{} {
	t.Helper()
	select {
	case resume := <-observed.selecting:
		return resume
	case got := <-observed.reads:
		t.Fatalf("unexpected refresh clock read at %v; want an idle stream before the next key", got)
	case <-time.After(20 * time.Second):
		t.Fatal("stream never returned to its select boundary")
	}
	return nil
}

func fakeRefreshStream(
	t *testing.T,
	runner indexRunner,
) (*refreshObservedClock, *ui.ActivityClock, <-chan ui.Snapshot, *bufferedWarnings) {
	t.Helper()
	previousInterval, previousThreshold := fleetRefreshInterval, fleetRefreshParkThreshold
	previousPoll, previousStale := fleetRefreshParkPollInterval, fleetRefreshStaleAfter
	fleetRefreshInterval, fleetRefreshParkThreshold = 30*time.Minute, time.Hour
	fleetRefreshParkPollInterval, fleetRefreshStaleAfter = time.Hour, 8*time.Second
	t.Cleanup(func() {
		fleetRefreshInterval, fleetRefreshParkThreshold = previousInterval, previousThreshold
		fleetRefreshParkPollInterval, fleetRefreshStaleAfter = previousPoll, previousStale
	})
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan ui.Snapshot, 4)
	t.Cleanup(func() {
		cancel()
		for range updates {
		}
	})
	start := time.Unix(1_800_000_000, 0)
	observed := &refreshObservedClock{
		Fake: clock.NewFake(start), ctx: ctx,
		reads: make(chan time.Time), selecting: make(chan chan struct{}),
	}
	activity := ui.NewActivityClock(start)
	warnings := &bufferedWarnings{}
	go streamFleetRefreshesWith(ctx, database, scanRequest{}, warnings.add, warnings, updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) { return runner, nil },
			activity:   activity, clock: observed,
		},
	)
	observed.waitNow(t, start)
	return observed, activity, updates, warnings
}

func awaitSettledRefresh(t *testing.T, updates <-chan ui.Snapshot) {
	t.Helper()
	for {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatal("refresh stream closed before publishing the pass")
			}
			if !snapshot.Refreshing {
				return
			}
		case <-time.After(20 * time.Second):
			t.Fatal("refresh pass never published")
		}
	}
}

func TestPickerRefreshStreamWakesOnKeystrokeWithoutWaitingForThePoll(t *testing.T) {
	runner := &immediateIndexRunner{}
	observed, activity, updates, _ := fakeRefreshStream(t, runner)
	awaitSettledRefresh(t, updates)
	resume := observed.waitSelect(t)

	observed.Advance(time.Second)
	activity.Stamp(observed.Fake.Now())
	close(resume)
	observed.waitNow(t, time.Unix(1_800_000_001, 0))
	resume = observed.waitSelect(t)
	runner.mutex.Lock()
	calls := len(runner.options)
	runner.mutex.Unlock()
	if calls != 1 {
		t.Fatalf("index calls after a fresh key = %d, want 1", calls)
	}

	observed.Advance(8 * time.Second)
	activity.Stamp(observed.Fake.Now())
	close(resume)
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	awaitSettledRefresh(t, updates)
	runner.mutex.Lock()
	calls = len(runner.options)
	runner.mutex.Unlock()
	if calls != 2 {
		t.Fatalf("index calls after a stale key with no timer due = %d, want 2", calls)
	}
}

func TestPickerRefreshStreamMeasuresStalenessFromThePassStartTheHeaderReads(t *testing.T) {
	runner := &slowIndexRunner{started: make(chan struct{}), release: make(chan struct{})}
	observed, activity, updates, _ := fakeRefreshStream(t, runner)
	select {
	case <-runner.started:
	case <-time.After(20 * time.Second):
		t.Fatal("first pass never reached the held indexer")
	}
	observed.Advance(9 * time.Second)
	close(runner.release)
	awaitSettledRefresh(t, updates)
	resume := observed.waitSelect(t)
	activity.Stamp(observed.Fake.Now())
	close(resume)
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	awaitSettledRefresh(t, updates)
	runner.mutex.Lock()
	calls := len(runner.options)
	runner.mutex.Unlock()
	if calls != 2 {
		t.Fatalf("index calls after a key at the slow pass's publication = %d, want 2", calls)
	}
}

type retryFailingIndexRunner struct {
	immediateIndexRunner
}

func (runner *retryFailingIndexRunner) Run(
	ctx context.Context,
	options fleetindex.Options,
) (fleetindex.Counters, error) {
	counters, err := runner.immediateIndexRunner.Run(ctx, options)
	if err != nil {
		return counters, err
	}
	runner.mutex.Lock()
	calls := len(runner.options)
	runner.mutex.Unlock()
	if calls >= 2 {
		return counters, errors.New("index unavailable")
	}
	return counters, nil
}

func TestPickerRefreshStreamCountsFailedAttempts(t *testing.T) {
	runner := &retryFailingIndexRunner{}
	observed, activity, updates, warnings := fakeRefreshStream(t, runner)
	awaitSettledRefresh(t, updates)
	resume := observed.waitSelect(t)

	observed.Advance(9 * time.Second)
	activity.Stamp(observed.Fake.Now())
	close(resume)
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	resume = observed.waitSelect(t)

	activity.Stamp(observed.Fake.Now())
	close(resume)
	observed.waitNow(t, time.Unix(1_800_000_009, 0))
	resume = observed.waitSelect(t)
	runner.mutex.Lock()
	calls := len(runner.options)
	runner.mutex.Unlock()
	if calls != 2 {
		t.Fatalf("index calls after a second key at the failed attempt = %d, want 2", calls)
	}

	observed.Advance(8 * time.Second)
	activity.Stamp(observed.Fake.Now())
	close(resume)
	observed.waitNow(t, time.Unix(1_800_000_017, 0))
	observed.waitNow(t, time.Unix(1_800_000_017, 0))
	observed.waitSelect(t)
	runner.mutex.Lock()
	calls = len(runner.options)
	runner.mutex.Unlock()
	if calls != 3 {
		t.Fatalf("index calls after the failed-attempt bound = %d, want 3", calls)
	}

	var stderr bytes.Buffer
	warnings.flush(&stderr)
	if got, want := stderr.String(), "pfm refresh index: index unavailable\n"; got != want {
		t.Fatalf("held stream failures = %q, want one distinct error %q", got, want)
	}
	stderr.Reset()
	warnings.flush(&stderr)
	if got := stderr.String(); got != "" {
		t.Fatalf("second flush = %q; want no repeated stream error", got)
	}
}

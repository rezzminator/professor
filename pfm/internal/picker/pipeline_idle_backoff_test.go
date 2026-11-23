package picker

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// parkBoundaryClock is a fake clock whose timers tell the test each time the
// refresh stream comes back to its select: the stream reads Timer.C once per
// loop turn, so one receipt on boundary is one finished turn.
type parkBoundaryClock struct {
	*clock.Fake
	boundary chan struct{}
}

func (observed *parkBoundaryClock) NewTimer(d time.Duration) clock.Timer {
	return &parkBoundaryTimer{Timer: observed.Fake.NewTimer(d), observed: observed}
}

type parkBoundaryTimer struct {
	clock.Timer
	observed *parkBoundaryClock
}

func (timer *parkBoundaryTimer) C() <-chan time.Time {
	select {
	case timer.observed.boundary <- struct{}{}:
	default: // a test that is not counting turns lets them pass
	}
	return timer.Timer.C()
}

func TestPickerRefreshStreamParksThenWakesOnKeystroke(t *testing.T) {
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

	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	updates := make(chan ui.Snapshot, 1)
	var stderr bytes.Buffer
	runner := &immediateIndexRunner{}
	epoch := time.Unix(1_800_000_000, 0)
	observed := &parkBoundaryClock{Fake: clock.NewFake(epoch), boundary: make(chan struct{}, 64)}
	activity := ui.NewActivityClock(epoch)
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
			activity: activity,
			clock:    observed,
		},
	)
	settled := func(what string) {
		t.Helper()
		for {
			snapshot, ok := testwait.Receive(t, what, updates)
			if !ok {
				t.Fatalf("refresh stream closed while %s: %s", what, stderr.String())
			}
			if !snapshot.Refreshing {
				return
			}
		}
	}
	turn := func(what string) { testwait.Receive(t, what, observed.boundary) }

	// The opening pass, then one timed pass: the idle cadence backs off past
	// the park threshold on it.
	settled("the opening pass to publish")
	turn("the stream to wait on its first timer")
	observed.Advance(fleetRefreshInterval)
	settled("the second pass to publish")
	turn("the stream to wait on its park poll")
	runner.mutex.Lock()
	passes := len(runner.options)
	runner.mutex.Unlock()

	// Parked: each park poll that passes (the stream back at its select, one
	// boundary per poll) starts no pass and publishes nothing.
	for poll := 1; poll <= 3; poll++ {
		observed.Advance(fleetRefreshParkPollInterval)
		turn("the park poll to finish")
		if len(updates) != 0 {
			t.Fatalf("a pass published while parked after %d polls: %#v", poll, <-updates)
		}
	}
	runner.mutex.Lock()
	calls := len(runner.options)
	runner.mutex.Unlock()
	if calls != passes {
		t.Fatalf("index calls after 3 park polls = %d, want the %d before parking", calls, passes)
	}

	// A keystroke must wake it inside a poll or two: the next poll sees the
	// new stamp, drops the park and runs a pass.
	activity.Stamp(observed.Now())
	observed.Advance(fleetRefreshParkPollInterval)
	settled("a pass to arrive after a keystroke while parked")
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
	case <-testwait.Context(t).Done():
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
	case <-testwait.Context(t).Done():
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
		case <-testwait.Context(t).Done():
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
	testwait.Receive(t, "the first pass to reach the held indexer", runner.started)
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

// A failed pass backs the timer retry off (fleetRefreshInterval, doubling to
// the park threshold; see pipeline_failure_backoff_test.go), but a keystroke
// still attempts at once when the last attempt is fleetRefreshStaleAfter old:
// the second attempt below lands 9 s after the first with a 30 min backoff
// pending, and a key inside the bound starts nothing.
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

// toggledFailingIndexRunner fails every index stage while failing is set.
type toggledFailingIndexRunner struct {
	immediateIndexRunner
	failing atomic.Bool
}

func (runner *toggledFailingIndexRunner) Run(
	ctx context.Context,
	options fleetindex.Options,
) (fleetindex.Counters, error) {
	counters, err := runner.immediateIndexRunner.Run(ctx, options)
	if err != nil {
		return counters, err
	}
	if runner.failing.Load() {
		return counters, errors.New("index unavailable")
	}
	return counters, nil
}

// TestPickerRefreshStreamPublishesAFailedPassAndStaysOpen pins root cause C at
// its source: a pass that errors used to write stderr alone — invisible under
// the TUI's alt-screen, so the picker kept showing the last rows as current —
// and a failure in the OPENING pass returned, closing the stream for good.
// Now every failure is a snapshot carrying the stage and the error, the stream
// stays open and retries on its cadence, and the first pass that lands
// publishes a snapshot with no failure, which is what clears the picker's mark.
func TestPickerRefreshStreamPublishesAFailedPassAndStaysOpen(t *testing.T) {
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
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	updates := make(chan ui.Snapshot, 1)
	var stderr bytes.Buffer
	runner := &toggledFailingIndexRunner{}
	runner.failing.Store(true)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
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
			clock: fake,
		},
	)
	next := func(what string) ui.Snapshot {
		t.Helper()
		snapshot, open := nextRefreshSnapshot(t, what, updates, fake)
		if !open {
			t.Fatalf("refresh stream closed while %s: %s", what, stderr.String())
		}
		return snapshot
	}

	var failure *ui.RefreshFailure
	for failure == nil {
		failure = next("waiting for the failed pass").RefreshFailure
	}
	if failure.Stage != "project index" || !strings.Contains(failure.Err, "index unavailable") || failure.AtNS == 0 {
		t.Fatalf("failure = %#v, want the project index stage, its error and a time", failure)
	}

	runner.failing.Store(false)
	for {
		snapshot := next("waiting for the pass that clears the failure")
		if snapshot.RefreshFailure == nil && !snapshot.Refreshing {
			break
		}
	}
	cancel()
	for range updates {
	}
}

package picker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// TestPickerRefreshStreamBacksOffConsecutiveFailedPasses pins the retry
// cadence of a refresh that keeps failing. A parked picker with a pending
// retry used to run a full pass on every park poll, so a broken tmux or index
// cost a full pass every 2 s for as long as the picker stayed open. Now the
// first retry waits fleetRefreshInterval, each further consecutive failure
// doubles the wait, and fleetRefreshParkThreshold caps it.
//
// The stream runs on a fake clock the test steps one second at a time, only
// while the stream sits at its select boundary, so every pass has an exact
// time: failures land at 0 (the opening pass), then 10, 30, 70 and 110 s with
// the interval at 10 s and the cap at 40 s.
func TestPickerRefreshStreamBacksOffConsecutiveFailedPasses(t *testing.T) {
	testRefreshFailureBackoff(t, false)
}

func TestPickerRefreshStreamBacksOffFailedNativePublications(t *testing.T) {
	testRefreshFailureBackoff(t, true)
}

func testRefreshFailureBackoff(t *testing.T, native bool) {
	t.Helper()
	previousInterval, previousThreshold := fleetRefreshInterval, fleetRefreshParkThreshold
	previousPoll, previousStale := fleetRefreshParkPollInterval, fleetRefreshStaleAfter
	fleetRefreshInterval, fleetRefreshParkThreshold = 10*time.Second, 40*time.Second
	fleetRefreshParkPollInterval, fleetRefreshStaleAfter = 2*time.Second, time.Hour
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
			t.Errorf("close database: %v", err)
		}
	})
	if native {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PFM_STATE_DB", filepath.Join(blocker, "state.db"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan ui.Snapshot, 64)
	var (
		mutex    sync.Mutex
		failedAt []int64
		stages   []string
		drained  = make(chan struct{})
	)
	go func() {
		defer close(drained)
		for snapshot := range updates {
			if snapshot.RefreshFailure != nil {
				mutex.Lock()
				failedAt = append(failedAt, snapshot.RefreshFailure.AtNS)
				stages = append(stages, snapshot.RefreshFailure.Stage)
				mutex.Unlock()
			}
		}
	}()
	start := time.Unix(1_800_000_000, 0)
	observed := &armedClock{refreshObservedClock: &refreshObservedClock{
		Fake: clock.NewFake(start), ctx: ctx,
		reads: make(chan time.Time), selecting: make(chan chan struct{}),
	}}
	runner := &toggledFailingIndexRunner{}
	runner.failing.Store(!native)
	var warnings bufferedWarnings
	go streamFleetRefreshesWith(ctx, database, scanRequest{}, warnings.add, &warnings, updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) { return runner, nil },
			activity:   ui.NewActivityClock(start), clock: observed,
		},
	)

	const horizon = 120 * time.Second
	for observed.Fake.Now().Sub(start) < horizon {
		select {
		case <-observed.reads:
		case resume := <-observed.selecting:
			// The stream sits at its select with its timer armed: jump to the
			// moment that timer is due, so every wake is exact.
			observed.Advance(time.Unix(0, observed.dueNS.Load()).Sub(observed.Fake.Now()))
			close(resume)
		case <-time.After(20 * time.Second):
			t.Fatalf("refresh stream never returned to its select boundary at %v", observed.Fake.Now().Sub(start))
		}
	}
	cancel()
	<-drained

	mutex.Lock()
	defer mutex.Unlock()
	if native {
		for _, stage := range stages {
			if stage != "native seats" {
				t.Fatalf("fixture failed at %q instead of native publication", stage)
			}
		}
		if len(failedAt) != 5 {
			t.Fatalf("native failed passes = %d, want 5 under doubling backoff (stages %v)", len(failedAt), stages)
		}
		return
	}
	var got []int
	for _, at := range failedAt {
		got = append(got, int(time.Unix(0, at).Sub(start)/time.Second))
	}
	if want := []int{0, 10, 30, 70, 110}; !slices.Equal(got, want) {
		t.Fatalf("failed passes ran at %v s, want %v (interval, then doubling to the 40 s cap)", got, want)
	}
}

// armedClock records when the stream's timer is next due, so a test can jump
// the fake clock straight to it.
type armedClock struct {
	*refreshObservedClock
	dueNS atomic.Int64
}

func (armed *armedClock) NewTimer(d time.Duration) clock.Timer {
	armed.arm(d)
	return &armedTimer{Timer: armed.refreshObservedClock.NewTimer(d), armed: armed}
}

func (armed *armedClock) arm(d time.Duration) {
	armed.dueNS.Store(armed.Fake.Now().Add(d).UnixNano())
}

type armedTimer struct {
	clock.Timer
	armed *armedClock
}

func (timer *armedTimer) Reset(d time.Duration) bool {
	timer.armed.arm(d)
	return timer.Timer.Reset(d)
}

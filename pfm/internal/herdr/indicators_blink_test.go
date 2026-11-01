package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// countPaneLists wraps the painter's call to count the pane.list requests it
// sends: one per sidebar paint.
func countPaneLists(painter *indicatorPainter) *int {
	lists := 0
	call := painter.call
	painter.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		for _, line := range lines {
			if strings.Contains(string(line), `"pane.list"`) {
				lists++
			}
		}
		return call(ctx, socket, lines...)
	}
	return &lists
}

// TestIdleTabBlinkerRereadsTheFleetOnlyWhenNudgedOrDue pins the idle cadence:
// with every tab decorated but nothing blocked, done or animated, a tick does
// no list and no fleet scan; a hook-fired painter's nudge makes the next tick
// re-read, and idleRefresh after the last read one tick re-reads unprompted.
func TestIdleTabBlinkerRereadsTheFleetOnlyWhenNudgedOrDue(t *testing.T) {
	f := newTabStatusFixture()
	painter, stepping := tabPainterFixture(t, f)
	lists := countPaneLists(painter)
	scans := 0
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		scans++
		return []TabDecoration{tabObservation("idle")}, nil
	}
	const ticks = 24
	var scansAt []int
	stepping.onSleep = func(context.Context) error {
		scansAt = append(scansAt, scans)
		if len(stepping.sleeps) == 3 {
			painter.nudge() // a hook fired while the blinker held the lock
		}
		if len(stepping.sleeps) > ticks {
			return context.Canceled
		}
		return nil
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	// The first sleep is 400 ms (phaseOn), so tick k ends at 400+(k-1)*500 ms:
	// the nudge is read at tick 3, and idleRefresh after it lands on tick 23.
	if scans != 3 || *lists != 3 {
		t.Fatalf("scans %d, lists %d over %d ticks; want 3 each: the first paint, the nudged tick, the due tick",
			scans, *lists, ticks)
	}
	if scansAt[3] != 2 || scansAt[22] != 2 || scansAt[23] != 3 {
		t.Fatalf("scans before each sleep = %v; want the nudge read at tick 3 and the refresh at tick 23", scansAt)
	}
	if _, err := os.Stat(painter.nudgePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nudge after the blinker read it: %v, want it removed", err)
	}
}

// TestBlockedTabAnimatesFromTheObservationsAlreadyRead pins that an animated
// tab advances a frame every tick without a fleet scan per tick.
func TestBlockedTabAnimatesFromTheObservationsAlreadyRead(t *testing.T) {
	f := newTabStatusFixture()
	painter, stepping := tabPainterFixture(t, f)
	scans := 0
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		scans++
		d := tabObservation("blocked")
		d.Glyphs.Blocked = blockedFrames
		return []TabDecoration{d}, nil
	}
	stepping.onSleep = func(context.Context) error {
		if len(stepping.sleeps) > 4 {
			return context.Canceled
		}
		return nil
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	want := []string{"🔴 Chat", "⭕ Chat", "🔴 Chat", "⭕ Chat", "🔴 Chat"}
	if !slices.Equal(f.renames, want) || scans != 1 {
		t.Fatalf("renames %q, scans %d; want a frame per tick %q from the one scan", f.renames, scans, want)
	}
}

// TestHookFiredPainterNudgesTheRunningBlinker pins the other half of the
// nudge: a tab painter that finds the blinker lock held leaves the nudge and
// does no work of its own.
func TestHookFiredPainterNudgesTheRunningBlinker(t *testing.T) {
	painter, _, _ := newTestPainter(t, &fakeIndicatorHerdr{}, phaseOn)
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		t.Fatal("a painter that lost the lock scanned the fleet")
		return nil, nil
	}
	release, held, err := painter.takeBlinkerLock()
	if err != nil || !held {
		t.Fatalf("take lock: %t %v", held, err)
	}
	defer release()
	if err := painter.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(painter.nudgePath()); err != nil {
		t.Fatalf("nudge = %v, want it left for the blinker", err)
	}
}

// TestBlinkerStopsWhenItsHerdrServerIsGone pins the owner check: a blinker
// whose socket vanished, or was taken by a newly started Herdr server, stops
// at that tick without further writes or a failure; one whose server refuses
// connections stops at the first refused repaint.
func TestBlinkerStopsWhenItsHerdrServerIsGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		owner  func(tick int) (int64, error)
		refuse bool
		want   string
	}{
		{
			name: "socket removed",
			owner: func(tick int) (int64, error) {
				if tick >= 2 {
					return 0, fmt.Errorf("stat Herdr socket: %w", os.ErrNotExist)
				}
				return 1, nil
			},
			want: "its socket is gone",
		},
		{
			name: "new server on the socket",
			owner: func(tick int) (int64, error) {
				if tick >= 2 {
					return 2, nil
				}
				return 1, nil
			},
			want: "a new Herdr server took its socket",
		},
		{
			name:   "server refuses connections",
			owner:  func(int) (int64, error) { return 1, nil },
			refuse: true,
			want:   "refuses connections",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := blockedHerdr()
			if tc.refuse {
				server.failList = func(listNo int) error {
					if listNo >= 3 {
						return fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
					}
					return nil
				}
			}
			painter, stepping, stderr := newTestPainter(t, server, phaseOn)
			painter.cadence.owner = func() (int64, error) { return tc.owner(len(stepping.sleeps)) }
			stepping.onSleep = func(context.Context) error {
				if len(stepping.sleeps) > 10 {
					return context.Canceled
				}
				return nil
			}
			if err := painter.run(context.Background()); err != nil {
				t.Fatalf("run = %v, want a clean stop; stderr %q", err, stderr)
			}
			if len(stepping.sleeps) != 2 || server.lists != 3-boolInt(!tc.refuse) {
				t.Fatalf("sleeps %d lists %d: want the stop at the second tick", len(stepping.sleeps), server.lists)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr %q lacks %q", stderr, tc.want)
			}
		})
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestCancelledTabPainterRestoresWithinItsBudget pins that a signal's
// cancellation never waits on an unbounded restore: a tab whose owner check
// hangs is given up at the restore budget, named in the returned error.
func TestCancelledTabPainterRestoresWithinItsBudget(t *testing.T) {
	f := newTabStatusFixture()
	painter, stepping := tabPainterFixture(t, f)
	painter.cadence.restoreBudget = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		d := tabObservation("blocked")
		d.Validate = func(validateCtx context.Context) (bool, error) {
			if ctx.Err() == nil {
				return true, nil
			}
			<-validateCtx.Done() // the restore's owner check hangs until its budget ends
			return false, validateCtx.Err()
		}
		return []TabDecoration{d}, nil
	}
	stepping.onSleep = func(sleepCtx context.Context) error {
		cancel()
		return sleepCtx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- painter.run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("run = %v, want the restore given up at its budget", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled painter's restore outlived its budget")
	}
}

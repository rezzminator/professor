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

// TestBlinkerStopsWhenItsHerdrServerIsGone pins the owner check: a blinker
// whose socket vanished, or was taken by a newly started Herdr server, stops
// at that tick without further writes or a failure; one whose server refuses
// connections stops at the first refused repaint.
func TestBlinkerStopsWhenItsHerdrServerIsGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		owner  func(tick int) (int64, error)
		refuse error
		want   string
		// only says stderr is want alone: the stop is no failure, so no diagnostic is written.
		only bool
		// stop writes the theme off's stop marker ahead of the second tick, whose clear meets refuse.
		stop bool
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
			refuse: fmt.Errorf("dial: %w", syscall.ECONNREFUSED),
			want:   "refuses connections",
		},
		{
			name:   "server shutting down",
			owner:  func(int) (int64, error) { return 1, nil },
			refuse: &Refusal{Request: "pane.list", Code: "server_unavailable", Message: "server is shutting down"},
			want:   "herdr indicators: socket /fake/herdr.sock stands down: its Herdr server is shutting down\n",
			only:   true,
		},
		{
			name:   "theme off clears as the server shuts down",
			owner:  func(int) (int64, error) { return 1, nil },
			refuse: &Refusal{Request: "pane.list", Code: "server_unavailable", Message: "server is shutting down"},
			want:   "herdr indicators: socket /fake/herdr.sock stands down: its Herdr server is shutting down\n",
			only:   true,
			stop:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := blockedHerdr()
			if tc.refuse != nil {
				server.failList = func(listNo int) error {
					if listNo >= 3 {
						return tc.refuse
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
				if tc.stop && len(stepping.sleeps) == 2 {
					if err := os.WriteFile(painter.stopPath(), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			if err := painter.run(context.Background()); err != nil {
				t.Fatalf("run = %v, want a clean stop; stderr %q", err, stderr)
			}
			if len(stepping.sleeps) != 2 || server.lists != 3-boolInt(tc.refuse == nil) {
				t.Fatalf("sleeps %d lists %d: want the stop at the second tick", len(stepping.sleeps), server.lists)
			}
			if got := stderr.String(); !strings.Contains(got, tc.want) || tc.only && got != tc.want {
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

// TestIdleTabBlinkerRereadsWhenItsProbeSeesAChange pins the third reason an
// idle tick re-reads the tabs: the provider's probe reports a change no hook
// announced (a transcript-only failed turn, a dead engine). A probe that
// fails reads as a change and is named once while it lasts.
func TestIdleTabBlinkerRereadsWhenItsProbeSeesAChange(t *testing.T) {
	f := newTabStatusFixture()
	painter, stepping := tabPainterFixture(t, f)
	stderr := &strings.Builder{}
	painter.stderr = stderr
	scans, probes := 0, 0
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		scans++
		return []TabDecoration{tabObservation("idle")}, nil
	}
	painter.tabsChanged = func(context.Context, string) (bool, error) {
		probes++
		switch len(stepping.sleeps) {
		case 4:
			return true, nil
		case 6, 7:
			return false, errors.New("pane.list refused")
		}
		return false, nil
	}
	const ticks = 9
	var scansAt []int
	stepping.onSleep = func(context.Context) error {
		scansAt = append(scansAt, scans)
		if len(stepping.sleeps) > ticks {
			return context.Canceled
		}
		return nil
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	// scansAt[k] is the count before sleep k+1, so after tick k.
	if want := []int{1, 1, 1, 1, 2, 2, 3, 4, 4, 4}; !slices.Equal(scansAt, want) {
		t.Fatalf(
			"scans after each tick = %v, want %v: a re-read at the changed tick and each failed probe",
			scansAt,
			want,
		)
	}
	if probes != ticks {
		t.Fatalf("probes = %d over %d ticks, want one per tick", probes, ticks)
	}
	if got := strings.Count(stderr.String(), "probe the tabs of socket"); got != 1 ||
		!strings.Contains(stderr.String(), "pane.list refused") {
		t.Fatalf("stderr %q: want the failing probe named once with its cause", stderr)
	}
}

// A blocked tab keeps playing every frame on the ticks that reuse the last
// tab read, while its pane's revision moves under the same agent: the
// painter's own sidebar reports bump it each tick, so a tab write gated on the
// revision read at the scan drew only the scanned tick's frame.
func TestTabPainterAnimatesBlockedWhileItsPaneRevisionMoves(t *testing.T) {
	f := newTabStatusFixture()
	painter, stepping := tabPainterFixture(t, f)
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		d := tabObservation("blocked")
		d.Glyphs.Blocked = blockedFrames
		return []TabDecoration{d}, nil
	}
	stepping.onSleep = func(context.Context) error {
		if len(stepping.sleeps) > 4 {
			return context.Canceled
		}
		f.revision++ // a sidebar report between the ticks
		return nil
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	want := []string{"🔴 Chat", "⭕ Chat", "🔴 Chat", "⭕ Chat", "🔴 Chat"}
	if !slices.Equal(f.renames, want) {
		t.Fatalf("renames = %q, want a frame per tick %q", f.renames, want)
	}
}

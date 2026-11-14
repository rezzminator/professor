package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestHookFiredPainterNudgesTheRunningBlinker pins the other half of the
// nudge: a tab painter that finds the blinker lock held leaves the nudge and
// does no work of its own.
func TestHookFiredPainterNudgesTheRunningBlinker(t *testing.T) {
	painter, _, _ := newTestPainter(t, &fakeIndicatorHerdr{}, phaseOn)
	painter.tabs = func(context.Context, string, bool) ([]TabDecoration, error) {
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

// TestStoppingBlinkerTakesTheNudgeLeftAsItStopped pins the lost wake-up: a
// hook fired while the blinker's last read found no tab (a chat being born)
// nudges a blinker about to stop; once it lets go of the lock it takes the
// nudge, takes the lock again and paints the new tab. A nudge it cannot take
// never spins it.
func TestStoppingBlinkerTakesTheNudgeLeftAsItStopped(t *testing.T) {
	observation := tabObservation("idle")
	observation.AgentStatus = "idle"
	idle := []TabDecoration{observation}
	for _, tc := range []struct {
		name  string
		reads [][]TabDecoration // the tab reads in order; an empty read is the one a hook nudged
		stuck bool              // the nudge path is a directory no Remove takes
		label string
	}{
		{"stops after its first paint", [][]TabDecoration{nil, idle}, false, "⚪ Chat"},
		{"stops at a tick", [][]TabDecoration{idle, nil, idle}, false, "⚪ Chat"},
		{"a nudge it cannot take", [][]TabDecoration{nil}, true, "Chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTabStatusFixture()
			f.state = "idle"
			painter, stepping := tabPainterFixture(t, f)
			painter.cadence.idleRefresh = time.Nanosecond // every tick re-reads the tabs
			if tc.stuck {
				if err := os.MkdirAll(filepath.Join(painter.nudgePath(), "held"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			painter.tabs = func(context.Context, string, bool) ([]TabDecoration, error) {
				if reads == len(tc.reads) {
					t.Fatalf("tab read %d, want %d", reads+1, len(tc.reads))
				}
				read := tc.reads[reads]
				reads++
				if len(read) == 0 {
					f.label = "Chat" // the born chat's tab, bare
					painter.nudge()  // its hook found the lock held
				}
				return slices.Clone(read), nil
			}
			stepping.onSleep = func(context.Context) error {
				if reads == len(tc.reads) {
					return context.Canceled
				}
				return nil
			}
			if err := painter.run(context.Background()); err != nil {
				t.Fatalf("run = %v", err)
			}
			if reads != len(tc.reads) || f.label != tc.label {
				t.Fatalf("tab reads %d, label %q; want %d reads and the label %q",
					reads, f.label, len(tc.reads), tc.label)
			}
			if _, err := os.Stat(painter.nudgePath()); !tc.stuck && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("nudge %s after the repaint: %v, want it taken", painter.nudgePath(), err)
			}
		})
	}
}

// TestNudgeIndicatorsLeavesTheSocketsNudge pins the door a rename calls: the
// nudge lands in the machine's lock directory under the socket's key, and a
// machine no painter ever ran on (no lock directory) is nothing to nudge.
func TestNudgeIndicatorsLeavesTheSocketsNudge(t *testing.T) {
	home, err := (paths.OSEnv{}).Home()
	if err != nil {
		t.Fatal(err)
	}
	lockDir := paths.HerdrLockDir(home)
	if err := os.RemoveAll(lockDir); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "herdr.sock")
	nudge := filepath.Join(lockDir, "indicators-"+socketKey(socket)+".nudge")
	if err := NudgeIndicators(socket); err != nil {
		t.Fatalf("nudge with no lock directory = %v, want nil", err)
	}
	if _, err := os.Stat(lockDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock directory after a nudge nothing reads: %v, want it absent", err)
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NudgeIndicators(socket); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nudge); err != nil {
		t.Fatalf("nudge %s = %v, want it left for the blinker", nudge, err)
	}
}

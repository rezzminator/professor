package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

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

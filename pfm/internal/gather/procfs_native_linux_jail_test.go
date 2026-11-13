//go:build jail && linux

package gather

import (
	"os/exec"
	"testing"
	"time"
)

// A process's start lands on the wall clock between the moments before and
// after its spawn, within the one 10 ms tick the kernel counts it in.
func TestLinuxProcFSStartedPlacesTheSpawnOnTheWallClock(t *testing.T) {
	before := time.Now()
	command := exec.Command("sleep", "5")
	if err := command.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	after := time.Now()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	started, err := NewProcFS("").(ProcStart).Started(command.Process.Pid)
	if err != nil {
		t.Fatalf("Started(%d): %v", command.Process.Pid, err)
	}
	if started.Before(before.Add(-time.Second/userHZ)) || started.After(after) {
		t.Fatalf("Started(%d) = %s, want between %s (one tick early) and %s",
			command.Process.Pid, started.Format(time.RFC3339Nano), before.Format(time.RFC3339Nano),
			after.Format(time.RFC3339Nano))
	}
}

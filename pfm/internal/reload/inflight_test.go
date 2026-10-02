package reload

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestInFlightTracksThePaneMutex pins the probe a SessionEnd hook uses to tell
// a reload's /exit from a human's: true exactly while the flock is held,
// false before the file exists and after the lock is released.
func TestInFlightTracksThePaneMutex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if inFlight, err := InFlight(dir, "cc-1-1-1", "%0"); err != nil || inFlight {
		t.Fatalf("no lock file: inFlight=%v err=%v, want false/nil", inFlight, err)
	}
	path := LockPath(dir, "cc-1-1-1", "%0")
	if path != filepath.Join(dir, ".cc-1-1-1.%0.reloadlock") {
		t.Fatalf("LockPath=%q", path)
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Errorf("close lock: %v", err)
		}
	}()
	if inFlight, err := InFlight(dir, "cc-1-1-1", "%0"); err != nil || inFlight {
		t.Fatalf("file present, lock free: inFlight=%v err=%v, want false/nil", inFlight, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if inFlight, err := InFlight(dir, "cc-1-1-1", "%0"); err != nil || !inFlight {
		t.Fatalf("lock held: inFlight=%v err=%v, want true/nil", inFlight, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if inFlight, err := InFlight(dir, "cc-1-1-1", "%0"); err != nil || inFlight {
		t.Fatalf("lock released: inFlight=%v err=%v, want false/nil", inFlight, err)
	}
}

// TestInFlightReadsAConcurrentProbeAsNotInFlight pins that the probe's own
// lock is shared: the SessionEnd hook and the Codex pane reconcile probe the
// same pane, and one probe mid-flight must never read as a reload to the other.
func TestInFlightReadsAConcurrentProbeAsNotInFlight(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	probe, err := os.OpenFile(LockPath(dir, "cx-1-1-1", "%0"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := probe.Close(); err != nil {
			t.Errorf("close probe lock: %v", err)
		}
	}()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if inFlight, err := InFlight(dir, "cx-1-1-1", "%0"); err != nil || inFlight {
		t.Fatalf("another probe holds the lock: inFlight=%v err=%v, want false/nil", inFlight, err)
	}
}

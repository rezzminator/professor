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

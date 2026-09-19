package inject

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hostops/pfm/internal/clock"
)

// TestLockBeatReportsALostLock (L1-F9): beat must not silently succeed when
// the lock directory's owner file now names another pid — a delivery that
// cannot prove it still holds the lock has to find that out from beat's
// return, not from a healthy-looking nil.
func TestLockBeatReportsALostLock(t *testing.T) {
	root := t.TempDir()
	lock, err := acquireTargetLock(
		context.Background(), clock.Real, root, "sock:%1",
		time.Second, time.Millisecond, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Another holder steals the lock: chat.sh-parity theft overwrites the
	// owner file in place rather than removing and recreating the directory.
	if err := os.WriteFile(
		filepath.Join(lock.path, "owner"), []byte("999999 1\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := lock.beat(); !errors.Is(err, errLockLost) {
		t.Fatalf("beat() = %v, want errLockLost", err)
	}
}

// TestLockBeatSucceedsWhileStillOwner: the ordinary path — beat keeps
// refreshing the owner file's timestamp for as long as this pid still holds
// it.
func TestLockBeatSucceedsWhileStillOwner(t *testing.T) {
	root := t.TempDir()
	lock, err := acquireTargetLock(
		context.Background(), clock.Real, root, "sock:%1",
		time.Second, time.Millisecond, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.beat(); err != nil {
		t.Fatalf("beat() = %v, want nil while still the owner", err)
	}
}

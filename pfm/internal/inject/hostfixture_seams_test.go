package inject

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/hostfixture"
)

// TestAcquireTargetLockUnderAnOddCharacteredRoot pins hostfixture case 7
// (OddPaths) for inject: the lock namespace (chat.sh's
// ${TMPDIR:-/tmp}/chat-inject-locks, or an account's own LockRoot) must
// create, hold and release its lock directory under a root whose ancestors
// carry a space and a non-ASCII character, not split on the space or
// mis-encode the accent.
// Stays serial: hostfixture.OddPaths jails through t.Setenv, which forbids
// a parallel test in the same body.
func TestAcquireTargetLockUnderAnOddCharacteredRoot(t *testing.T) {
	fixture := hostfixture.OddPaths(t)
	root := filepath.Join(fixture.Dir, "chat-inject-locks")
	key := "/tmp/tmux-1000/cc-1-2-3:%5"

	ctx := context.Background()
	lock, err := acquireTargetLock(ctx, clock.Real, root, key, time.Second, time.Millisecond, time.Minute)
	if err != nil {
		t.Fatalf("acquireTargetLock() under an odd-charactered root error = %v", err)
	}
	owner, readErr := os.ReadFile(filepath.Join(root, lockDirName(key), "owner"))
	if readErr != nil {
		t.Fatalf("read owner file under the odd-charactered root: %v", readErr)
	}
	if len(owner) == 0 {
		t.Fatal("owner file under the odd-charactered root is empty")
	}
	lock.release()
	if _, statErr := os.Stat(filepath.Join(root, lockDirName(key))); !os.IsNotExist(statErr) {
		t.Fatalf("lock directory survived release under the odd-charactered root: stat err = %v", statErr)
	}
}

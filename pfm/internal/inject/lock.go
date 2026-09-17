package inject

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hostops/pfm/internal/clock"
)

type targetLock struct {
	path  string
	pid   int
	clock clock.Clock
}

// lockDirName mirrors chat.sh's _inject_lock_acquire filename scheme byte for
// byte (chat.sh:146-147): the "<socket>:<pane>" key with every '/', ':', '.',
// and ' ' turned into '_', suffixed ".lock". A Go inject and a chat.sh inject
// into the same pane must land on the SAME lock directory, or the two
// implementations would interleave keystrokes into one mangled turn.
func lockDirName(key string) string {
	sanitized := strings.Map(func(character rune) rune {
		switch character {
		case '/', ':', '.', ' ':
			return '_'
		}
		return character
	}, key)
	return sanitized + ".lock"
}

// acquireTargetLock takes ctx and clk explicitly (never the bare standard-
// library clock, and never a package default) because it is the seam both
// engine.inject (clk = engine.options.Clock) and a test (clk = clock.Real or
// clock.NewFake) already have a Clock in hand to hand down.
func acquireTargetLock(
	ctx context.Context,
	clk clock.Clock,
	root, key string,
	timeout, poll, maxHold time.Duration,
) (*targetLock, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create inject lock root: %w", err)
	}
	path := filepath.Join(root, lockDirName(key))
	pid := os.Getpid()
	deadline := clk.Now().Add(timeout)
	for {
		if err := os.Mkdir(path, 0o700); err == nil {
			lock := &targetLock{path: path, pid: pid, clock: clk}
			if err := lock.beat(); err != nil {
				_ = os.RemoveAll(path)
				return nil, err
			}
			// The settle/re-read closes chat.sh's double-steal race.
			if poll > 0 {
				if err := clk.Sleep(ctx, minDuration(poll, 50*time.Millisecond)); err != nil {
					return nil, err
				}
			}
			ownerPID, _, _ := readLockOwner(path)
			if ownerPID == pid {
				return lock, nil
			}
			continue
		} else if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create inject target lock: %w", err)
		}

		// chat.sh:164-168 steals a held lock only when its owner process is
		// gone or the hold has run past MAXHOLD, and measures the hold in whole
		// seconds with a STRICT >. An unreadable or malformed owner file is
		// never stale: the contender waits out its deadline instead.
		ownerPID, epoch, ok := readLockOwner(path)
		stale := ok && (processDead(ownerPID) ||
			clk.Now().Unix()-epoch > int64(maxHold/time.Second))
		if stale {
			_ = os.RemoveAll(path)
			continue
		}
		if !clk.Now().Before(deadline) {
			return nil, fmt.Errorf("inject target lock timeout")
		}
		if err := clk.Sleep(ctx, poll); err != nil {
			return nil, err
		}
	}
}

func (lock *targetLock) beat() error {
	ownerPID, _, ok := readLockOwner(lock.path)
	if ok && ownerPID != lock.pid {
		return nil
	}
	content := fmt.Sprintf("%d %d\n", lock.pid, lock.clock.Now().Unix())
	return os.WriteFile(filepath.Join(lock.path, "owner"), []byte(content), 0o600)
}

func (lock *targetLock) release() {
	ownerPID, _, ok := readLockOwner(lock.path)
	if ok && ownerPID == lock.pid {
		_ = os.RemoveAll(lock.path)
	}
}

func readLockOwner(path string) (pid int, epoch int64, ok bool) {
	content, err := os.ReadFile(filepath.Join(path, "owner"))
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(content))
	if len(fields) != 2 {
		return 0, 0, false
	}
	pid, err = strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return 0, 0, false
	}
	epoch, err = strconv.ParseInt(fields[1], 10, 64)
	return pid, epoch, err == nil
}

// processDead mirrors chat.sh's `! kill -0 "$opid"` test: any failure of the
// zero signal — the pid is gone (ESRCH) or belongs to another user (EPERM) —
// counts as a dead owner whose lock may be stolen.
func processDead(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err != nil
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

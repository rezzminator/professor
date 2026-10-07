package harvestpy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// provisionLockName is the advisory lock one environment root is converged
// under. It sits INSIDE the root it guards, so the conversion and browser
// roots serialize independently — they never share a directory.
const provisionLockName = ".provision.lock"

// lockProvisionRoot takes an exclusive advisory lock on root, so two pfm
// processes never converge the same environment tree at once: the browser
// provisioner rebuilds a digest directory a live worker may be executing out
// of, and the conversion provisioner quarantines and restores one. The lock
// is flock(2) — the pattern internal/reload/transcript.go already uses for a
// blocking exclusive hold — so a process that dies mid-provision releases it
// with its descriptors and leaves nothing stale behind. It WAITS: the second
// caller waits for the first to finish rather than racing it or skipping the
// work (unlike internal/updatecheck's try-lock, whose caller may simply not
// check for updates this time) — but only as long as ctx lives: it polls a
// non-blocking flock, so a browser fetch's deadline or an MCP cancel ends the
// wait instead of parking a goroutine in flock(2) for a whole provision.
//
// The returned release is safe to call exactly once and reports its own
// failure rather than dropping it.
func lockProvisionRoot(ctx context.Context, root string) (release func() error, returnErr error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create harvestpy environment root %s for locking: %w", root, err)
	}
	path := filepath.Join(root, provisionLockName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open harvestpy provision lock %s: %w", path, err)
	}
	if err := flockUntil(ctx, file); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close harvestpy provision lock %s: %w", path, closeErr))
		}
		return nil, fmt.Errorf("lock harvestpy provision root %s: %w", path, err)
	}
	return func() error {
		var releaseErr error
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("unlock harvestpy provision root %s: %w", path, err))
		}
		if err := file.Close(); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("close harvestpy provision lock %s: %w", path, err))
		}
		return releaseErr
	}, nil
}

// provisionLockPoll is how often a waiting holder retries the lock.
const provisionLockPoll = 50 * time.Millisecond

// flockUntil takes file's exclusive flock, retrying while another holder has
// it, until ctx ends.
func flockUntil(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-clock.Real.After(provisionLockPoll):
		}
	}
}

package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	// shareFormat versions the snapshot file; a reader skips any other.
	shareFormat = 1
	// ShareLockWait bounds how long a watch waits on another process's scan
	// before scanning itself. A holder scanning for longer than this is taken
	// for stuck (a stopped process keeps its lock), and nobody waits on it.
	ShareLockWait = time.Second
	// ShareFutureSkew is how far past this process's clock a snapshot's scan
	// may claim to have begun and still be read: anything later means a clock
	// stepped backward since it was written, and the poll scans for itself.
	ShareFutureSkew = time.Second
	// sharePollEvery is how often a waiting watch looks for the holder's
	// snapshot or a released lock.
	sharePollEvery = 20 * time.Millisecond
	// shareMaxBytes caps a snapshot read; a larger file is not a snapshot.
	shareMaxBytes = 64 << 20
	// shareScratchAge is how old an unpublished scratch file must be before a
	// publisher removes it: it was left by a writer killed mid-write.
	shareScratchAge = time.Minute
)

// ScanShare lets every `pfm chat watch` process on the box share one fleet
// scan per poll. Watches poll on a common grid (multiples of their poll since
// the Unix epoch); the first to poll at a tick scans under the snapshot's lock
// and publishes the result whole (atomicfile), and the others read it instead
// of scanning.
//
// A snapshot serves a poll only when its scan began at or after the poll's
// tick, so the shared view is never older than the one the process would have
// taken itself: a seat that died, was renamed or was respawned before the tick
// reads that way in it. A missing, corrupt, partial, foreign or out-of-time
// snapshot is never read; the poll scans for itself, so sharing can lose its
// saving but never a fact.
type ScanShare struct {
	path string
	lock string
	key  string
	// clock is real time: the waits are on other processes, never on the
	// watch's poll clock.
	clock clock.Clock

	mu    sync.Mutex
	noted map[string]bool
}

// shareHeader is the snapshot's first line; the scan follows it as JSON.
type shareHeader struct {
	Format int `json:"format"`
	// Key identifies everything a scan's rows depend on besides the world:
	// the binary, the user, the paths, the machine config, the caller's
	// directory and socket. A snapshot under another key is someone else's.
	Key string `json:"key"`
	// StartNS is the wall-clock instant the scan began.
	StartNS int64 `json:"start_ns"`
	// Sum is the SHA-256 of the scan bytes that follow the header line.
	Sum string `json:"sum"`
}

// NewScanShare opens the share every watch under the same binary, user, paths,
// config and caller reads and writes. An error means this process cannot
// share (it scans each poll itself), never that the fleet is unreadable.
func NewScanShare(runtime *pfmconfig.Runtime) (*ScanShare, error) {
	env, err := fleet.ResolveEnv(fleet.Request{Runtime: runtime})
	if err != nil {
		return nil, fmt.Errorf("watch snapshot: resolve the scan environment: %w", err)
	}
	key, err := shareKey(env)
	if err != nil {
		return nil, err
	}
	path := paths.WatchSnapshot(env.Paths)
	return &ScanShare{path: path, lock: path + ".lock", key: key, clock: clock.Real, noted: map[string]bool{}}, nil
}

// shareKey hashes every input a scan's rows depend on besides the world
// itself; two processes whose keys match would compose the same rows from the
// same world.
func shareKey(env fleet.Env) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("watch snapshot: locate this binary: %w", err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", fmt.Errorf("watch snapshot: stat this binary %s: %w", executable, err)
	}
	machine, err := json.Marshal(struct {
		Paths  paths.Values
		Config pfmconfig.Config
	}{env.Paths, env.Config})
	if err != nil {
		return "", fmt.Errorf("watch snapshot: encode the scan environment: %w", err)
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "format=%d\nbinary=%s %d %d\nuid=%d\ncwd=%s\nsocket=%s\nprimary=%d\n",
		shareFormat, executable, info.Size(), info.ModTime().UnixNano(), os.Getuid(),
		env.CurrentDir, fleet.CurrentSocket(), env.Primary)
	hash.Write(machine)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Path is the snapshot file the share reads and publishes.
func (share *ScanShare) Path() string { return share.path }

// Scan returns a fleet scan begun at or after since: the shared snapshot when
// one qualifies, else scan's own result, published for the other watches. A
// scan error is returned as it is and never published.
func (share *ScanShare) Scan(
	ctx context.Context,
	since time.Time,
	scan func(context.Context) (FleetScan, error),
) (FleetScan, error) {
	if result, ok := share.read(ctx, since); ok {
		return result, nil
	}
	lock, err := share.openLock()
	if err != nil {
		share.note(ctx, "lock", err)
		return share.scanAndPublish(ctx, scan, nil)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			share.note(ctx, "lock", fmt.Errorf("close %s: %w", share.lock, closeErr))
		}
	}()
	held, result, ok := share.acquire(ctx, lock, since)
	if ok {
		return result, nil
	}
	if !held {
		return share.scanAndPublish(ctx, scan, nil)
	}
	// Another watch may have published between the first read and the lock.
	if result, ok := share.read(ctx, since); ok {
		share.release(ctx, lock)
		return result, nil
	}
	return share.scanAndPublish(ctx, scan, lock)
}

// acquire takes the snapshot lock, or waits for the process holding it to
// publish. It answers held when this process now scans under the lock, ok with
// the holder's snapshot when that arrived first, and neither when the holder
// is stuck or the wait ran out: the poll then scans without the lock.
func (share *ScanShare) acquire(
	ctx context.Context,
	lock *os.File,
	since time.Time,
) (held bool, result FleetScan, ok bool) {
	held, err := tryLock(lock)
	if err != nil {
		share.note(ctx, "lock", err)
		return false, FleetScan{}, false
	}
	if held {
		return true, FleetScan{}, false
	}
	deadline := share.clock.Now().Add(ShareLockWait)
	if started, stamped := holderStart(lock); stamped && started.Add(ShareLockWait).Before(deadline) {
		deadline = started.Add(ShareLockWait)
	}
	last := statFile(share.path)
	for share.clock.Now().Before(deadline) {
		if err := share.clock.Sleep(ctx, sharePollEvery); err != nil {
			return false, FleetScan{}, false
		}
		if current := statFile(share.path); !sameStat(current, last) {
			last = current
			if result, ok := share.read(ctx, since); ok {
				return false, result, true
			}
		}
		held, err := tryLock(lock)
		if err != nil {
			share.note(ctx, "lock", err)
			return false, FleetScan{}, false
		}
		if held {
			return true, FleetScan{}, false
		}
	}
	return false, FleetScan{}, false
}

// scanAndPublish scans and publishes the result; under the lock it stamps the
// lock with the scan's start first, so a waiter can tell a stuck holder, and
// releases it after.
func (share *ScanShare) scanAndPublish(
	ctx context.Context,
	scan func(context.Context) (FleetScan, error),
	lock *os.File,
) (FleetScan, error) {
	start := share.clock.Now()
	if lock != nil {
		if err := stampHolder(lock, start); err != nil {
			share.note(ctx, "lock", err)
		}
		defer share.release(ctx, lock)
	}
	result, err := scan(ctx)
	if err != nil {
		return result, err
	}
	if err := share.publish(start, result); err != nil {
		share.note(ctx, "publish", err)
	}
	return result, nil
}

// read loads the snapshot when it qualifies for a poll at since: the right
// format and key, its checksum intact, its scan begun at or after since and
// not past this clock by more than ShareFutureSkew.
func (share *ScanShare) read(ctx context.Context, since time.Time) (FleetScan, bool) {
	data, err := readSnapshot(share.path, shareMaxBytes)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			share.note(ctx, "read", err)
		}
		return FleetScan{}, false
	}
	result, start, err := decodeSnapshot(data, share.key)
	if err != nil {
		return FleetScan{}, false
	}
	if start.Before(since) || start.After(share.clock.Now().Add(ShareFutureSkew)) {
		return FleetScan{}, false
	}
	return result, true
}

// decodeSnapshot parses a snapshot written under key: its scan and the
// instant that scan began. Any other format or key, a checksum mismatch or a
// torn file is an error.
func decodeSnapshot(data []byte, key string) (FleetScan, time.Time, error) {
	headerLine, payload, found := bytes.Cut(data, []byte("\n"))
	if !found {
		return FleetScan{}, time.Time{}, errors.New("watch snapshot: no header line")
	}
	var header shareHeader
	if err := json.Unmarshal(headerLine, &header); err != nil {
		return FleetScan{}, time.Time{}, fmt.Errorf("watch snapshot: header: %w", err)
	}
	if header.Format != shareFormat || header.Key != key {
		return FleetScan{}, time.Time{}, errors.New("watch snapshot: another format or key")
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != header.Sum {
		return FleetScan{}, time.Time{}, errors.New("watch snapshot: checksum mismatch")
	}
	var result FleetScan
	if err := json.Unmarshal(payload, &result); err != nil {
		return FleetScan{}, time.Time{}, fmt.Errorf("watch snapshot: scan: %w", err)
	}
	return result, time.Unix(0, header.StartNS), nil
}

// encodeSnapshot is decodeSnapshot's inverse.
func encodeSnapshot(result FleetScan, key string, start time.Time) ([]byte, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("watch snapshot: encode scan: %w", err)
	}
	sum := sha256.Sum256(payload)
	header, err := json.Marshal(shareHeader{
		Format: shareFormat, Key: key, StartNS: start.UnixNano(), Sum: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return nil, fmt.Errorf("watch snapshot: encode header: %w", err)
	}
	return append(append(header, '\n'), payload...), nil
}

// publish replaces the snapshot whole, then removes scratch files a writer
// killed mid-write left behind.
func (share *ScanShare) publish(start time.Time, result FleetScan) error {
	content, err := encodeSnapshot(result, share.key, start)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(share.path, content, 0o600); err != nil {
		return err
	}
	return share.sweepScratch()
}

func (share *ScanShare) sweepScratch() error {
	directory := filepath.Dir(share.path)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("watch snapshot: list %s: %w", directory, err)
	}
	prefix := "." + filepath.Base(share.path) + ".tmp-"
	var errs []error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || share.clock.Now().Sub(info.ModTime()) < shareScratchAge {
			continue
		}
		scratch := filepath.Join(directory, entry.Name())
		if err := os.Remove(scratch); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("watch snapshot: remove stale scratch %s: %w", scratch, err))
		}
	}
	return errors.Join(errs...)
}

func (share *ScanShare) openLock() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(share.lock), 0o700); err != nil {
		return nil, fmt.Errorf("watch snapshot: create %s: %w", filepath.Dir(share.lock), err)
	}
	lock, err := os.OpenFile(share.lock, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("watch snapshot: open %s: %w", share.lock, err)
	}
	return lock, nil
}

func (share *ScanShare) release(ctx context.Context, lock *os.File) {
	if err := lock.Truncate(0); err != nil {
		share.note(ctx, "lock", fmt.Errorf("clear %s: %w", share.lock, err))
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		share.note(ctx, "lock", fmt.Errorf("unlock %s: %w", share.lock, err))
	}
}

// note logs a share failure once per kind: the watch goes on scanning for
// itself, so the failure costs footprint, never a fact.
func (share *ScanShare) note(ctx context.Context, kind string, err error) {
	share.mu.Lock()
	defer share.mu.Unlock()
	if share.noted[kind] {
		return
	}
	share.noted[kind] = true
	obs.Logger(ctx).Warn("watch.snapshot."+kind, "path", share.path, "err", err.Error())
}

// tryLock takes the lock without blocking: false when another process holds it.
func tryLock(lock *os.File) (bool, error) {
	err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, fmt.Errorf("watch snapshot: lock %s: %w", lock.Name(), err)
}

// stampHolder records when the holder's scan began, in the lock file itself,
// so a waiter reads it through the descriptor it already holds.
func stampHolder(lock *os.File, start time.Time) error {
	stamp := strconv.FormatInt(start.UnixNano(), 10) + " " + strconv.Itoa(os.Getpid()) + "\n"
	if err := lock.Truncate(0); err != nil {
		return fmt.Errorf("watch snapshot: clear %s: %w", lock.Name(), err)
	}
	if _, err := lock.WriteAt([]byte(stamp), 0); err != nil {
		return fmt.Errorf("watch snapshot: stamp %s: %w", lock.Name(), err)
	}
	return nil
}

// holderStart reads the holder's stamp: when its scan began.
func holderStart(lock *os.File) (time.Time, bool) {
	buffer := make([]byte, 64)
	count, err := lock.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return time.Time{}, false
	}
	field, _, _ := strings.Cut(string(buffer[:count]), " ")
	nanos, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

type fileStat struct {
	found bool
	size  int64
	mtime int64
	inode uint64
}

func statFile(path string) fileStat {
	info, err := os.Stat(path)
	if err != nil {
		return fileStat{}
	}
	stat := fileStat{found: true, size: info.Size(), mtime: info.ModTime().UnixNano()}
	if system, ok := info.Sys().(*syscall.Stat_t); ok {
		stat.inode = system.Ino
	}
	return stat
}

func sameStat(left, right fileStat) bool { return left == right }

// readSnapshot reads the snapshot file, at most limit bytes, only when it is
// a regular file this user owns and no one else can write: a symlink, a FIFO
// (whose open would block the poll) or a file another party could have
// forged is an error, and the poll scans for itself.
func readSnapshot(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: not a regular file (%s)", path, info.Mode().Type())
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("read %s: writable by others (%s)", path, info.Mode().Perm())
	}
	if system, ok := info.Sys().(*syscall.Stat_t); !ok || int(system.Uid) != os.Getuid() {
		return nil, fmt.Errorf("read %s: not owned by this user", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("read %s: larger than %d bytes", path, limit)
	}
	return data, nil
}

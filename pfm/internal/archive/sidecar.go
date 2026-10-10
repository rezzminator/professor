package archive

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

// beforePruneCommit is the filesystem race/failure boundary exercised by archive tests.
var beforePruneCommit func(string) error

// pruneLines uses each producer's protocol: Claude's realpath-keyed directory
// lease protects replacement; Codex's process-local mutex cannot exclude us,
// so its ledger is redacted in place without truncating an external append.
func (runner *Runner) pruneLines(path string, ids []string) (dropped int, unclassified []string, returnErr error) {
	if len(ids) == 0 {
		return 0, nil, nil
	}
	realPath, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("resolve sidecar %s: %w", path, err)
	}
	index := filepath.Base(path) == "session_index.jsonl"
	if !index {
		lease, err := acquireHistoryLease(realPath)
		if err != nil {
			return 0, nil, err
		}
		defer func() {
			if err := lease.release(); err != nil {
				dropped = 0
				returnErr = errors.Join(returnErr, err)
			}
		}()
		return pruneSidecar(path, realPath, ids, index, lease.check)
	}
	return pruneSidecar(path, realPath, ids, index, nil)
}

type sidecarSpan struct {
	offset int64
	line   []byte
}

func pruneSidecar(
	path, realPath string,
	ids []string,
	index bool,
	protected func() error,
) (dropped int, unclassified []string, returnErr error) {
	flags := os.O_RDONLY
	if index {
		flags = os.O_RDWR
	}
	file, err := os.OpenFile(realPath, flags, 0o600)
	if err != nil {
		return 0, nil, fmt.Errorf("open sidecar %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			dropped = 0
			returnErr = errors.Join(returnErr, fmt.Errorf("close sidecar %s: %w", path, err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return 0, nil, fmt.Errorf("stat sidecar %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return 0, nil, fmt.Errorf("sidecar %s is not a regular file", path)
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[strings.ToLower(id)] = struct{}{}
	}
	// Snapshot the original length; an append is not ours to classify or rewrite.
	scanner := bufio.NewScanner(io.LimitReader(file, info.Size()))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	scanner.Split(sidecarLine)
	var content bytes.Buffer
	var spans []sidecarSpan
	var offset int64
	lineNumber := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		lineNumber++
		owner := ""
		var decodeErr error
		// Native Codex readers skip blank rows; previous redactions are not unknown ownership.
		if !index || len(bytes.TrimSpace(line)) != 0 {
			owner, decodeErr = sidecarOwner(path, string(line))
		}
		if decodeErr != nil {
			unclassified = append(unclassified, fmt.Sprintf("%s:%d: %v", path, lineNumber, decodeErr))
		}
		_, archived := wanted[strings.ToLower(owner)]
		if archived && decodeErr == nil {
			spans = append(spans, sidecarSpan{offset: offset, line: bytes.Clone(line)})
		} else if !index {
			if _, err := content.Write(line); err != nil {
				return 0, unclassified, fmt.Errorf("buffer sidecar %s: %w", path, err)
			}
		}
		offset += int64(len(line))
	}
	if err := scanner.Err(); err != nil {
		return 0, unclassified, fmt.Errorf("read sidecar %s: %w", path, err)
	}
	if len(spans) == 0 {
		return 0, unclassified, nil
	}
	if beforePruneCommit != nil {
		if err := beforePruneCommit(path); err != nil {
			return 0, unclassified, fmt.Errorf("prepare sidecar commit %s: %w", path, err)
		}
	}
	if protected != nil {
		if err := protected(); err != nil {
			return 0, unclassified, err
		}
	}
	if err := checkSidecarIdentity(realPath, info); err != nil {
		return 0, unclassified, err
	}
	if !index {
		if err := atomicfile.Write(realPath, content.Bytes(), 0o600); err != nil {
			return 0, unclassified, fmt.Errorf("replace sidecar %s: %w", path, err)
		}
		return len(spans), unclassified, nil
	}
	for _, span := range spans {
		blank := bytes.Repeat([]byte{' '}, len(span.line))
		for position, value := range span.line {
			if value == '\r' || value == '\n' {
				blank[position] = value
			}
		}
		if _, err := file.WriteAt(blank, span.offset); err != nil {
			return 0, unclassified, fmt.Errorf("redact index %s at byte %d: %w", path, span.offset, err)
		}
	}
	if err := file.Sync(); err != nil {
		return 0, unclassified, fmt.Errorf("sync redacted index %s: %w", path, err)
	}
	if err := checkSidecarIdentity(realPath, info); err != nil {
		return 0, unclassified, err
	}
	return len(spans), unclassified, nil
}

// Keep raw line endings and byte offsets, including an incomplete final row.
func sidecarLine(data []byte, atEOF bool) (int, []byte, error) {
	if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
		return newline + 1, data[:newline+1], nil
	}
	if atEOF && len(data) != 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func checkSidecarIdentity(path string, expected fs.FileInfo) error {
	actual, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("verify sidecar %s before retiring cleanup: %w", path, err)
	}
	if !os.SameFile(expected, actual) {
		return fmt.Errorf("sidecar %s was replaced during cleanup; retaining kill for retry", path)
	}
	return nil
}

// sidecarOwner reads only top-level ownership, never UUIDs in prompt or title text.
func sidecarOwner(path, line string) (string, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return "", fmt.Errorf("unclassified record: decode JSON: %w", err)
	}
	fields := []string{"sessionId", "sid"}
	if filepath.Base(path) == "session_index.jsonl" {
		fields = []string{"id"}
	}
	owner := ""
	for _, field := range fields {
		raw, found := record[field]
		if !found {
			continue
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", fmt.Errorf("unclassified record: decode %s: %w", field, err)
		}
		if id == "" || (owner != "" && !strings.EqualFold(owner, id)) {
			return "", fmt.Errorf("unclassified record: empty or conflicting ownership field %s", field)
		}
		owner = id
	}
	if owner == "" {
		return "", fmt.Errorf("unclassified record: missing ownership field")
	}
	return owner, nil
}

// leaseClock times the history lease's heartbeat and freshness; a test drives
// the ten-second stale boundary on a fake one instead of waiting it out.
var leaseClock = clock.Real

// Claude's proper-lockfile writer owns realpath + .lock, with a ten-second
// stale threshold. Keep it fresh and refuse takeover of an existing lease.
// Holding the directory descriptor also prevents inode reuse from disguising replacement.
type historyLease struct {
	path     string
	file     *os.File
	expected fs.FileInfo
	mu       sync.Mutex
	stop     chan struct{}
	done     chan error
}

func acquireHistoryLease(path string) (*historyLease, error) {
	lockPath := path + ".lock"
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		return nil, fmt.Errorf("acquire native history lease %s: %w", lockPath, err)
	}
	file, err := os.Open(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open acquired history lease %s (lease retained): %w", lockPath, err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close unreadable history lease %s: %w", lockPath, closeErr)
		}
		return nil, errors.Join(
			fmt.Errorf("stat acquired history lease %s (lease retained): %w", lockPath, err),
			closeErr,
		)
	}
	lease := &historyLease{
		path:     lockPath,
		file:     file,
		expected: info,
		stop:     make(chan struct{}),
		done:     make(chan error, 1),
	}
	// The heartbeat's period starts at acquisition, not whenever its goroutine
	// is first scheduled.
	go lease.heartbeat(leaseClock.NewTicker(2 * time.Second))
	return lease, nil
}

func (lease *historyLease) checkLocked() error {
	actual, err := os.Lstat(lease.path)
	if err != nil {
		return fmt.Errorf("verify native history lease %s: %w", lease.path, err)
	}
	if !os.SameFile(lease.expected, actual) || !lease.expected.ModTime().Equal(actual.ModTime()) {
		return fmt.Errorf("native history lease %s was compromised; refusing cleanup", lease.path)
	}
	if leaseClock.Now().Sub(actual.ModTime()) >= 8*time.Second {
		return fmt.Errorf("native history lease %s could not stay fresh; refusing cleanup", lease.path)
	}
	return nil
}

func (lease *historyLease) check() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.checkLocked()
}

func (lease *historyLease) refresh() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if err := lease.checkLocked(); err != nil {
		return err
	}
	now := unix.NsecToTimeval(leaseClock.Now().UnixNano())
	if err := unix.Futimes(int(lease.file.Fd()), []unix.Timeval{now, now}); err != nil {
		return fmt.Errorf("refresh native history lease %s: %w", lease.path, err)
	}
	info, err := lease.file.Stat()
	if err != nil {
		return fmt.Errorf("stat refreshed history lease %s: %w", lease.path, err)
	}
	lease.expected = info
	return lease.checkLocked()
}

func (lease *historyLease) heartbeat(ticker clock.Ticker) {
	defer ticker.Stop()
	for {
		select {
		case <-lease.stop:
			lease.done <- nil
			return
		case <-ticker.C():
			if err := lease.refresh(); err != nil {
				lease.done <- err
				return
			}
		}
	}
}

func (lease *historyLease) release() error {
	close(lease.stop)
	result := <-lease.done
	if err := lease.check(); err != nil {
		result = errors.Join(result, err)
	} else if err := os.Remove(lease.path); err != nil {
		result = errors.Join(result, fmt.Errorf("release native history lease %s: %w", lease.path, err))
	}
	if err := lease.file.Close(); err != nil {
		result = errors.Join(result, fmt.Errorf("close native history lease %s: %w", lease.path, err))
	}
	return result
}

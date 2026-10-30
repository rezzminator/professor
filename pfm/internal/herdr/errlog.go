package herdr

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// A reporter run from a tmux hook (`run-shell -b`) has its stderr discarded
// by tmux, so every reporter error is also appended to a per-session log in
// the pfm state directory, where `pfm doctor` reads it. One line per error:
// the RFC 3339 time, a tab, the message with newlines folded to spaces.
// Expected lifecycle outcomes carry an INFO column and never count as errors.

// ErrorLogSuffix ends the per-session log file name: {session}.log.
const ErrorLogSuffix = ".log"

// Messages are cut on a rune boundary, below the reader's 64 KiB line limit.
// A log that reached ErrorLogBytes rotates to .log.1 before the next append.
// Before replacing .log.1, .log.lost atomically records the newest discarded
// error time. This fixed-size watermark survives further INFO-only rotations.
// Readers and writers hold the directory flock for a coherent snapshot.
const (
	ErrorMessageBytes = 4096
	ErrorLogBytes     = 256 * 1024
)

// ErrorRecord is one logged reporter error, with the retained file holding it.
type ErrorRecord struct {
	Session string
	At      time.Time
	Message string
	LogPath string
}

// ErrorLogIncompleteError means bounded retention discarded errors within the
// requested window. RecentErrors still returns every retained matching error.
type ErrorLogIncompleteError struct {
	Since   time.Time
	Through time.Time
}

func (gap *ErrorLogIncompleteError) Error() string {
	return fmt.Sprintf("herdr error log: history incomplete since %s: bounded retention discarded errors through %s",
		gap.Since.UTC().Format(time.RFC3339), gap.Through.UTC().Format(time.RFC3339))
}

// AppendError appends one error line to dir/{session}.log, creating dir.
func AppendError(dir, session string, at time.Time, message string) error {
	return appendReporterLog(dir, session, at, "", message)
}

// AppendInfo records an expected lifecycle outcome, not a reporter fault.
// The explicit level column leaves legacy, unlevelled errors readable.
func AppendInfo(dir, session string, at time.Time, message string) error {
	return appendReporterLog(dir, session, at, "INFO\t", message)
}

func lockReporterLogs(dir string, mode int) (*os.File, error) {
	lock, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("herdr error log: open %s to lock it: %w", dir, err)
	}
	for {
		err := syscall.Flock(int(lock.Fd()), mode)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EINTR) {
			return nil, errors.Join(fmt.Errorf("herdr error log: lock %s: %w", dir, err), lock.Close())
		}
	}
}

func appendReporterLog(dir, session string, at time.Time, level, message string) (returnErr error) {
	if dir == "" || session == "" || strings.ContainsAny(session, "/\x00") {
		return fmt.Errorf("herdr error log: dir %q session %q is not a plain log location", dir, session)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("herdr error log: create %s: %w", dir, err)
	}
	lock, err := lockReporterLogs(dir, syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("herdr error log: unlock %s: %w", dir, closeErr))
		}
	}()
	name := session + ErrorLogSuffix
	path := filepath.Join(dir, name)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("herdr error log: %s is not a regular file", path)
		}
		if info.Size() >= ErrorLogBytes {
			if err := recordDiscardedErrors(dir, name); err != nil {
				return err
			}
			if err := os.Rename(path, path+".1"); err != nil {
				return fmt.Errorf("herdr error log: rotate %s: %w", path, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("herdr error log: inspect %s: %w", path, err)
	}
	file, err := OpenStateFile(dir, name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, "herdr error log")
	if err != nil {
		return fmt.Errorf("herdr error log: open %s: %w", path, err)
	}
	folded := strings.Join(strings.Fields(message), " ")
	if len(folded) > ErrorMessageBytes {
		cut := ErrorMessageBytes
		for cut > 0 && !utf8.RuneStart(folded[cut]) {
			cut--
		}
		folded = folded[:cut]
	}
	line := at.UTC().Format(time.RFC3339) + "\t" + level + folded + "\n"
	if _, err := file.WriteString(line); err != nil {
		return errors.Join(fmt.Errorf("herdr error log: write %s: %w", path, err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("herdr error log: close %s: %w", path, err)
	}
	return nil
}

// recordDiscardedErrors runs under the writer lock, before any history is
// replaced. Failure preserves that history. INFO-only loss needs no warning.
func recordDiscardedErrors(dir, name string) error {
	newest, err := readLostErrors(dir, name+".lost")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	records, err := readReporterLog(dir, name+".1", "", time.Time{})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, record := range records {
		if record.At.After(newest) {
			newest = record.At
		}
	}
	if newest.IsZero() {
		return nil
	}
	path := filepath.Join(dir, name+".lost")
	line := newest.UTC().Format(time.RFC3339) + "\tbounded retention discarded errors\n"
	if err := atomicfile.Write(path, []byte(line), 0o600); err != nil {
		return fmt.Errorf("herdr error log: record discarded history %s: %w", path, err)
	}
	return nil
}

// RecentErrors returns every retained error at or after since, newest first,
// excluding explicit INFO records. Legacy unlevelled lines remain errors.
// A missing dir is no errors; failed reads are errors, never empty success.
// An ErrorLogIncompleteError accompanies retained records when .log.lost
// proves the requested window lost errors. Loss predating these watermarks
// (written by older versions) cannot be reconstructed.
func RecentErrors(dir string, since time.Time) (records []ErrorRecord, returnErr error) {
	lock, err := lockReporterLogs(dir, syscall.LOCK_SH)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("herdr error log: unlock %s: %w", dir, closeErr))
		}
	}()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("herdr error log: read %s: %w", dir, err)
	}
	var lostThrough time.Time
	for _, entry := range entries {
		name := entry.Name()
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".1"), ".lost")
		if !strings.HasSuffix(base, ErrorLogSuffix) {
			continue
		}
		if strings.HasSuffix(name, ".lost") {
			through, err := readLostErrors(dir, name)
			if err != nil {
				return nil, err
			}
			if !through.Before(since) && through.After(lostThrough) {
				lostThrough = through
			}
			continue
		}
		session := strings.TrimSuffix(base, ErrorLogSuffix)
		found, err := readReporterLog(dir, name, session, since)
		if err != nil {
			return nil, err
		}
		records = append(records, found...)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].At.After(records[j].At) })
	if !lostThrough.IsZero() {
		return records, &ErrorLogIncompleteError{Since: since, Through: lostThrough}
	}
	return records, nil
}

func readLostErrors(dir, name string) (time.Time, error) {
	records, err := readReporterLog(dir, name, "", time.Time{})
	if err != nil {
		return time.Time{}, err
	}
	if len(records) != 1 || records[0].Message != "bounded retention discarded errors" {
		return time.Time{}, fmt.Errorf(
			"herdr error log: %s holds a malformed retention watermark",
			filepath.Join(dir, name),
		)
	}
	return records[0].At, nil
}

func readReporterLog(dir, name, session string, since time.Time) ([]ErrorRecord, error) {
	path := filepath.Join(dir, name)
	file, err := OpenStateFile(dir, name, os.O_RDONLY, "herdr error log")
	if err != nil {
		return nil, fmt.Errorf("herdr error log: open %s: %w", path, err)
	}
	var records []ErrorRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		stamp, message, found := strings.Cut(scanner.Text(), "\t")
		at, parseErr := time.Parse(time.RFC3339, stamp)
		if !found || parseErr != nil {
			return nil, errors.Join(
				fmt.Errorf("herdr error log: %s holds a malformed line %q", path, scanner.Text()), file.Close())
		}
		if !strings.HasPrefix(message, "INFO\t") && !at.Before(since) {
			records = append(records, ErrorRecord{Session: session, At: at, Message: message, LogPath: path})
		}
	}
	if err := errors.Join(scanner.Err(), file.Close()); err != nil {
		return nil, fmt.Errorf("herdr error log: read %s: %w", path, err)
	}
	return records, nil
}

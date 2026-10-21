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
)

// A reporter run from a tmux hook (`run-shell -b`) has its stderr discarded
// by tmux, so every reporter error is also appended to a per-session log in
// the pfm state directory, where `pfm doctor` reads it. One line per error:
// the RFC 3339 time, a tab, the message with newlines folded to spaces.
// Expected lifecycle outcomes carry an INFO column and never count as errors.

// ErrorLogSuffix ends the per-session log file name: {session}.log.
const ErrorLogSuffix = ".log"

// The log stays bounded and readable: a message is cut to ErrorMessageBytes
// (on a rune boundary), well under the reader's 64 KiB line limit, and a log
// that reached ErrorLogBytes is renamed to {session}.log.1, replacing the
// previous one, before the next line is written; a torn line goes with it.
// Sessions without a Herdr pane share one log, so the size check, the
// rotation and the append run under an exclusive flock on the log directory:
// two writers never both rotate, and no line lands in a log being renamed.
const (
	ErrorMessageBytes = 4096
	ErrorLogBytes     = 256 * 1024
)

// ErrorRecord is one logged reporter error.
type ErrorRecord struct {
	Session string
	At      time.Time
	Message string
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

func appendReporterLog(dir, session string, at time.Time, level, message string) (returnErr error) {
	if dir == "" || session == "" || strings.ContainsAny(session, "/\x00") {
		return fmt.Errorf("herdr error log: dir %q session %q is not a plain log location", dir, session)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("herdr error log: create %s: %w", dir, err)
	}
	lock, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("herdr error log: open %s to lock it: %w", dir, err)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("herdr error log: unlock %s: %w", dir, closeErr))
		}
	}()
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EINTR) {
			return fmt.Errorf("herdr error log: lock %s: %w", dir, err)
		}
	}
	path := filepath.Join(dir, session+ErrorLogSuffix)
	if info, err := os.Stat(path); err == nil && info.Size() >= ErrorLogBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			return fmt.Errorf("herdr error log: rotate %s: %w", path, err)
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
		_ = file.Close()
		return fmt.Errorf("herdr error log: write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("herdr error log: close %s: %w", path, err)
	}
	return nil
}

// RecentErrors returns every logged error in dir at or after since, newest
// first, excluding explicit INFO records. Legacy unlevelled lines are errors.
// A missing dir is no errors; an unreadable log or a malformed line is
// an error, never an empty list.
func RecentErrors(dir string, since time.Time) ([]ErrorRecord, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("herdr error log: read %s: %w", dir, err)
	}
	var records []ErrorRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ErrorLogSuffix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("herdr error log: open %s: %w", path, err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			stamp, message, found := strings.Cut(scanner.Text(), "\t")
			at, parseErr := time.Parse(time.RFC3339, stamp)
			if !found || parseErr != nil {
				_ = file.Close()
				return nil, fmt.Errorf("herdr error log: %s holds a malformed line %q", path, scanner.Text())
			}
			if strings.HasPrefix(message, "INFO\t") {
				continue
			}
			if !at.Before(since) {
				records = append(
					records,
					ErrorRecord{Session: strings.TrimSuffix(entry.Name(), ErrorLogSuffix), At: at, Message: message},
				)
			}
		}
		scanErr := scanner.Err()
		if closeErr := file.Close(); scanErr == nil && closeErr != nil {
			scanErr = closeErr
		}
		if scanErr != nil {
			return nil, fmt.Errorf("herdr error log: read %s: %w", path, scanErr)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].At.After(records[j].At) })
	return records, nil
}

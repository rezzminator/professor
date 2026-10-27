package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestErrorLogRefusesFIFOsWithoutBlocking(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(map[bool]string{false: "append", true: "read"}[read], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "fifo.log")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if read {
					_, err := RecentErrors(dir, time.Time{})
					done <- err
				} else {
					done <- AppendError(dir, "fifo", time.Now(), "message")
				}
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "herdr error log") {
					t.Fatalf("FIFO must be a contextual error, got %v", err)
				}
			case <-time.After(time.Second):
				// Release an unfixed blocking open so the red regression leaves no
				// worker behind. Opening both ends never blocks, even without peers.
				rescue, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				if read {
					if _, err := rescue.WriteString("malformed\n"); err != nil {
						t.Error(err)
					}
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("blocked FIFO worker did not release")
				}
				if err := rescue.Close(); err != nil {
					t.Error(err)
				}
				t.Fatal("reporter log blocked on a FIFO instead of refusing it")
			}
		})
	}
}

func TestErrorLogRoundTripsRecentErrorsNewestFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "herdr")
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, line := range []struct {
		session string
		at      time.Time
		message string
	}{
		{"cc-1", now.Add(-48 * time.Hour), "old"},
		{"cc-1", now.Add(-time.Hour), "name pane w1:p1 \"x\":\nrefused code 7"},
		{"cc-2", now.Add(-time.Minute), "lock timed out"},
	} {
		if err := AppendError(dir, line.session, line.at, line.message); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RecentErrors(dir, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Session != "cc-2" || got[0].Message != "lock timed out" ||
		got[1].Session != "cc-1" || got[1].Message != "name pane w1:p1 \"x\": refused code 7" {
		t.Fatalf("RecentErrors = %+v, want cc-2 then cc-1 with newlines folded", got)
	}
}

func TestErrorLogNamesWhatIsBroken(t *testing.T) {
	dir := t.TempDir()
	if got, err := RecentErrors(filepath.Join(dir, "absent"), time.Time{}); err != nil || got != nil {
		t.Fatalf("RecentErrors(missing dir) = %v, %v, want none", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cc-1.log"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecentErrors(dir, time.Time{}); err == nil {
		t.Fatal("RecentErrors over a malformed line returned no error")
	}
	// A session name that is a path would write outside the log directory.
	logs := filepath.Join(dir, "logs")
	for _, session := range []string{"a/b", "../escape", "", "nul\x00byte"} {
		if err := AppendError(logs, session, time.Now(), "x"); err == nil {
			t.Fatalf("AppendError accepted the session name %q", session)
		}
	}
	if err := AppendError("", "cc-1", time.Now(), "x"); err == nil {
		t.Fatal("AppendError accepted an empty directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.log")); !os.IsNotExist(err) {
		t.Fatalf("a refused session name still wrote outside the log directory: %v", err)
	}
}

// The log stays readable and bounded: a message is cut below the reader's
// line limit, and a log past its cap is rotated aside before the next line.
func TestErrorLogStaysBounded(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	if err := AppendError(dir, "cc-1", at, strings.Repeat("é", 70*1024)); err != nil {
		t.Fatal(err)
	}
	records, err := RecentErrors(dir, at)
	if err != nil || len(records) != 1 || len(records[0].Message) > ErrorMessageBytes {
		t.Fatalf("records=%d err=%v, want one message cut to %d bytes", len(records), err, ErrorMessageBytes)
	}
	path := filepath.Join(dir, "cc-1"+ErrorLogSuffix)
	if err := os.WriteFile(path, []byte("torn line\n"+strings.Repeat("x", ErrorLogBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AppendError(dir, "cc-1", at, "fresh"); err != nil {
		t.Fatal(err)
	}
	records, err = RecentErrors(dir, at)
	if err != nil || len(records) != 1 || records[0].Message != "fresh" {
		t.Fatalf("records=%+v err=%v, want only the fresh line once the full log was rotated", records, err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log: %v, want the old log kept beside it", err)
	}
}

// Sessions without a Herdr pane share one log, so writers race: every line
// lands, a full log is rotated aside exactly once, and no rotation replaces
// the log another writer just rotated.
func TestErrorLogRotatesOnceUnderConcurrentWriters(t *testing.T) {
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	for round := 0; round < 40; round++ {
		dir := t.TempDir()
		path := filepath.Join(dir, "shared"+ErrorLogSuffix)
		full := at.Format(time.RFC3339) + "\tkept\n" + strings.Repeat("x", ErrorLogBytes) + "\n"
		if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
			t.Fatal(err)
		}
		const writers = 16
		start := make(chan struct{})
		errs := make(chan error, writers)
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- AppendError(dir, "shared", at, "line")
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: AppendError under concurrent writers: %v", round, err)
			}
		}
		rotated, err := os.ReadFile(path + ".1")
		if err != nil || !strings.HasPrefix(string(rotated), at.Format(time.RFC3339)+"\tkept\n") {
			t.Fatalf("round %d: rotated log %d bytes, err %v, want the full log kept once", round, len(rotated), err)
		}
		current, err := os.ReadFile(path)
		if err != nil || strings.Count(string(current), "\tline\n") != writers {
			t.Fatalf(
				"round %d: current log holds %d of %d lines, err %v",
				round,
				strings.Count(string(current), "\tline\n"),
				writers,
				err,
			)
		}
	}
}

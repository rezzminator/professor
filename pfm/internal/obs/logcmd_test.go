package obs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// logNow is the fixed instant every fixture record and fake clock starts at.
var logNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// lockedBuffer is a buffer the follow goroutine writes while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fixtureLog writes a small activity file and returns its path.
func fixtureLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pfm.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// logLine renders one record the way the handler would have written it.
func logLine(stamp time.Time, level, message, command, chat string) string {
	return fmt.Sprintf(
		`{"ts":%q,"level":%q,"msg":%q,"cmd":%q,"pid":1,"version":"1.2.3","chat":%q}`,
		stamp.UTC().Format(time.RFC3339Nano), level, message, command, chat,
	)
}

func TestReadActivityFiltersBySinceLevelChatAndCmd(t *testing.T) {
	now := logNow
	path := fixtureLog(t,
		logLine(now.Add(-time.Hour), "INFO", "old.record", "reload", "cc-1"),
		logLine(now.Add(-time.Minute), "INFO", "recent.info", "reload", "cc-1"),
		logLine(now.Add(-time.Minute), "WARN", "recent.warn", "reap", "cc-2"),
		logLine(now.Add(-time.Minute), "ERROR", "recent.error", "reload", "cc-2"),
	)
	for _, testCase := range []struct {
		name    string
		args    []string
		want    []string
		refused []string
	}{
		{name: "no filter", args: nil, want: []string{"old.record", "recent.info", "recent.warn", "recent.error"}},
		{
			name: "since", args: []string{"--since", "10m"},
			want: []string{"recent.info", "recent.warn"}, refused: []string{"old.record"},
		},
		{
			name: "level", args: []string{"--level", "warn"},
			want: []string{"recent.warn", "recent.error"}, refused: []string{"recent.info"},
		},
		{
			name: "chat", args: []string{"--chat", "cc-2"},
			want: []string{"recent.warn", "recent.error"}, refused: []string{"recent.info"},
		},
		{
			name: "cmd", args: []string{"--cmd", "reload"},
			want: []string{"recent.info", "recent.error"}, refused: []string{"recent.warn"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := ReadActivity(
				context.Background(),
				testCase.args,
				&stdout,
				&stderr,
				path,
				clock.NewFake(logNow),
			); code != 0 {
				t.Fatalf("ReadActivity = %d, want 0; stderr = %q", code, stderr.String())
			}
			for _, want := range testCase.want {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, stdout.String())
				}
			}
			for _, refused := range testCase.refused {
				if strings.Contains(stdout.String(), refused) {
					t.Fatalf("output kept %q it should have filtered:\n%s", refused, stdout.String())
				}
			}
		})
	}
}

// TestRunLogFollowReadsRecordsAppendedAfterItStarted is the follow-once proof.
func TestReadActivityFollowReadsRecordsAppendedAfterItStarted(t *testing.T) {
	now := logNow
	path := fixtureLog(t, logLine(now, "INFO", "before.follow", "reload", "cc-1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout lockedBuffer
	var stderr bytes.Buffer
	fake := clock.NewFake(logNow)
	done := make(chan int, 1)
	go func() { done <- ReadActivity(ctx, []string{"--follow"}, &stdout, &stderr, path, fake) }()
	testwait.Until(t, "follow parked on its poll timer", func() (bool, string) {
		return fake.Pending() > 0, fmt.Sprintf("pending=%d", fake.Pending())
	})

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// The record lands in two writes: the reader holds the torn first half
	// until the rest arrives and never prints half of it.
	record := logLine(now, "INFO", "after.follow", "reload", "cc-1") + "\n"
	half := len(record) / 2
	if _, err := file.WriteString(record[:half]); err != nil {
		t.Fatal(err)
	}
	fake.Advance(followPoll)
	testwait.Until(t, "follow parked again on its poll timer after reading the torn half", func() (bool, string) {
		return fake.Pending() > 0, fmt.Sprintf("pending=%d", fake.Pending())
	})
	if got, want := stdout.String(), logLine(now, "INFO", "before.follow", "reload", "cc-1")+"\n"; got != want {
		t.Fatalf("stdout after the torn half = %q, want only the first record %q", got, want)
	}
	if _, err := file.WriteString(record[half:]); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	testwait.Until(t, "--follow printed the appended record", func() (bool, string) {
		fake.Advance(followPoll)
		out := stdout.String()
		return strings.Contains(out, "after.follow"), out
	})
	cancel()
	code, _ := testwait.Receive(t, "ReadActivity --follow returned after its context was cancelled", done)
	if code != 0 {
		t.Fatalf("ReadActivity --follow = %d, want 0; stderr = %q", code, stderr.String())
	}
}

func TestReadActivitySeparatesAnAbsentLogFromAnUnreadableOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "pfm.jsonl")
	if code := ReadActivity(context.Background(), nil, &stdout, &stderr, missing, clock.NewFake(logNow)); code != 0 {
		t.Fatalf("an absent log exited %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "no activity log at") {
		t.Fatalf("stderr = %q, want the absence named", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := ReadActivity(
		context.Background(),
		nil,
		&stdout,
		&stderr,
		t.TempDir(),
		clock.NewFake(logNow),
	); code != 1 {
		t.Fatalf("an unreadable log exited %d, want 1 — absence is not the same as a failed read", code)
	}
}

func TestReadActivityPrintsAnUndecodableRecordAndCountsIt(t *testing.T) {
	path := fixtureLog(t, "{not json}", logLine(logNow, "INFO", "good.record", "ls", ""))
	var stdout, stderr bytes.Buffer
	if code := ReadActivity(context.Background(), nil, &stdout, &stderr, path, clock.NewFake(logNow)); code != 0 {
		t.Fatalf("ReadActivity = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "{not json}") {
		t.Fatalf("a damaged record disappeared:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "could not be decoded") {
		t.Fatalf("stderr = %q, want the damaged record counted", stderr.String())
	}
}

func TestReadActivityRefusesAnUnknownLevelAndExtraArguments(t *testing.T) {
	path := fixtureLog(t, logLine(logNow, "INFO", "record", "ls", ""))
	var stdout, stderr bytes.Buffer
	if code := ReadActivity(
		context.Background(),
		[]string{"--level", "chatty"},
		&stdout,
		&stderr,
		path,
		clock.NewFake(logNow),
	); code != 2 {
		t.Fatalf("an unknown --level exited %d, want 2", code)
	}
	stderr.Reset()
	if code := ReadActivity(
		context.Background(),
		[]string{"extra"},
		&stdout,
		&stderr,
		path,
		clock.NewFake(logNow),
	); code != 2 {
		t.Fatalf("a positional argument exited %d, want 2", code)
	}
}

// compLine renders one component-scoped record.
func compLine(stamp time.Time, message, comp string) string {
	return fmt.Sprintf(
		`{"ts":%q,"level":"INFO","msg":%q,"cmd":"chat","pid":1,"version":"1.2.3","comp":%q}`,
		stamp.UTC().Format(time.RFC3339Nano), message, comp,
	)
}

// TestReadActivityFiltersByComp: `pfm log --comp mcp` keeps one component's
// records; an unregistered name is the registry's error, exit 2.
func TestReadActivityFiltersByComp(t *testing.T) {
	now := logNow
	path := fixtureLog(t,
		compLine(now, "mcp.record", "mcp"),
		compLine(now, "db.record", "db"),
		logLine(now, "INFO", "unscoped.record", "chat", ""),
	)
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if code := ReadActivity(ctx, []string{"--comp", "mcp"}, &stdout, &stderr, path, clock.NewFake(logNow)); code != 0 {
		t.Fatalf("ReadActivity --comp mcp = %d; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "mcp.record") {
		t.Fatalf("output missing the mcp record:\n%s", stdout.String())
	}
	for _, refused := range []string{"db.record", "unscoped.record"} {
		if strings.Contains(stdout.String(), refused) {
			t.Fatalf("output kept %q past --comp mcp:\n%s", refused, stdout.String())
		}
	}
	stdout.Reset()
	if code := ReadActivity(
		ctx,
		[]string{"--comp", "database"},
		&stdout,
		&stderr,
		path,
		clock.NewFake(logNow),
	); code != 2 {
		t.Fatalf("an unknown --comp exited %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `"database"`) ||
		!strings.Contains(stderr.String(), strings.Join(Components, ", ")) {
		t.Fatalf("stderr = %q, want the unknown component and the registry named", stderr.String())
	}
	if !strings.Contains(LogUsage, "--comp mcp") {
		t.Fatalf("LogUsage = %q, want --comp in it", LogUsage)
	}
}

// TestReadActivityLevelUsesTheOneParser: `warning` and `off` are read by the
// same parser as every other level surface.
func TestReadActivityLevelUsesTheOneParser(t *testing.T) {
	now := logNow
	path := fixtureLog(t,
		logLine(now, "INFO", "info.record", "chat", ""),
		logLine(now, "WARN", "warn.record", "chat", ""),
	)
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if code := ReadActivity(
		ctx,
		[]string{"--level", "warning"},
		&stdout,
		&stderr,
		path,
		clock.NewFake(logNow),
	); code != 0 {
		t.Fatalf("--level warning = %d; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "info.record") || !strings.Contains(stdout.String(), "warn.record") {
		t.Fatalf("--level warning printed:\n%s", stdout.String())
	}
	stdout.Reset()
	if code := ReadActivity(
		ctx,
		[]string{"--level", "chatty"},
		&stdout,
		&stderr,
		path,
		clock.NewFake(logNow),
	); code != 2 {
		t.Fatalf("--level chatty = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), strings.Join(LevelNames, ", ")) {
		t.Fatalf("stderr = %q, want every accepted level named", stderr.String())
	}
}

// TestReadActivityReadsTheKeptArchiveTheSegmentsAndTheCurrentInTimeOrder:
// `pfm log` reads the whole log — kept warnings and errors by day, the closed
// segments, the current segment — oldest first; --since leaves out a file
// whose last write is older than it, and reaches into the archive for one
// that is not.
func TestReadActivityReadsTheKeptArchiveTheSegmentsAndTheCurrentInTimeOrder(t *testing.T) {
	now := logNow
	path := fixtureLog(t, logLine(now, "INFO", "current", "ls", ""))
	files := []struct {
		name, line string
		age        time.Duration
	}{
		{"pfm.kept/2026-11-10.jsonl", logLine(now.Add(-50*time.Hour), "WARN", "kept.old", "ls", ""), 50 * time.Hour},
		{
			"pfm.kept/2026-11-11.jsonl",
			logLine(now.Add(-30*time.Minute), "ERROR", "kept.new", "ls", ""),
			30 * time.Minute,
		},
		{
			"pfm.segments/2026-11-12T09-00-00.000Z.jsonl",
			logLine(now.Add(-10*time.Minute), "INFO", "segment", "ls", ""),
			10 * time.Minute,
		},
	}
	for _, file := range files {
		full := filepath.Join(filepath.Dir(path), file.name)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(file.line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, now.Add(-file.age), now.Add(-file.age)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, files[0].line + "\n" + files[1].line + "\n" + files[2].line + "\n" + logLine(now, "INFO", "current", "ls", "") + "\n"},
		{[]string{"--since", "1h", "--level", "warn"}, files[1].line + "\n"},
	} {
		var stdout, stderr bytes.Buffer
		if code := ReadActivity(
			context.Background(),
			tc.args,
			&stdout,
			&stderr,
			path,
			clock.NewFake(logNow),
		); code != 0 {
			t.Fatalf("%v: ReadActivity = %d; stderr %q", tc.args, code, stderr.String())
		}
		if stdout.String() != tc.want {
			t.Fatalf("%v: printed\n%s\nwant\n%s", tc.args, stdout.String(), tc.want)
		}
	}
}

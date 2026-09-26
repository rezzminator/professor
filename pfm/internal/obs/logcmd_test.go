package obs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

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
	now := time.Now()
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
				clock.Real,
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
	now := time.Now()
	path := fixtureLog(t, logLine(now, "INFO", "before.follow", "reload", "cc-1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- ReadActivity(ctx, []string{"--follow"}, &stdout, &stderr, path, clock.Real) }()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(logLine(now, "INFO", "after.follow", "reload", "cc-1") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for !strings.Contains(stdout.String(), "after.follow") {
		select {
		case <-deadline:
			t.Fatalf("--follow never printed the appended record:\n%s", stdout.String())
		case <-time.After(followPoll):
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("ReadActivity --follow = %d, want 0; stderr = %q", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReadActivity --follow did not return after its context was cancelled")
	}
}

func TestReadActivitySeparatesAnAbsentLogFromAnUnreadableOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "pfm.jsonl")
	if code := ReadActivity(context.Background(), nil, &stdout, &stderr, missing, clock.Real); code != 0 {
		t.Fatalf("an absent log exited %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "no activity log at") {
		t.Fatalf("stderr = %q, want the absence named", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := ReadActivity(context.Background(), nil, &stdout, &stderr, t.TempDir(), clock.Real); code != 1 {
		t.Fatalf("an unreadable log exited %d, want 1 — absence is not the same as a failed read", code)
	}
}

func TestReadActivityPrintsAnUndecodableRecordAndCountsIt(t *testing.T) {
	path := fixtureLog(t, "{not json}", logLine(time.Now(), "INFO", "good.record", "ls", ""))
	var stdout, stderr bytes.Buffer
	if code := ReadActivity(context.Background(), nil, &stdout, &stderr, path, clock.Real); code != 0 {
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
	path := fixtureLog(t, logLine(time.Now(), "INFO", "record", "ls", ""))
	var stdout, stderr bytes.Buffer
	if code := ReadActivity(
		context.Background(),
		[]string{"--level", "chatty"},
		&stdout,
		&stderr,
		path,
		clock.Real,
	); code != 2 {
		t.Fatalf("an unknown --level exited %d, want 2", code)
	}
	stderr.Reset()
	if code := ReadActivity(context.Background(), []string{"extra"}, &stdout, &stderr, path, clock.Real); code != 2 {
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
	now := time.Now()
	path := fixtureLog(t,
		compLine(now, "mcp.record", "mcp"),
		compLine(now, "db.record", "db"),
		logLine(now, "INFO", "unscoped.record", "chat", ""),
	)
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if code := ReadActivity(ctx, []string{"--comp", "mcp"}, &stdout, &stderr, path, clock.Real); code != 0 {
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
	if code := ReadActivity(ctx, []string{"--comp", "database"}, &stdout, &stderr, path, clock.Real); code != 2 {
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
	now := time.Now()
	path := fixtureLog(t,
		logLine(now, "INFO", "info.record", "chat", ""),
		logLine(now, "WARN", "warn.record", "chat", ""),
	)
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if code := ReadActivity(ctx, []string{"--level", "warning"}, &stdout, &stderr, path, clock.Real); code != 0 {
		t.Fatalf("--level warning = %d; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "info.record") || !strings.Contains(stdout.String(), "warn.record") {
		t.Fatalf("--level warning printed:\n%s", stdout.String())
	}
	stdout.Reset()
	if code := ReadActivity(ctx, []string{"--level", "chatty"}, &stdout, &stderr, path, clock.Real); code != 2 {
		t.Fatalf("--level chatty = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), strings.Join(LevelNames, ", ")) {
		t.Fatalf("stderr = %q, want every accepted level named", stderr.String())
	}
}

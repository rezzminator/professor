package obs

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestParseLevelAcceptsEveryNameAndTheWarningAlias pins the one level parser:
// six accepted spellings, `warning` an alias of `warn`, `off` above every
// record, case and surrounding space ignored.
func TestParseLevelAcceptsEveryNameAndTheWarningAlias(t *testing.T) {
	for _, testCase := range []struct {
		text string
		want Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{" Error ", slog.LevelError},
		{"off", LevelOff},
	} {
		got, err := ParseLevel(testCase.text)
		if err != nil {
			t.Fatalf("ParseLevel(%q) error = %v", testCase.text, err)
		}
		if got != testCase.want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", testCase.text, got, testCase.want)
		}
	}
	if LevelOff <= slog.LevelError {
		t.Fatalf("LevelOff = %v must sit above ERROR so an ERROR record never passes it", LevelOff)
	}
	if strings.Join(LevelNames, ",") != "debug,info,warn,warning,error,off" {
		t.Fatalf("LevelNames = %v, want the six spec spellings in order", LevelNames)
	}
}

// TestParseLevelNamesTheAcceptedValues: an invalid value is refused with every
// accepted spelling in the message, never silently mapped.
func TestParseLevelNamesTheAcceptedValues(t *testing.T) {
	for _, text := range []string{"", "chatty", "warn ing", "trace"} {
		_, err := ParseLevel(text)
		if err == nil {
			t.Fatalf("ParseLevel(%q) accepted", text)
		}
		for _, name := range LevelNames {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("ParseLevel(%q) error %q does not name accepted level %q", text, err, name)
			}
		}
	}
}

// TestLevelNameRoundTripsEveryLevel: the canonical spelling comes back for
// every parsed level, so config stores `warn` for a file that said `warning`.
func TestLevelNameRoundTripsEveryLevel(t *testing.T) {
	for _, name := range LevelNames {
		level, err := ParseLevel(name)
		if err != nil {
			t.Fatal(err)
		}
		want := name
		if name == "warning" {
			want = "warn"
		}
		if got := LevelName(level); got != want {
			t.Fatalf("LevelName(ParseLevel(%q)) = %q, want %q", name, got, want)
		}
	}
	if got := LevelName(slog.Level(3)); got != "info" {
		t.Fatalf("LevelName(3) = %q, want the floor level info — a level between two names reads as the lower", got)
	}
}

// TestRoutineLevelPicksTheLevelByOutcome pins the rule every per-call door
// shares (db.statement, db.open, tmux.exec): the failure level on err, INFO
// when retried or slow, DEBUG for a routine success.
func TestRoutineLevelPicksTheLevelByOutcome(t *testing.T) {
	failed := errors.New("boom")
	for _, tc := range []struct {
		name    string
		err     error
		failure slog.Level
		retries int
		elapsed time.Duration
		want    slog.Level
	}{
		{"routine success", nil, slog.LevelError, 0, time.Millisecond, slog.LevelDebug},
		{"just under slow", nil, slog.LevelError, 0, SlowCall - time.Millisecond, slog.LevelDebug},
		{"slow success", nil, slog.LevelError, 0, SlowCall, slog.LevelInfo},
		{"retried success", nil, slog.LevelError, 2, time.Millisecond, slog.LevelInfo},
		{"failure at the door's level", failed, slog.LevelError, 0, time.Millisecond, slog.LevelError},
		{"failure beats slow and retried", failed, slog.LevelError, 3, 2 * SlowCall, slog.LevelError},
		{"failure the door keeps soft", failed, slog.LevelInfo, 0, 5 * SlowCall, slog.LevelInfo},
	} {
		if got := RoutineLevel(tc.err, tc.failure, tc.retries, tc.elapsed); got != tc.want {
			t.Errorf("%s: level = %s, want %s", tc.name, got, tc.want)
		}
	}
}

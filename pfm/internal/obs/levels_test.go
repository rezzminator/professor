package obs

import (
	"log/slog"
	"strings"
	"testing"
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

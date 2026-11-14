package obs

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestTruncateValueAtTheCapBoundary pins § Middleware's field cap: 2 KB passes
// whole, one byte more is cut to the cap with the exact marker naming how many
// bytes were dropped.
func TestTruncateValueAtTheCapBoundary(t *testing.T) {
	if MaxValueBytes != 2048 {
		t.Fatalf("MaxValueBytes = %d, want 2048 (the spec's 2 KB field cap)", MaxValueBytes)
	}
	fits := strings.Repeat("x", 2048)
	if got := TruncateValue(fits); got != fits {
		t.Fatalf("a 2048 byte value was changed: len %d", len(got))
	}
	over := fits + "y"
	want := fits + "…(truncated 1 bytes)"
	if got := TruncateValue(over); got != want {
		t.Fatalf(
			"2049 bytes -> %q…%q (len %d), want the 2048 byte head plus %q",
			got[:8], got[len(got)-24:], len(got), want[len(want)-22:],
		)
	}
	big := strings.Repeat("z", 5000)
	got := TruncateValue(big)
	if !strings.HasSuffix(got, "…(truncated 2952 bytes)") || !strings.HasPrefix(got, big[:2048]) {
		t.Fatalf("5000 bytes -> len %d suffix %q", len(got), got[len(got)-26:])
	}
}

// TestTruncateValueKeepsUTF8Whole: the cut never splits a multi-byte rune, and
// the marker counts every byte that did not survive.
func TestTruncateValueKeepsUTF8Whole(t *testing.T) {
	value := strings.Repeat("x", 2047) + "é" + "tail"
	got := TruncateValue(value)
	if got, want := runs(got), "{2047×x}…(truncated 6 bytes)"; got != want {
		t.Fatalf("TruncateValue = %q, want %q", got, want)
	}
}

func TestHandlerCapsEveryStringFieldAtDebug(t *testing.T) {
	ctx, recorder := Test(t)
	Logger(ctx).Debug("dump", "path", strings.Repeat("p", 3000), "reason", strings.Repeat("r", 2048))
	Logger(ctx).Error("fail", FieldErr, slog.AnyValue(strings.Repeat("e", 2049)))
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	path, _ := records[0].Field("path")
	reason, _ := records[0].Field("reason")
	err, _ := records[1].Field(FieldErr)
	if got, want := runs(
		fmt.Sprint(path, " | ", reason, " | ", err),
	), "{2048×p}…(truncated 952 bytes) | {2048×r} | {2048×e}…(truncated 1 bytes)"; got != want {
		t.Fatalf("path | reason | err = %q, want %q", got, want)
	}
}

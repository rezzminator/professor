package obs

import (
	"context"
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
	if strings.Contains(got, "�") || !strings.HasPrefix(got, strings.Repeat("x", 2047)+"…(truncated ") {
		t.Fatalf("cut split the rune: %q", got[2040:])
	}
	if !strings.HasSuffix(got, "(truncated 6 bytes)") {
		t.Fatalf("marker = %q, want 6 bytes dropped (é is two, tail is four)", got[len(got)-22:])
	}
}

// TestHandlerCapsEveryStringFieldAtDebug: the cap runs inside the handler, so
// a middleware dumping a 3 KB argument at DEBUG writes 2 KB plus the marker —
// there is no call site that can bypass it.
func TestHandlerCapsEveryStringFieldAtDebug(t *testing.T) {
	ctx, recorder := Test(t)
	Logger(ctx).Debug("dump", "path", strings.Repeat("p", 3000), "reason", strings.Repeat("r", 2048))
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	path, _ := records[0].Field("path")
	const marker = "…(truncated 952 bytes)"
	if text, _ := path.(string); len(text) != 2048+len(marker) || !strings.HasSuffix(text, marker) {
		t.Fatalf("path field = len %d, want the 2048 byte head plus the marker", len(text))
	}
	if reason, _ := records[0].Field("reason"); len(reason.(string)) != 2048 {
		t.Fatalf("a 2048 byte field was cut to %d", len(reason.(string)))
	}
	// An error value is a string field too.
	Logger(ctx).Error("fail", FieldErr, slog.AnyValue(strings.Repeat("e", 2049)))
	last := recorder.Records()[1]
	if err, _ := last.Field(FieldErr); !strings.HasSuffix(err.(string), "…(truncated 1 bytes)") {
		t.Fatalf("err field = %q", err)
	}
	_ = context.Background()
}

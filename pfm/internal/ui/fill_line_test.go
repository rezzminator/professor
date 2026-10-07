package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A line cut at the pane width ends in an ellipsis, so the reader sees the
// cut rather than a sentence that looks complete (a status line losing its
// retry time used to read as a whole message).
func TestFillLineMarksACutWithAnEllipsis(t *testing.T) {
	line := fillLine("  ⚠ account 6 rate-limited — retry 15:04; limits unavailable", 24)
	if got := lipgloss.Width(line); got != 24 {
		t.Fatalf("cut line width=%d, want 24: %q", got, line)
	}
	if !strings.HasSuffix(line, "…") {
		t.Fatalf("cut line %q, want it to end in …", line)
	}
	if short := fillLine("fits", 8); short != "fits    " {
		t.Fatalf("short line %q, want it padded, never marked", short)
	}
}

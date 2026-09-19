package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderProfessorUpdateFailedRowNamesTheFailure pins the row's own
// shape: it carries the failure text (round 1's stderr-only notice, folded
// in here) and never the "Enter →" action hint ProfessorUpdate's row
// carries — the row offers no action (internal/ui/model.go's Enter handler
// no-ops on it).
func TestRenderProfessorUpdateFailedRowNamesTheFailure(t *testing.T) {
	line := renderProfessorUpdateFailedRow("│ ", "failing since 2026-09-19 (network): dial tcp: refused", false, 100)
	plain := ansi.Strip(line)
	if !strings.Contains(plain, "PROFESSOR UPDATE CHECK FAILING") {
		t.Fatalf("rendered row = %q, want the failing-check banner", plain)
	}
	if !strings.Contains(plain, "failing since 2026-09-19") {
		t.Fatalf("rendered row = %q, want the failure detail", plain)
	}
	if strings.Contains(plain, "Enter") {
		t.Fatalf("rendered row = %q, a notice with no action must not invite one", plain)
	}
}

// TestRenderProfessorUpdateFailedRowSelectedStyleDiffers pins that the
// selected and unselected renders are visually distinct styles, the same
// contract every other row kind's selected state holds to.
func TestRenderProfessorUpdateFailedRowSelectedStyleDiffers(t *testing.T) {
	unselected := renderProfessorUpdateFailedRow("│ ", "boom", false, 60)
	selected := renderProfessorUpdateFailedRow("› ", "boom", true, 60)
	if unselected == selected {
		t.Fatalf("selected and unselected renders are identical: %q", unselected)
	}
}

package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderProfessorUpdateFailedRowNamesTheFailure(t *testing.T) {
	line := renderProfessorUpdateFailedRow("│ ", "failing since 2026-09-19 (network): dial tcp: refused", false, 100)
	if plain, want := ansi.Strip(
		line,
	), "│ ⚠ PROFESSOR UPDATE CHECK FAILING ⚠  failing since 2026-09-19 (network): dial tcp: refused         "; plain != want {
		t.Fatalf("rendered row = %q, want %q", plain, want)
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

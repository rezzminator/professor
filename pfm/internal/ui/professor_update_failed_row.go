package ui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// professorUpdateFailedStyle and its selected twin render the
// ProfessorUpdateFailed row: a fixed error red, deliberately NOT re-derived
// from the cosmos palette the way ProfessorUpdate's amber is — a failed
// check is an alarm, and an alarm that quietly took on a calm theme's colors
// would defeat the one thing this row exists to do: never look like a
// genuine update notice or a healthy "nothing to report".
var (
	professorUpdateFailedStyle = lipgloss.NewStyle().Bold(true).
					Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#fb7185"))
	professorUpdateFailedSelectedStyle = lipgloss.NewStyle().Bold(true).
						Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#f43f5e"))
)

// renderProfessorUpdateFailedRow renders the ProfessorUpdateFailed notice
// row: no "Enter →" hint, unlike ProfessorUpdate's — the row carries no
// engine, no socket, and offers no action, so Enter on it is a no-op
// (internal/ui/model.go), and the row itself must never invite one.
func renderProfessorUpdateFailedRow(pointer, name string, selected bool, width int) string {
	content := pointer + "⚠ PROFESSOR UPDATE CHECK FAILING ⚠  " + name
	line := fillLine(ansi.Truncate(content, width, "…"), width)
	if selected {
		return professorUpdateFailedSelectedStyle.Render(line)
	}
	return professorUpdateFailedStyle.Render(line)
}

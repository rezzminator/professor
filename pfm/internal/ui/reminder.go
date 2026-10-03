package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/rezzminator/professor/pfm/internal/theme"
)

// A chat with an unseen fired reminder renders red, so it reads as the row
// that wants attention; these are the defaults until configureStyles runs.
var (
	reminderRowStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fb7185"))
	reminderSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#fb7185")).
				Background(lipgloss.Color("#334155"))
)

// configureReminderStyles derives the reminder styles from the existing error
// red; it runs after selectedStyle is set, since the selected twin extends it.
func configureReminderStyles(palette theme.Palette) {
	reminderRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.LimitRed))
	reminderSelectedStyle = selectedStyle.Bold(true).Foreground(lipgloss.Color(palette.LimitRed))
}

// renderRemindedRow styles a filled row line red; the cursor row stays red
// too, since the cursor usually starts on the top (reminded) row.
func renderRemindedRow(line string, selected bool) string {
	if selected {
		return reminderSelectedStyle.Render(line)
	}
	return reminderRowStyle.Render(line)
}

// pinRemindedRows emits every reminded, visible row ahead of every other row
// (update and new-chat pins, project and name groups) in project-group order,
// flat: such a row never joins a name group.
func (model *Model) pinRemindedRows(pinned map[int]bool) {
	for _, group := range model.groups {
		for _, index := range group.indices {
			if model.rows[index].Reminded && model.visibleInView(model.rows[index]) {
				model.order = append(model.order, index)
				pinned[index] = true
			}
		}
	}
}

// chatsHeaderLine is the Chats tab's third header line: the fuzzy-search hint,
// or, when the reminder flags could not be read, that failure — an unreadable
// flag store never renders as "no reminders".
func (model Model) chatsHeaderLine(width int) string {
	if model.reminderError != "" {
		return warnStyle.Render(fillLine(" Chats · reminder flags unreadable: "+model.reminderError, width))
	}
	return dimStyle.Render(fillLine(" Chats · fuzzy search and all existing chat controls", width))
}

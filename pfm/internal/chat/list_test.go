package chat

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// TestListNeverListsThePickerPlaceholders pins which rows chat_ls drops: only
// the picker's "start a new chat" actions, which have no chat behind them. A
// Booting row is a real chat with a live pane — it answers inject by name — so
// it is listed, never reported absent for its first minute.
func TestListNeverListsThePickerPlaceholders(t *testing.T) {
	for _, kind := range []compose.Kind{compose.NewClaude, compose.NewCodex, compose.NewOpenCode} {
		if !placeholderRow(kind) {
			t.Errorf("placeholder kind %s would be listed", kind)
		}
	}
	for _, kind := range []compose.Kind{
		compose.Booting, compose.LiveClaude, compose.LiveCodex, compose.LiveSplit, compose.Agent,
		compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode,
	} {
		if placeholderRow(kind) {
			t.Errorf("real row kind %s would be dropped", kind)
		}
	}
}

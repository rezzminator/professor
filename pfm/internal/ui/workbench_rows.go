package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func isFleetNewChatRow(row compose.Row) bool {
	return row.Workbench == "" && isNewChatActionKind(row.Kind)
}

func isLaunchFailureNotice(kind compose.Kind) bool {
	return kind == compose.ProfessorUpdateFailed || kind == compose.WorkbenchInvalid
}

func (model Model) newChatEnginesFor(row compose.Row) []pfmengine.ID {
	if row.Workbench != "" && isNewChatActionKind(row.Kind) {
		return row.Engines
	}
	return model.newChatEngines()
}

func (model Model) effectiveNewChatEngine(row compose.Row) pfmengine.ID {
	if row.Workbench == "" || !isNewChatActionKind(row.Kind) {
		return model.newChatEngine
	}
	ids := model.newChatEnginesFor(row)
	if slices.Contains(ids, model.newChatEngine) || len(ids) == 0 {
		return model.newChatEngine
	}
	return ids[0]
}

func (model Model) newChatActionRow(row compose.Row) (compose.Row, bool) {
	id := model.effectiveNewChatEngine(row)
	switch id {
	case pfmengine.Claude:
		row.Kind = compose.NewClaude
	case pfmengine.Codex:
		row.Kind = compose.NewCodex
	case pfmengine.OpenCode:
		row.Kind = compose.NewOpenCode
	default:
		return row, false
	}
	row.Name = "New " + pfmengine.MustLookup(id).Short + " chat"
	row.Account = model.accountForKind(row.Kind)
	return row, true
}

func (model Model) selectNewChat(row compose.Row) (tea.Model, tea.Cmd) {
	selected, ok := model.newChatActionRow(row)
	if !ok {
		model.killStatus = "new chat is not available for " + pfmengine.MustLookup(
			model.effectiveNewChatEngine(row),
		).Short
		return model, nil
	}
	model.outcome = OutcomeSelected
	model.outcomeRow = selected
	return model, tea.Quit
}

func workbenchNameGroup(row compose.Row) (key, prefix string, ok bool) {
	prefix, ok = nameGroupPrefix(row.Name)
	key = prefix
	if row.Workbench != "" {
		key = row.Workbench + "\x00" + prefix
	}
	return key, prefix, ok
}

func renderWorkbenchInvalidRow(pointer, name string, selected bool, width int) string {
	line := fillLine(ansi.Truncate(pointer+"⚠ WORKBENCH ⚠  "+name, width, "…"), width)
	if selected {
		return professorUpdateFailedSelectedStyle.Render(line)
	}
	return professorUpdateFailedStyle.Render(line)
}

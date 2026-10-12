package ui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/spawn"
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

// effectiveNewChatEngine is the engine a ✦ row shows: the one its wizard is on
// while the wizard is open, else the one the last launch started (launchwizard.go).
func (model Model) effectiveNewChatEngine(row compose.Row) pfmengine.ID {
	if model.wizardOn(row) {
		return model.wizard.engine
	}
	return model.idleEngine(row)
}

func (model Model) newChatActionRow(row compose.Row) (compose.Row, bool) {
	return model.newChatRowFor(row, model.effectiveNewChatEngine(row))
}

// newChatRowFor turns a ✦ row into the launch row of engine id.
func (model Model) newChatRowFor(row compose.Row, id pfmengine.ID) (compose.Row, bool) {
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

func adjacentID(current pfmengine.ID, direction int, ids []pfmengine.ID) pfmengine.ID {
	if len(ids) == 0 {
		return ""
	}
	for index, id := range ids {
		if id == current {
			if direction < 0 {
				return ids[(index+len(ids)-1)%len(ids)]
			}
			return ids[(index+1)%len(ids)]
		}
	}
	return ids[0]
}

func (model Model) newChatEngines() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(pfmengine.All()))
	appendUnique := func(id pfmengine.ID) {
		for _, present := range ids {
			if present == id {
				return
			}
		}
		ids = append(ids, id)
	}
	for index := range model.rows {
		row := &model.rows[index]
		if row.Workbench != "" {
			continue
		}
		switch row.Kind {
		case compose.NewClaude:
			appendUnique(pfmengine.Claude)
		case compose.NewCodex:
			appendUnique(pfmengine.Codex)
		case compose.NewOpenCode:
			appendUnique(pfmengine.OpenCode)
		}
	}
	for _, id := range spawn.RegisteredLaunchers() {
		if id != pfmengine.Claude && id != pfmengine.Codex {
			appendUnique(id)
		}
	}
	return ids
}

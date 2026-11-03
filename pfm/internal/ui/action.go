package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func (model Model) action(action string, command tea.Cmd) (tea.Model, tea.Cmd) {
	obs.Action(model.samplingContext, action, obs.DoorPicker)
	return model, command
}

func (model Model) queryAction(value string) {
	action := "picker.query-edit"
	if strings.HasPrefix(value, "@") || strings.HasPrefix(model.query.Value(), "@") {
		action = pickerOmniboxAction
	}
	model.action(action, nil)
}

func (model Model) killAction(killed bool) {
	action := "chat.kill"
	if killed {
		action = "chat.unkill"
	}
	model.action(action, nil)
}

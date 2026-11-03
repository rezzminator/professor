package ui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

func TestPickerActionsCarryTheirBeat(t *testing.T) {
	for _, row := range []struct {
		name    string
		action  string
		prepare func(*Model)
		keys    []tea.KeyPressMsg
	}{
		{"quit", "picker.quit", nil, []tea.KeyPressMsg{specialKey(tea.KeyEscape)}},
		{"tabs", "picker.tabs", nil, []tea.KeyPressMsg{specialKey(tea.KeyTab)}},
		{"stats sort", "picker.stats-sort", func(model *Model) { model.tab = TabStats }, []tea.KeyPressMsg{printableKey('c')}},
		{"limits scroll", "picker.limits-scroll", func(model *Model) { model.tab = TabLimits }, []tea.KeyPressMsg{specialKey(tea.KeyDown)}},
		{"cosmos", "picker.cosmos", func(model *Model) { model.tab = TabCosmos }, []tea.KeyPressMsg{printableKey('o')}},
		{"omnibox", "picker.omnibox", nil, []tea.KeyPressMsg{shiftKey(tea.KeyRight)}},
		{"query", "picker.query-edit", nil, []tea.KeyPressMsg{printableKey('a')}},
		{"carousel", "picker.carousel-deactive", nil, []tea.KeyPressMsg{specialKey(tea.KeyRight)}},
		{"kill", "chat.kill", nil, []tea.KeyPressMsg{controlKey('x')}},
		{"unkill", "chat.unkill", func(model *Model) { model.rows[0].Killed = true }, []tea.KeyPressMsg{controlKey('x')}},
		{"reload", "chat.reload", nil, []tea.KeyPressMsg{controlKey('o')}},
		{"open", "chat.open", nil, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"resume", "chat.resume", func(model *Model) {
			model.rows[0].Kind = compose.ResumeClaude
			model.rows[0].Backend = spawn.BackendHerdr
		}, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"new wizard", "chat.new", func(model *Model) { *model = NewModel(wizardSnapshot([]int{1}, nil, nil)) }, []tea.KeyPressMsg{specialKey(tea.KeyEnter), specialKey(tea.KeyEnter)}},
		{"new direct", "chat.new", func(model *Model) { model.rows[0].Kind = compose.NewClaude }, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"resume wizard", "chat.resume", func(model *Model) {
			*model = NewModel(wizardSnapshot([]int{1}, nil, nil))
			model.rows[0] = compose.Row{Kind: compose.ResumeClaude, ID: "atlas", Name: "atlas"}
		}, []tea.KeyPressMsg{specialKey(tea.KeyEnter), specialKey(tea.KeyEnter)}},
		{"reload carousel", "chat.reload", func(model *Model) { model.actionIndex = 1 }, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"deactivate carousel", "picker.carousel-deactive", func(model *Model) { model.actionIndex = 3 }, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"horizontal tabs", "picker.tabs", func(model *Model) { model.tab = TabLimits }, []tea.KeyPressMsg{specialKey(tea.KeyRight)}},
		{"stats memory sort", "picker.stats-sort", func(model *Model) { model.tab = TabStats }, []tea.KeyPressMsg{printableKey('m')}},
		{"cosmos open", "picker.cosmos", func(model *Model) { model.tab = TabCosmos }, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"omnibox commit", "picker.omnibox", func(model *Model) { model.query.SetValue("@atlas") }, []tea.KeyPressMsg{specialKey(tea.KeyEnter)}},
		{"omnibox edit", "picker.omnibox", nil, []tea.KeyPressMsg{printableKey('@')}},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			model := NewModel(
				Snapshot{
					Rows: []compose.Row{
						{Kind: compose.LiveClaude, ID: "atlas", Name: "atlas", Socket: "seat-atlas"},
					},
					ApplyKill: func(KillChange) error { return nil },
				},
			)
			if row.prepare != nil {
				row.prepare(&model)
			}
			model.samplingContext = obs.With(ctx, obs.FieldBeat, "picker-action", obs.FieldRun, "synthetic")
			for _, key := range row.keys {
				model, _ = applyKey(t, model, key)
			}
			var actions []string
			for _, record := range recorder.Records() {
				if action, found := record.Field(obs.FieldAction); found {
					actions = append(actions, action.(string))
					if beat, _ := record.Field(obs.FieldBeat); beat != "picker-action" {
						t.Fatalf("beat = %v, want picker-action; record=%v", beat, record)
					}
					if door, _ := record.Field(obs.FieldDoor); door != obs.DoorPicker {
						t.Fatalf("door = %v, want picker; record=%v", door, record)
					}
				}
			}
			if !reflect.DeepEqual(actions, []string{row.action}) {
				t.Fatalf("actions = %v, want [%s]; log=%s", actions, row.action, recorder.Raw())
			}
		})
	}
}

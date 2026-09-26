package ui

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// TestKillKeyRefusesALabelKilledRow pins the one place the two kill kinds must
// not be confused: a "_KILL…" chat is held out of the list by its LABEL, so an
// unkill written from the picker would delete a store row that is not holding
// it down and the chat would stay killed anyway. The key does nothing instead.
func TestKillKeyRefusesALabelKilledRow(t *testing.T) {
	snapshot := Snapshot{
		View:           compose.KilledView,
		KilledCount:    1,
		PrimaryAccount: 1,
		NowNS:          fixtureNowNS,
		Width:          120,
		Height:         17,
		Rows: []compose.Row{{
			Kind:        compose.ResumeClaude,
			ID:          "33333333-3333-4333-8333-333333333333",
			Name:        "_KILL headless worker",
			Project:     "alpha",
			CWD:         "/work/alpha",
			Size:        1024,
			PromptCount: 4,
			ActivityNS:  fixtureNowNS,
			Account:     1,
			Killed:      true,
			NameKilled:  true,
		}},
	}

	model := NewModel(snapshot)
	model, _ = applyKey(t, model, controlKey('x'))
	result := model.Result()
	if len(result.KillChanges) != 0 {
		t.Fatalf("kill key wrote a change for a label-killed row: %#v", result.KillChanges)
	}
	if !model.rows[0].Killed || !model.rows[0].NameKilled {
		t.Fatalf("label-killed row was flipped in the model: %#v", model.rows[0])
	}
}

// TestKillKeyRefusesTheUpdateCheckFailureRow: the failure row is a notice
// carried from the update-check marker, not a chat — a kill keyed by its
// synthetic id would write a ledger entry that holds nothing down.
func TestKillKeyRefusesTheUpdateCheckFailureRow(t *testing.T) {
	snapshot := Snapshot{
		View:           compose.DefaultView,
		PrimaryAccount: 1,
		NowNS:          fixtureNowNS,
		Width:          120,
		Height:         17,
		Rows: []compose.Row{{
			Kind:    compose.ProfessorUpdateFailed,
			ID:      "pfm-update-check-failed-2026-01-02T03:04:05Z",
			Name:    "failing since 2026-01-02 03:04 (network): 503",
			Project: ".professor",
			CWD:     "/home/test/.professor",
		}},
	}

	model := NewModel(snapshot)
	model, _ = applyKey(t, model, controlKey('x'))
	if changes := model.Result().KillChanges; len(changes) != 0 {
		t.Fatalf("kill key wrote a change for the update-check failure row: %#v", changes)
	}
	if !strings.Contains(model.killStatus, "notice, not a chat") {
		t.Fatalf("killStatus = %q, want the refusal to name the row a notice", model.killStatus)
	}
}

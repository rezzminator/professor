package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// After an in-picker deactivate the cursor stays on the conversation just
// deactivated — now its resumable row — so Enter resumes it rather than the
// neighbour that slid under the cursor. A native stop only schedules its end,
// so Enter refuses that row while the roster still lists the seat live.
func TestDeactivateKeepsCursorOnTheDeactivatedConversation(t *testing.T) {
	deactivated := compose.Row{
		Kind: compose.LiveClaude, ID: "deactivated-session", Name: "Deactivated", Project: "p",
		Backend: "herdr", Socket: "/fixture/herdr.sock", PaneID: "w1:p1", SeatID: "seat-1",
	}
	neighbour := compose.Row{
		Kind: compose.LiveClaude, ID: "neighbour-session", Name: "Neighbour", Project: "p",
		Backend: "herdr", Socket: "/fixture/herdr.sock", PaneID: "w1:p2", SeatID: "seat-2",
	}
	snapshot := Snapshot{
		Rows:            []compose.Row{deactivated, neighbour},
		InitialCursorID: deactivated.ID,
		ApplyDeactivate: func(compose.Row) error { return nil },
	}
	selected := func(model Model) compose.Row {
		t.Helper()
		row, ok := model.selectedRow()
		if !ok {
			t.Fatalf("no row under the cursor: %+v", model.VisibleRows())
		}
		return row
	}
	model := NewModel(snapshot)
	if got := selected(model); got.ID != deactivated.ID {
		t.Fatalf("setup cursor on %q, want %q", got.ID, deactivated.ID)
	}
	model.deactivate(deactivated)
	got := selected(model)
	if got.ID != deactivated.ID || got.Kind != compose.ResumeClaude || got.Socket != "" ||
		got.PaneID != "" || got.SeatID != "" || got.Backend != "" {
		t.Fatalf("after deactivate the cursor row = %+v, want the resumable row of %q", got, deactivated.ID)
	}
	enter := func(model Model) (Model, tea.Cmd) {
		t.Helper()
		return applyKey(t, model, specialKey(tea.KeyEnter))
	}
	wantRefusal := "resume refused — Deactivated is still ending; resume once its pane has closed"
	if refused, cmd := enter(model); cmd != nil || refused.outcome == OutcomeSelected ||
		refused.killStatus != wantRefusal {
		t.Fatalf("enter while the native end is pending: cmd=%v outcome=%v status=%q, want %q",
			cmd != nil, refused.outcome, refused.killStatus, wantRefusal)
	}
	// A refresh whose snapshot still lists the stopped pane live keeps the
	// cursor on the conversation, not the neighbour, and revives no pane.
	model.applyRefresh(Snapshot{Rows: []compose.Row{deactivated, neighbour}})
	got = selected(model)
	if got.ID != deactivated.ID || got.Kind.IsLiveSeat() {
		t.Fatalf("after a stale refresh the cursor row = %+v", got)
	}
	if refused, cmd := enter(model); cmd != nil || refused.killStatus != wantRefusal {
		t.Fatalf("enter after a stale refresh: cmd=%v status=%q, want %q", cmd != nil, refused.killStatus, wantRefusal)
	}
	// A refresh that already carries the resumable row shows it once.
	resumable := deactivated
	resumable.Kind, resumable.Backend, resumable.Socket, resumable.PaneID, resumable.SeatID =
		compose.ResumeClaude, "", "", "", ""
	model.applyRefresh(Snapshot{Rows: []compose.Row{resumable, neighbour}})
	got = selected(model)
	count := 0
	for _, row := range model.VisibleRows() {
		if row.ID == deactivated.ID {
			count++
		}
	}
	if got.ID != deactivated.ID || got.Kind != compose.ResumeClaude || count != 1 {
		t.Fatalf("after a fresh refresh the cursor row = %+v, rows for it = %d", got, count)
	}
	opened, cmd := enter(model)
	if cmd == nil || opened.outcome != OutcomeSelected || opened.outcomeRow.ID != deactivated.ID {
		t.Fatalf("enter once the seat left the roster: cmd=%v outcome=%v row=%q, want the resume of %q",
			cmd != nil, opened.outcome, opened.outcomeRow.ID, deactivated.ID)
	}
	// A tmux stop is synchronous: its resumable row opens at once.
	tmux := compose.Row{
		Kind: compose.LiveClaude, ID: "tmux-session", Name: "Tmux", Project: "p", Socket: "/fixture/tmux",
	}
	tmuxModel := NewModel(Snapshot{
		Rows: []compose.Row{tmux}, InitialCursorID: tmux.ID, ApplyDeactivate: func(compose.Row) error { return nil },
	})
	tmuxModel.deactivate(tmux)
	opened, cmd = enter(tmuxModel)
	if cmd == nil || opened.outcome != OutcomeSelected || opened.outcomeRow.ID != tmux.ID {
		t.Fatalf("enter on a deactivated tmux row: cmd=%v outcome=%v row=%q, want its resume",
			cmd != nil, opened.outcome, opened.outcomeRow.ID)
	}
}

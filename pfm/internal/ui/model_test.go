package ui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
)

func TestModelKeysKillModifiersAndCancel(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.InitialCursorID = snapshot.Rows[1].ID
	model := NewModel(snapshot)
	follow := model.SelectedKey()
	visible := model.VisibleRows()
	model, command := applyKey(t, model, controlKey('r'))
	if command != nil || model.SelectedKey() != follow ||
		!reflect.DeepEqual(model.VisibleRows(), visible) {
		t.Fatalf(
			"ctrl+r command=%v selected=%q rows_changed=%t",
			command,
			model.SelectedKey(),
			!reflect.DeepEqual(model.VisibleRows(), visible),
		)
	}

	beforeRows := append([]compose.Row(nil), snapshot.Rows...)
	model, _ = applyKey(t, model, controlKey('x'))
	result := model.Result()
	if model.SelectedKey() != follow ||
		len(result.KillChanges) != 1 ||
		!result.KillChanges[0].Killed ||
		result.KillChanges[0].Engine != "cx" {
		t.Fatalf("kill selected=%q result=%#v", model.SelectedKey(), result)
	}
	if !reflect.DeepEqual(snapshot.Rows, beforeRows) {
		t.Fatal("pure model mutated input snapshot")
	}
	// A second ⌃X is an UNKILL, applied like the first one. The receipt keeps
	// the row's final state rather than cancelling out to nothing: both writes
	// really happened.
	model, _ = applyKey(t, model, controlKey('x'))
	undo := model.Result()
	if len(undo.KillChanges) != 1 || undo.KillChanges[0].Killed {
		t.Fatalf("second ⌃X did not record an unkill: %#v", undo)
	}

	model, _ = applyKey(t, model, controlKey('e'))
	if model.Cache1H() {
		t.Fatal("ctrl+e did not toggle 1h off")
	}
	// The selected row is Codex, so ⌃S cycles the Codex roster and leaves the
	// independently persisted Claude primary alone.
	for step := 0; step < 4; step++ {
		before := model.codexPrimary
		model, _ = applyKey(t, model, controlKey('s'))
		want := before%3 + 1
		if model.codexPrimary != want || model.PrimaryAccount() != 2 {
			t.Fatalf("codex account=%d want=%d; Claude primary=%d", model.codexPrimary, want, model.PrimaryAccount())
		}
	}

	ignored, command := applyKey(t, model, controlKey('t'))
	if command != nil || ignored.Result().Kind != OutcomeNone {
		t.Fatalf("ctrl+t command=%v result=%#v, want no-op", command, ignored.Result())
	}
	cancelled, command := applyKey(t, model, specialKey(tea.KeyEscape))
	if command == nil || cancelled.Result().Kind != OutcomeCancelled {
		t.Fatalf("cancel command=%v result=%#v", command, cancelled.Result())
	}
}

func TestChatCursorWrapsUpFromFirstRowToLast(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	if model.Cursor() != 0 || model.FilteredRowCount() < 2 {
		t.Fatalf("setup cursor=%d rows=%d", model.Cursor(), model.FilteredRowCount())
	}
	model.actionIndex = 2
	model, command := applyKey(t, model, specialKey(tea.KeyUp))
	if command != nil || model.Cursor() != model.FilteredRowCount()-1 || model.ActionIndex() != 0 {
		t.Fatalf(
			"up wrap command=%v cursor=%d/%d action=%d",
			command,
			model.Cursor(),
			model.FilteredRowCount(),
			model.ActionIndex(),
		)
	}
}

func TestChatCursorWrapsDownFromLastRowToFirst(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	model, _ = applyKey(t, model, specialKey(tea.KeyEnd))
	if model.Cursor() != model.FilteredRowCount()-1 || model.FilteredRowCount() < 2 {
		t.Fatalf("setup cursor=%d rows=%d", model.Cursor(), model.FilteredRowCount())
	}
	model.actionIndex = 2
	model, command := applyKey(t, model, specialKey(tea.KeyDown))
	if command != nil || model.Cursor() != 0 || model.ActionIndex() != 0 {
		t.Fatalf(
			"down wrap command=%v cursor=%d action=%d",
			command,
			model.Cursor(),
			model.ActionIndex(),
		)
	}
}

func TestChatCursorMovementIsNoOpForEmptyList(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.Rows = nil
	model := NewModel(snapshot)
	for _, key := range []tea.KeyPressMsg{specialKey(tea.KeyUp), specialKey(tea.KeyDown)} {
		var command tea.Cmd
		model, command = applyKey(t, model, key)
		if command != nil || model.Cursor() != 0 || model.FilteredRowCount() != 0 {
			t.Fatalf(
				"empty move %q command=%v cursor=%d rows=%d",
				key.String(),
				command,
				model.Cursor(),
				model.FilteredRowCount(),
			)
		}
	}
}

func TestNewChatCarouselAndChatActionCarousel(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.Rows = []compose.Row{
		{Kind: compose.NewClaude, Name: "New Claude chat", Project: "new"},
		{Kind: compose.NewCodex, Name: "New Codex chat", Project: "new"},
		{Kind: compose.NewOpenCode, Name: "New OpenCode chat", Project: "new", Account: 5},
	}
	snapshot.OpenCodePrimaryAccount = 5
	snapshot.OpenCodeAccountIDs = []int{5}
	snapshot.MergeNewChat = true
	model := NewModel(snapshot)
	if model.NewChatEngine() != pfmengine.Claude {
		t.Fatalf("new chat engine=%q, want %s", model.NewChatEngine(), pfmengine.Claude)
	}
	model, command := applyKey(t, model, specialKey(tea.KeyRight))
	if command != nil || model.NewChatEngine() != pfmengine.Codex {
		t.Fatalf("right new-chat engine=%q command=%v", model.NewChatEngine(), command)
	}
	model, command = applyKey(t, model, specialKey(tea.KeyRight))
	if command != nil || model.NewChatEngine() != pfmengine.OpenCode {
		t.Fatalf("second right new-chat engine=%q command=%v", model.NewChatEngine(), command)
	}
	model, command = applyKey(t, model, specialKey(tea.KeyEnter))
	if command == nil || model.Result().Kind != OutcomeSelected || model.Result().Row.Kind != compose.NewOpenCode ||
		model.Result().PrimaryAccount != 5 {
		t.Fatalf("new-chat Enter result=%#v command=%v", model.Result(), command)
	}

	snapshot.Rows = []compose.Row{{Kind: compose.LiveClaude, ID: "live", Name: "live", Project: "p", Socket: "s"}}
	snapshot.InitialCursorID = "live"
	var deactivated []compose.Row
	snapshot.ApplyDeactivate = func(row compose.Row) error {
		deactivated = append(deactivated, row)
		return nil
	}
	model = NewModel(snapshot)
	model, command = applyKey(t, model, specialKey(tea.KeyRight))
	if command != nil || model.ActionIndex() != 1 {
		t.Fatalf("chat carousel index=%d command=%v", model.ActionIndex(), command)
	}
	model, command = applyKey(t, model, specialKey(tea.KeyEnter))
	if command == nil || model.Result().Kind != OutcomeReboot {
		t.Fatalf("chat carousel Enter result=%#v command=%v", model.Result(), command)
	}

	model = NewModel(snapshot)
	for step := 0; step < 4; step++ {
		model, command = applyKey(t, model, specialKey(tea.KeyRight))
		if command != nil {
			t.Fatalf("deactive carousel step %d returned command", step)
		}
	}
	model, command = applyKey(t, model, specialKey(tea.KeyEnter))
	if command != nil || model.Result().Kind != OutcomeNone || len(deactivated) != 1 || deactivated[0].Socket != "s" {
		t.Fatalf("deactive carousel Enter result=%#v command=%v deactivated=%#v", model.Result(), command, deactivated)
	}
	if model.FilteredRowCount() != 0 {
		t.Fatalf("deactivated live row remained visible: %#v", model.VisibleRows())
	}
}

func TestProfessorUpdateRowLeadsNewChatPersistsAcrossRefreshAndLaunchesChosenEngine(t *testing.T) {
	snapshot := Snapshot{
		Rows: []compose.Row{
			{
				Kind: compose.ProfessorUpdate, ID: "pfm-update-v0.61.2",
				Name: "Professor v0.61.2 available — update", Project: ".professor",
				CWD: "/home/test/.professor",
			},
			{
				Kind: compose.LiveClaude, ID: "professor-work", Name: "Professor work",
				Project: ".professor", CWD: "/home/test/.professor",
			},
			{Kind: compose.NewClaude, Name: "New Claude chat", Project: "project", CWD: "/work/project"},
			{Kind: compose.NewCodex, Name: "New Codex chat", Project: "project", CWD: "/work/project"},
			{Kind: compose.NewOpenCode, Name: "New OpenCode chat", Project: "project", CWD: "/work/project"},
		},
		View:                   compose.DefaultView,
		PrimaryAccount:         1,
		AccountIDs:             []int{1},
		CodexPrimaryAccount:    2,
		CodexAccountIDs:        []int{2},
		OpenCodePrimaryAccount: 3,
		OpenCodeAccountIDs:     []int{3},
		MergeNewChat:           true,
		NowNS:                  fixtureNowNS,
		Width:                  120,
		Height:                 20,
	}
	model := NewModel(snapshot)
	visible := model.VisibleRows()
	if len(visible) < 2 || visible[0].Kind != compose.ProfessorUpdate || !isNewChatActionKind(visible[1].Kind) {
		t.Fatalf("visible order = %#v, want update immediately before merged new-chat row", visible)
	}
	model, command := applyKey(t, model, controlKey('x'))
	if command != nil || len(model.Result().KillChanges) != 0 || !strings.Contains(model.killStatus, "not a chat") {
		t.Fatalf(
			"update banner accepted hide: status=%q result=%#v command=%v",
			model.killStatus,
			model.Result(),
			command,
		)
	}

	refresh := snapshot
	refresh.Rows = snapshot.Rows[1:]
	updated, _ := model.Update(RefreshMsg{Snapshot: refresh})
	model = updated.(Model)
	visible = model.VisibleRows()
	if len(visible) == 0 || visible[0].Kind != compose.ProfessorUpdate {
		t.Fatalf("refresh dropped cached update row: %#v", visible)
	}

	model, command = applyKey(t, model, specialKey(tea.KeyRight))
	if command != nil || model.NewChatEngine() != pfmengine.Codex {
		t.Fatalf("update engine after Right = %q command=%v, want Codex", model.NewChatEngine(), command)
	}
	model, command = applyKey(t, model, specialKey(tea.KeyEnter))
	result := model.Result()
	if command == nil || result.Kind != OutcomeProfessorUpdate || result.Engine != pfmengine.Codex ||
		result.PrimaryAccount != 2 || result.Row.CWD != "/home/test/.professor" {
		t.Fatalf("update selection result=%#v command=%v", result, command)
	}
}

func TestProfessorUpdateCtrlSCyclesVisibleClaudeAccountEvenWhenCodexChosen(t *testing.T) {
	snapshot := Snapshot{
		Rows: []compose.Row{
			{Kind: compose.ProfessorUpdate, ID: "pfm-update-v0.61.2", Name: "v0.61.2", Project: ".professor"},
			{Kind: compose.NewClaude, Name: "New Claude chat", Project: "project"},
			{Kind: compose.NewCodex, Name: "New Codex chat", Project: "project"},
		},
		View:                compose.DefaultView,
		PrimaryAccount:      2,
		AccountIDs:          []int{1, 2},
		CodexPrimaryAccount: 7,
		CodexAccountIDs:     []int{7},
		MergeNewChat:        true,
		NowNS:               fixtureNowNS,
		Width:               120,
		Height:              20,
	}
	model := NewModel(snapshot)
	model, command := applyKey(t, model, specialKey(tea.KeyRight))
	if command != nil || model.NewChatEngine() != pfmengine.Codex {
		t.Fatalf("update engine after Right = %q command=%v, want Codex", model.NewChatEngine(), command)
	}
	model, command = applyKey(t, model, controlKey('s'))
	if command != nil || model.PrimaryAccount() != 1 {
		t.Fatalf(
			"Ctrl+S on update selected for Codex left visible Claude account=%d command=%v, want 1",
			model.PrimaryAccount(),
			command,
		)
	}
}

func TestProfessorUpdateBannerIsFullWidthGoldAndAnimated(t *testing.T) {
	snapshot := Snapshot{
		Rows: []compose.Row{
			{Kind: compose.ProfessorUpdate, ID: "pfm-update-v0.61.2", Name: "v0.61.2", Project: ".professor"},
			{Kind: compose.NewClaude, Name: "New Claude chat", Project: ".professor"},
			{Kind: compose.NewCodex, Name: "New Codex chat", Project: ".professor"},
			{Kind: compose.NewOpenCode, Name: "New OpenCode chat", Project: ".professor"},
		},
		View: compose.DefaultView, MergeNewChat: true, NowNS: fixtureNowNS,
		AccountIDs: []int{1}, CodexAccountIDs: []int{1}, OpenCodeAccountIDs: []int{1},
		Width: 120, Height: 20,
	}
	model := NewModel(snapshot)
	line := model.renderRow(snapshot.Rows[0], false, 118)
	plain := ansi.Strip(line)
	for _, want := range []string{"✦ PROFESSOR UPDATE ✦", "v0.61.2", "◖ Claude ◗", "[ Codex ]", "[ OpenCode ]", "guided upgrade"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("update banner %q lacks %q", plain, want)
		}
	}
	if ansi.StringWidth(line) != 118 || !strings.Contains(line, ";5;") {
		t.Fatalf("update banner width=%d raw=%q, want full width and ANSI blink", ansi.StringWidth(line), line)
	}
	model.nowNS += int64(500 * time.Millisecond)
	if next := ansi.Strip(
		model.renderRow(snapshot.Rows[0], false, 118),
	); !strings.Contains(
		next,
		"✧ PROFESSOR UPDATE ✧",
	) {
		t.Fatalf("animated update banner = %q, want alternate sparkle phase", next)
	}
}

func TestNewChatUsesOnlyPresentEnginesAndCyclesTheirOwnRoster(t *testing.T) {
	codexOnly := Snapshot{
		Rows: []compose.Row{
			{Kind: compose.NewCodex, Name: "New Codex chat", CWD: "/work/new", Account: 7},
		},
		CodexPrimaryAccount: 7,
		CodexAccountIDs:     []int{7, 9},
		MergeNewChat:        true,
		NowNS:               fixtureNowNS,
	}
	model := NewModel(codexOnly)
	if model.NewChatEngine() != pfmengine.Codex {
		t.Fatalf("codex-only engine = %q", model.NewChatEngine())
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyLeft))
	if model.NewChatEngine() != pfmengine.Codex {
		t.Fatalf("single-engine carousel exposed Claude: %q", model.NewChatEngine())
	}
	model, _ = applyKey(t, model, controlKey('s'))
	selected, command := applyKey(t, model, specialKey(tea.KeyEnter))
	if command == nil || selected.Result().Row.Kind != compose.NewCodex || selected.Result().PrimaryAccount != 9 {
		t.Fatalf("Codex account cycle result = %#v", selected.Result())
	}

	claudeOnly := Snapshot{
		Rows:           []compose.Row{{Kind: compose.NewClaude, Name: "New Claude chat", CWD: "/work/new", Account: 2}},
		PrimaryAccount: 2,
		AccountIDs:     []int{2, 4},
		MergeNewChat:   true,
		NowNS:          fixtureNowNS,
	}
	model = NewModel(claudeOnly)
	model, _ = applyKey(t, model, specialKey(tea.KeyRight))
	if model.NewChatEngine() != pfmengine.Claude {
		t.Fatalf("single-engine carousel exposed Codex: %q", model.NewChatEngine())
	}
}

func TestLiveSelectionKeepsBirthAccountSeparateFromSelectedAccount(t *testing.T) {
	for _, test := range []struct {
		name   string
		row    compose.Row
		make   func() Snapshot
		chosen int
	}{
		{
			name: "claude",
			row:  compose.Row{Kind: compose.LiveClaude, Account: 2},
			make: func() Snapshot {
				return Snapshot{PrimaryAccount: 4, AccountIDs: []int{2, 4}}
			},
			chosen: 4,
		},
		{
			name: "codex",
			row:  compose.Row{Kind: compose.LiveCodex, Account: 7},
			make: func() Snapshot {
				return Snapshot{CodexPrimaryAccount: 9, CodexAccountIDs: []int{7, 9}}
			},
			chosen: 9,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := test.make()
			snapshot.Rows = []compose.Row{test.row}
			snapshot.NowNS = fixtureNowNS
			model := NewModel(snapshot)
			selected, command := applyKey(t, model, specialKey(tea.KeyEnter))
			result := selected.Result()
			if command == nil || result.Row.Account != test.row.Account || result.PrimaryAccount != test.chosen {
				t.Fatalf(
					"live handoff row account=%d selected=%d, want birth=%d selected=%d",
					result.Row.Account,
					result.PrimaryAccount,
					test.row.Account,
					test.chosen,
				)
			}
		})
	}
}

// TestCancelKeepsAppliedKillsAndDropsPrimaryChange pins the split that ⌃X
// acting immediately creates: Esc still abandons a ⌃S account switch, which is
// only ever a pending intent, but it can no longer take back a kill. Batching
// the kill until quit meant the only exits that applied one were the ones that
// launched something, so closing the list the natural way silently threw every
// ⌃X away.
func TestCancelKeepsAppliedKillsAndDropsPrimaryChange(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.PrimaryAccount = 1
	var applied []KillChange
	snapshot.ApplyKill = func(change KillChange) error {
		applied = append(applied, change)
		return nil
	}
	model := NewModel(snapshot)

	model, _ = applyKey(t, model, controlKey('x'))
	if len(applied) != 1 || !applied[0].Killed {
		t.Fatalf("⌃X did not apply on the keypress: %#v", applied)
	}
	model, _ = applyKey(t, model, controlKey('s'))
	if model.PrimaryAccount() == 1 {
		t.Fatal("setup: ⌃S did not change the account")
	}

	cancelled, command := applyKey(t, model, specialKey(tea.KeyEscape))
	if command == nil {
		t.Fatal("cancel returned no quit command")
	}
	result := cancelled.Result()
	if result.Kind != OutcomeCancelled ||
		len(result.KillChanges) != 1 ||
		!result.KillChanges[0].Killed ||
		result.PrimaryAccount != 1 {
		t.Fatalf(
			"cancel result=%#v, want Cancelled/kill kept/account reverted to 1",
			result,
		)
	}
	if len(applied) != 1 {
		t.Fatalf("Esc re-applied or reverted the kill: %#v", applied)
	}
}

// TestHideAppliesImmediatelyWithoutEndingALiveChat separates visibility from
// process lifecycle: hiding a row must not stop work that is still running.
func TestHideAppliesImmediatelyWithoutEndingALiveChat(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	var live compose.Row
	for _, row := range snapshot.Rows {
		if row.Kind == compose.LiveClaude && row.Socket != "" {
			live = row
			break
		}
	}
	if live.ID == "" {
		t.Skip("fixture has no live claude row with a socket")
	}
	snapshot.Rows = []compose.Row{live}
	snapshot.InitialCursorID = live.ID
	var applied []KillChange
	snapshot.ApplyKill = func(change KillChange) error {
		applied = append(applied, change)
		return nil
	}
	model := NewModel(snapshot)

	model, _ = applyKey(t, model, controlKey('x'))
	if len(applied) != 1 {
		t.Fatalf("⌃X did not apply on the keypress: %#v", applied)
	}
	if !applied[0].Live || applied[0].Socket != live.Socket {
		t.Fatalf("the kill had nothing to aim at: %#v", applied[0])
	}
	if got := model.rows[0]; got.Kind != live.Kind || got.Socket != live.Socket {
		t.Fatalf("hidden row was incorrectly deactivated: %#v", got)
	}
}

// TestToggleKilledCarriesTheRowsPaneOnlyWhenItIsLive pins the other half of
// the KillChange the applier now depends on: hiding a live chat must also end
// it (manager.Kill's live gate), and the applier can only forward a pane it
// actually received from toggleKilled. A resumable row has no pane to carry —
// forwarding a stale or invented one would tell the applier a dead row is
// live.
func TestToggleKilledCarriesTheRowsPaneOnlyWhenItIsLive(t *testing.T) {
	tests := []struct {
		name string
		row  compose.Row
	}{
		{
			name: "live row",
			row: compose.Row{
				Kind:   compose.LiveClaude,
				ID:     "11111111-1111-4111-8111-111111111111",
				Socket: "cc-1800000000-1-1",
				PaneID: "%3",
				Name:   "live claude",
			},
		},
		{
			name: "resumable row",
			row: compose.Row{
				Kind: compose.ResumeClaude,
				ID:   "44444444-4444-4444-8444-444444444444",
				Name: "resume claude",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := fixtureSnapshot(120)
			snapshot.Rows = []compose.Row{test.row}
			snapshot.InitialCursorID = test.row.ID
			var applied []KillChange
			snapshot.ApplyKill = func(change KillChange) error {
				applied = append(applied, change)
				return nil
			}
			model := NewModel(snapshot)

			_, _ = applyKey(t, model, controlKey('x'))
			if len(applied) != 1 {
				t.Fatalf("⌃X did not apply on the keypress: %#v", applied)
			}
			if applied[0].Socket != test.row.Socket || applied[0].PaneID != test.row.PaneID {
				t.Fatalf(
					"KillChange address = socket=%q pane=%q, want the row's own socket=%q pane=%q",
					applied[0].Socket, applied[0].PaneID, test.row.Socket, test.row.PaneID,
				)
			}
			isLiveRow := test.row.Kind == compose.LiveClaude
			if isLiveRow && (applied[0].Socket == "" || applied[0].PaneID == "") {
				t.Fatalf("a live row produced an empty live address: %#v", applied[0])
			}
			if !isLiveRow && (applied[0].Socket != "" || applied[0].PaneID != "") {
				t.Fatalf("a resumable row produced a live address: %#v", applied[0])
			}
		})
	}
}

// TestKillThatCannotLandLeavesTheRowAlone: a ⌃X that fails must not pretend it
// worked. The row keeps its state and the failure takes the status line.
func TestKillThatCannotLandLeavesTheRowAlone(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.ApplyKill = func(KillChange) error {
		return errors.New("store is locked")
	}
	model := NewModel(snapshot)
	before := model.rows[model.filtered[model.cursor]].Killed

	model, _ = applyKey(t, model, controlKey('x'))
	if model.rows[model.filtered[model.cursor]].Killed != before {
		t.Fatal("a failed ⌃X flipped the row anyway")
	}
	if len(model.Result().KillChanges) != 0 {
		t.Fatalf("a failed ⌃X was recorded: %#v", model.Result().KillChanges)
	}
	if !strings.Contains(model.View().Content, "store is locked") {
		t.Fatal("a failed ⌃X was invisible to the user")
	}
}

func TestEnterOutcomeEveryKindAndLiveReboot(t *testing.T) {
	for _, row := range fixtureSnapshot(120).Rows {
		snapshot := Snapshot{
			Rows:                []compose.Row{row},
			View:                compose.AllView,
			PrimaryAccount:      3,
			AccountIDs:          []int{1, 2, 3},
			CodexPrimaryAccount: 3,
			CodexAccountIDs:     []int{1, 2, 3},
			Cache1H:             true,
			NowNS:               fixtureNowNS,
		}
		model := NewModel(snapshot)
		selected, command := applyKey(t, model, specialKey(tea.KeyEnter))
		result := selected.Result()
		if command == nil ||
			result.Kind != OutcomeSelected ||
			compose.RowKey(result.Row) != compose.RowKey(row) ||
			result.PrimaryAccount != 3 ||
			!result.Cache1H {
			t.Fatalf("%s enter command=%v result=%#v", row.Kind, command, result)
		}

		// Reboot is ⌃O, never ⌃B: the picker always runs inside tmux, and C-b is
		// tmux's prefix — it never reaches the picker.
		reboot, rebootCommand := applyKey(t, model, controlKey('o'))
		if row.Kind.IsLiveSeat() {
			if rebootCommand == nil ||
				reboot.Result().Kind != OutcomeReboot ||
				compose.RowKey(reboot.Result().Row) != compose.RowKey(row) {
				t.Fatalf("%s reboot=%#v cmd=%v", row.Kind, reboot.Result(), rebootCommand)
			}
		} else if rebootCommand != nil || reboot.Result().Kind != OutcomeNone {
			t.Fatalf("%s unexpectedly rebooted: %#v", row.Kind, reboot.Result())
		}

		stale, staleCommand := applyKey(t, model, controlKey('b'))
		if staleCommand != nil || stale.Result().Kind != OutcomeNone {
			t.Fatalf("%s rebooted on the tmux prefix key: %#v", row.Kind, stale.Result())
		}
	}
}

// TestBootingRowIgnoresKillAndRebootButSelectsOnEnter is the pure-model half
// of the booting-row fix: ⌃X and ⌃O must both be no-ops on a booting row —
// its "id" is a crumbless socket, not a chat identity, and stops meaning
// anything once the crumb lands — while Enter still selects it normally, the
// one live operation (attach) this Kind supports.
func TestBootingRowIgnoresKillAndRebootButSelectsOnEnter(t *testing.T) {
	row := compose.Row{
		Kind:    compose.Booting,
		ID:      "cc-new-fixture-1",
		Socket:  "cc-new-fixture-1",
		Name:    "booting…",
		Project: "booting-project",
	}
	snapshot := Snapshot{
		Rows:           []compose.Row{row},
		View:           compose.DefaultView,
		PrimaryAccount: 1,
		NowNS:          fixtureNowNS,
	}
	model := NewModel(snapshot)

	model, command := applyKey(t, model, controlKey('x'))
	if command != nil || len(model.Result().KillChanges) != 0 || model.rows[0].Killed {
		t.Fatalf(
			"⌃X on a booting row was not a no-op: command=%v result=%#v",
			command,
			model.Result(),
		)
	}

	reboot, rebootCommand := applyKey(t, model, controlKey('o'))
	if rebootCommand != nil || reboot.Result().Kind != OutcomeNone {
		t.Fatalf("⌃O on a booting row was not a no-op: %#v", reboot.Result())
	}

	selected, selectCommand := applyKey(t, model, specialKey(tea.KeyEnter))
	result := selected.Result()
	if selectCommand == nil ||
		result.Kind != OutcomeSelected ||
		compose.RowKey(result.Row) != compose.RowKey(row) {
		t.Fatalf("Enter on a booting row = command=%v result=%#v", selectCommand, result)
	}
}

func TestAccountsOffTheRosterFallBackToTheFirst(t *testing.T) {
	// A stale ~/.claude-primary naming an off-roster account must open the
	// picker on account 1, not on an account no launcher
	// can reach.
	for _, account := range []int{0, -1, 4, 9} {
		snapshot := fixtureSnapshot(120)
		snapshot.PrimaryAccount = account
		if got := NewModel(snapshot).PrimaryAccount(); got != 1 {
			t.Fatalf("account %d became %d, want 1", account, got)
		}
	}
	for account := 1; account <= 3; account++ {
		snapshot := fixtureSnapshot(120)
		snapshot.PrimaryAccount = account
		if got := NewModel(snapshot).PrimaryAccount(); got != account {
			t.Fatalf("account %d became %d", account, got)
		}
	}
}

func TestValidAccountUsesRosterOnly(t *testing.T) {
	if got := validAccount(3, nil); got != 0 {
		t.Fatalf("validAccount(3, nil) = %d, want 0", got)
	}
	if got := validAccount(3, []int{1, 2}); got != 1 {
		t.Fatalf("validAccount(3, [1 2]) = %d, want 1", got)
	}
}

func TestFuzzyFilterAndRefreshCursorFollow(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.InitialCursorID = snapshot.Rows[3].ID
	model := NewModel(snapshot)
	for _, runeValue := range "needle" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	rows := model.VisibleRows()
	if model.Query() != "needle" ||
		len(rows) != 1 ||
		rows[0].ID != snapshot.Rows[3].ID ||
		!model.HasVisibleSelection() {
		t.Fatalf("query=%q rows=%#v cursor=%d", model.Query(), rows, model.Cursor())
	}

	refresh := snapshot
	refresh.Rows = append([]compose.Row(nil), snapshot.Rows...)
	refresh.Rows[0], refresh.Rows[3] = refresh.Rows[3], refresh.Rows[0]
	updated, command := model.Update(RefreshMsg{Snapshot: refresh})
	if command != nil {
		t.Fatalf("refresh command=%v", command)
	}
	model = updated.(Model)
	if model.SelectedKey() != snapshot.Rows[3].ID {
		t.Fatalf("refresh cursor moved to %q", model.SelectedKey())
	}

	model = NewModel(snapshot)
	for _, runeValue := range "agndl" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	rows = model.VisibleRows()
	if len(rows) != 1 || rows[0].ID != snapshot.Rows[3].ID {
		t.Fatalf("non-contiguous fuzzy query rows=%#v", rows)
	}
}

func TestDefaultKillRemovesRowAndKeepsValidCursor(t *testing.T) {
	snapshot := fixtureSnapshot(80)
	snapshot.View = compose.DefaultView
	snapshot.Rows = snapshot.Rows[:3]
	snapshot.InitialCursorID = snapshot.Rows[1].ID
	model := NewModel(snapshot)
	model, _ = applyKey(t, model, controlKey('x'))
	if len(model.VisibleRows()) != 2 ||
		!model.HasVisibleSelection() ||
		model.SelectedKey() == snapshot.Rows[1].ID {
		t.Fatalf(
			"rows=%d cursor=%d selected=%q",
			len(model.VisibleRows()),
			model.Cursor(),
			model.SelectedKey(),
		)
	}
}

func TestTabsDefaultToChatsAndTabKeysWrap(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	if model.Tab() != TabChats {
		t.Fatalf("initial tab = %d, want Chats", model.Tab())
	}
	selected := model.SelectedKey()
	model, command := applyKey(t, model, specialKey(tea.KeyTab))
	if command != nil || model.Tab() != TabStats || model.SelectedKey() != selected {
		t.Fatalf("tab: tab=%d selected=%q command=%v", model.Tab(), model.SelectedKey(), command)
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	if model.Tab() != TabLimits {
		t.Fatalf("tab: tab=%d, want Limits", model.Tab())
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	if model.Tab() != TabCosmos {
		t.Fatalf("tab: tab=%d, want Cosmos", model.Tab())
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	if model.Tab() != TabChats {
		t.Fatalf("tab wrap = %d, want Chats", model.Tab())
	}
	model, _ = applyKey(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.Tab() != TabCosmos {
		t.Fatalf("shift+tab wrap = %d, want Cosmos", model.Tab())
	}
}

func TestEnteringStatsTabDefaultsFocusToSubtabs(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	if model.statsFocus != StatsFocusSubtab {
		t.Fatalf("entering Stats statsFocus = %d, want StatsFocusSubtab", model.statsFocus)
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyRight))
	if model.Tab() != TabStats {
		t.Fatalf("right from subtab focus left Stats: tab=%d, want TabStats", model.Tab())
	}
	if model.statsSubtab != StatsDocker {
		t.Fatalf("right from subtab focus statsSubtab = %d, want StatsDocker", model.statsSubtab)
	}
}

func TestColonNameGroupsClusterInsideProject(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.Rows = []compose.Row{
		{Kind: compose.LiveClaude, ID: "b1", Name: "BUILDER:1", Project: "alpha", ActivityNS: 100},
		{Kind: compose.LiveClaude, ID: "b2", Name: "BUILDER:2", Project: "alpha", ActivityNS: 90},
		{Kind: compose.LiveCodex, ID: "flat", Name: "fix: the bug", Project: "alpha", ActivityNS: 80},
		{Kind: compose.LiveClaude, ID: "b3", Name: "BUILDER:3", Project: "alpha", ActivityNS: 70},
	}
	model := NewModel(snapshot)
	rows := model.VisibleRows()
	want := []string{"BUILDER:1", "BUILDER:2", "BUILDER:3", "fix: the bug"}
	if len(rows) != len(want) {
		t.Fatalf("visible rows = %#v", rows)
	}
	for index := range want {
		if rows[index].Name != want[index] {
			t.Fatalf("row %d = %q, want %q", index, rows[index].Name, want[index])
		}
	}
	plain := ansi.Strip(model.View().Content)
	if strings.Count(plain, "BUILDER (3)") != 1 || strings.Contains(plain, "fix (1)") {
		t.Fatalf("group rendering:\n%s", plain)
	}
}

func TestColonNameGroupsClusterAcrossProjects(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.Rows = []compose.Row{
		{Kind: compose.LiveClaude, ID: "orch", Name: "P:CCC", Project: "professor", ActivityNS: 100},
		{Kind: compose.LiveCodex, ID: "builder", Name: "P:BUILDER", Project: "limits-own-tab", ActivityNS: 90},
	}
	model := NewModel(snapshot)
	rows := model.VisibleRows()
	want := []string{"P:CCC", "P:BUILDER"}
	if len(rows) != len(want) {
		t.Fatalf("visible rows = %#v", rows)
	}
	for index := range want {
		if rows[index].Name != want[index] {
			t.Fatalf("row %d = %q, want %q", index, rows[index].Name, want[index])
		}
	}
	plain := ansi.Strip(model.View().Content)
	if strings.Count(plain, "P (2)") != 1 {
		t.Fatalf("group rendering did not fold across projects:\n%s", plain)
	}
}

func applyKey(
	t *testing.T,
	model Model,
	message tea.KeyMsg,
) (Model, tea.Cmd) {
	t.Helper()
	updated, command := model.Update(message)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return result, command
}

func controlKey(runeValue rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: runeValue, Mod: tea.ModCtrl})
}

func printableKey(runeValue rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{
		Code: runeValue,
		Text: string(runeValue),
	})
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

// TestControlXAlwaysLeavesAReceipt is the no-silent-keystroke contract: every
// ⌃X ends with killStatus naming what landed or why it was refused. A swallowed
// ⌃X looks identical to a kill that landed, and the row's return on the next
// open then reads as the store losing the order.
func TestControlXAlwaysLeavesAReceipt(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Snapshot)
		receipt string
		// seek moves the cursor to the first row matching it — for rows whose
		// mutation erases the id the cursor-follow would otherwise target.
		seek func(compose.Row) bool
	}{
		{
			name: "booting row is refused by name",
			mutate: func(snapshot *Snapshot) {
				snapshot.Rows[1].Kind = compose.Booting
				snapshot.Rows[1].Name = "boot-probe"
			},
			receipt: "still booting",
		},
		{
			// The Kind arm matches before the empty-ID arm, so the cursor can
			// still follow the fixture id while the refusal under test fires.
			name: "split live window is refused",
			mutate: func(snapshot *Snapshot) {
				snapshot.Rows[1].Kind = compose.LiveSplit
			},
			receipt: "split live window",
		},
		{
			name: "label-killed row is refused",
			mutate: func(snapshot *Snapshot) {
				snapshot.Rows[1].NameKilled = true
			},
			receipt: "_KILL label",
		},
		{
			name: "identityless row is refused",
			mutate: func(snapshot *Snapshot) {
				snapshot.Rows[1].ID = ""
				snapshot.Rows[1].Kind = compose.NewClaude
			},
			receipt: "not a chat yet",
			seek: func(row compose.Row) bool {
				return row.ID == "" && row.Kind == compose.NewClaude
			},
		},
		{
			name: "nil applier refuses instead of painting a cosmetic kill",
			mutate: func(snapshot *Snapshot) {
				snapshot.ApplyKill = nil
			},
			receipt: "cannot write kills",
		},
		{
			name: "failed store write names the chat and the error",
			mutate: func(snapshot *Snapshot) {
				snapshot.ApplyKill = func(KillChange) error {
					return errors.New("store is sealed")
				}
			},
			receipt: "store is sealed",
		},
		{
			name:    "landed kill prints its receipt",
			mutate:  func(_ *Snapshot) {},
			receipt: "hidden — ",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := fixtureSnapshot(120)
			snapshot.InitialCursorID = snapshot.Rows[1].ID
			testCase.mutate(&snapshot)
			model := NewModel(snapshot)
			if testCase.seek != nil {
				for step := 0; step < len(model.filtered); step++ {
					if testCase.seek(model.rows[model.filtered[model.cursor]]) {
						break
					}
					model, _ = applyKey(t, model, specialKey(tea.KeyDown))
				}
				if !testCase.seek(model.rows[model.filtered[model.cursor]]) {
					t.Fatal("seek never reached the row under test")
				}
			}
			model, _ = applyKey(t, model, controlKey('x'))
			if model.killStatus == "" {
				t.Fatal("⌃X left no receipt — the keystroke went silent")
			}
			if !strings.Contains(model.killStatus, testCase.receipt) {
				t.Fatalf(
					"receipt = %q, want it to mention %q",
					model.killStatus,
					testCase.receipt,
				)
			}
		})
	}
}

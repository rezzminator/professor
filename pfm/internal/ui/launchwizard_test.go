package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// wizardSnapshot is a picker with a ✦ row per engine that has accounts.
func wizardSnapshot(claude, codex, opencode []int) Snapshot {
	snapshot := Snapshot{
		MergeNewChat: true, NowNS: fixtureNowNS, Width: 120, Height: 30,
		AccountIDs: claude, CodexAccountIDs: codex, OpenCodeAccountIDs: opencode,
		Cache1H: true, Cache1HByAccount: map[int]bool{},
	}
	if len(claude) > 0 {
		snapshot.PrimaryAccount = claude[0]
		snapshot.Rows = append(
			snapshot.Rows,
			compose.Row{Kind: compose.NewClaude, Name: "New Claude chat", Project: "p", CWD: "/p"},
		)
		for _, account := range claude {
			snapshot.Cache1HByAccount[account] = true
		}
	}
	if len(codex) > 0 {
		snapshot.CodexPrimaryAccount = codex[0]
		snapshot.Rows = append(
			snapshot.Rows,
			compose.Row{Kind: compose.NewCodex, Name: "New Codex chat", Project: "p", CWD: "/p"},
		)
	}
	if len(opencode) > 0 {
		snapshot.OpenCodePrimaryAccount = opencode[0]
		snapshot.Rows = append(
			snapshot.Rows,
			compose.Row{Kind: compose.NewOpenCode, Name: "New OpenCode chat", Project: "p", CWD: "/p"},
		)
	}
	return snapshot
}

var wizardKeys = map[string]tea.KeyPressMsg{
	"enter": specialKey(tea.KeyEnter), "right": specialKey(tea.KeyRight),
	"left": specialKey(tea.KeyLeft), "esc": specialKey(tea.KeyEscape),
}

func walk(t *testing.T, model Model, keys ...string) Model {
	t.Helper()
	for _, key := range keys {
		model, _ = applyKey(t, model, wizardKeys[key])
	}
	return model
}

// enterThrough presses ⏎ on the selected row, then on every step it opens,
// each on its default; walked reports that a walk opened.
func enterThrough(t *testing.T, model Model) (next Model, command tea.Cmd, walked bool) {
	t.Helper()
	next, command = applyKey(t, model, wizardKeys["enter"])
	walked = next.wizard.on
	for steps := 0; command == nil && next.wizard.on && steps <= int(wizardCache); steps++ {
		next, command = applyKey(t, next, wizardKeys["enter"])
	}
	return next, command, walked
}

func TestWizardPaths(t *testing.T) {
	on, off := true, false
	for _, test := range []struct {
		name     string
		snapshot Snapshot
		keys     []string
		engine   pfmengine.ID
		kind     compose.Kind
		account  int
		cache    *bool // nil: Claude's cache step not asked
	}{
		{
			name:     "three engines: enter, enter on the default Claude, enter on its 1h cache",
			snapshot: wizardSnapshot([]int{1}, []int{3}, []int{5}),
			keys:     []string{"enter", "enter", "enter"},
			engine:   pfmengine.Claude, kind: compose.NewClaude, account: 1, cache: &on,
		},
		{
			name:     "right picks Codex; one account and no cache option launches at once",
			snapshot: wizardSnapshot([]int{1}, []int{3}, []int{5}),
			keys:     []string{"enter", "right", "enter"},
			engine:   pfmengine.Codex, kind: compose.NewCodex, account: 3,
		},
		{
			name:     "left wraps to OpenCode",
			snapshot: wizardSnapshot([]int{1}, []int{3}, []int{5}),
			keys:     []string{"enter", "left", "enter"},
			engine:   pfmengine.OpenCode, kind: compose.NewOpenCode, account: 5,
		},
		{
			name:     "one engine, one account, no cache: enter starts the chat",
			snapshot: wizardSnapshot(nil, []int{3}, nil),
			keys:     []string{"enter"},
			engine:   pfmengine.Codex, kind: compose.NewCodex, account: 3,
		},
		{
			name:     "Claude only: the engine step is skipped, two accounts ask, then the cache",
			snapshot: wizardSnapshot([]int{1, 2}, nil, nil),
			keys:     []string{"enter", "right", "enter", "right", "enter"},
			engine:   pfmengine.Claude, kind: compose.NewClaude, account: 2, cache: &off,
		},
		{
			name:     "Codex with two accounts asks the account and never the cache",
			snapshot: wizardSnapshot(nil, []int{3, 4}, nil),
			keys:     []string{"enter", "right", "enter"},
			engine:   pfmengine.Codex, kind: compose.NewCodex, account: 4,
		},
		{
			name:     "engine then account then cache, each confirmed",
			snapshot: wizardSnapshot([]int{1, 2}, []int{3}, nil),
			keys:     []string{"enter", "enter", "right", "enter", "enter"},
			engine:   pfmengine.Claude, kind: compose.NewClaude, account: 2, cache: &on,
		},
		{
			name:     "going back to the engine step and choosing another resets the account",
			snapshot: wizardSnapshot([]int{1, 2}, []int{3, 4}, nil),
			keys:     []string{"enter", "enter", "right", "esc", "right", "enter", "enter"},
			engine:   pfmengine.Codex, kind: compose.NewCodex, account: 3,
		},
		{
			name:     "going back keeps the account chosen for the same engine",
			snapshot: wizardSnapshot([]int{1, 2}, []int{3}, nil),
			keys:     []string{"enter", "enter", "right", "enter", "esc", "esc", "enter", "enter", "enter"},
			engine:   pfmengine.Claude, kind: compose.NewClaude, account: 2, cache: &on,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := walk(t, NewModel(test.snapshot), test.keys...)
			result := model.Result()
			if result.Kind != OutcomeSelected || result.Row.Kind != test.kind ||
				result.PrimaryAccount != test.account ||
				model.wizard.on {
				t.Fatalf(
					"result %#v (wizard on %t), want %s account %d",
					result,
					model.wizard.on,
					test.kind,
					test.account,
				)
			}
			if test.cache != nil && result.Cache1H != *test.cache {
				t.Fatalf("cache1H = %t, want %t", result.Cache1H, *test.cache)
			}
		})
	}
}

func TestWizardStepsAndEscBack(t *testing.T) {
	model := NewModel(wizardSnapshot([]int{1, 2}, []int{3}, nil))
	steps := []struct {
		key      string
		step     wizardStep
		on, quit bool
	}{
		{"enter", wizardEngine, true, false},
		{"enter", wizardAccount, true, false},
		{"enter", wizardCache, true, false},
		{"esc", wizardAccount, true, false},
		{"esc", wizardEngine, true, false},
		{"esc", 0, false, false},
	}
	for index, step := range steps {
		var command tea.Cmd
		model, command = applyKey(t, model, wizardKeys[step.key])
		if model.wizard.on != step.on || (step.on && model.wizard.step != step.step) || (command != nil) != step.quit {
			t.Fatalf("key %d (%s): wizard on %t step %d quit %v, want on %t step %d quit %v",
				index, step.key, model.wizard.on, model.wizard.step, command != nil, step.on, step.step, step.quit)
		}
	}
	// With the wizard closed, esc is the picker's own again.
	cancelled, command := applyKey(t, model, wizardKeys["esc"])
	if command == nil || cancelled.Result().Kind != OutcomeCancelled {
		t.Fatalf("esc after the wizard closed = %#v, want the picker cancelled", cancelled.Result())
	}
}

func TestWizardStepsOpenOnTheLastLaunch(t *testing.T) {
	snapshot := wizardSnapshot([]int{1, 2}, []int{3, 4}, []int{5})
	snapshot.PrimaryAccount = 2
	snapshot.LastLaunch = fleetdb.LaunchMemory{
		Engine:   string(pfmengine.Codex),
		Accounts: map[string]int{string(pfmengine.Codex): 4, string(pfmengine.OpenCode): 5},
		Cache1H:  map[int]bool{2: false},
	}
	model := NewModel(snapshot)
	model = walk(t, model, "enter")
	if got := model.wizard.engine; got != pfmengine.Codex || model.wizard.pos != 0 {
		t.Fatalf("engine step opens on %q at chip %d, want the last launch's Codex first", got, model.wizard.pos)
	}
	model = walk(t, model, "enter")
	if got := model.orderedChoices(model.rows[0], model.wizard)[model.wizard.pos].account; got != 4 ||
		model.wizard.pos != 0 {
		t.Fatalf("Codex account step opens on %d at chip %d, want the last launch's 4 first", got, model.wizard.pos)
	}
	// The chips read Codex, Claude, OpenCode: the remembered engine leads.
	model = walk(t, model, "esc", "right", "enter")
	if got := model.orderedChoices(model.rows[0], model.wizard)[model.wizard.pos].account; got != 2 {
		t.Fatalf("Claude account step opens on %d, want the primary account 2", got)
	}
	model = walk(t, model, "enter")
	if model.wizard.step != wizardCache || model.wizard.pos != 0 ||
		model.orderedChoices(model.rows[0], model.wizard)[0].cache {
		t.Fatalf(
			"cache step %d opens on chip %d, want account 2's remembered 5m first",
			model.wizard.step,
			model.wizard.pos,
		)
	}
	// An engine the row cannot launch, and an account off the roster, are not defaults.
	snapshot.LastLaunch = fleetdb.LaunchMemory{
		Engine: string(pfmengine.OpenCode), Accounts: map[string]int{string(pfmengine.Codex): 99},
	}
	snapshot.Rows = snapshot.Rows[:2]
	snapshot.OpenCodeAccountIDs = nil
	model = walk(t, NewModel(snapshot), "enter", "right", "enter")
	if got := model.orderedChoices(model.rows[0], model.wizard)[model.wizard.pos].account; got != 3 {
		t.Fatalf("unknown remembered account opened on %d, want the roster's first (3)", got)
	}
}

func TestWizardFallsBackToTheConfiguredCache(t *testing.T) {
	snapshot := wizardSnapshot([]int{1, 2}, nil, nil)
	snapshot.Cache1HByAccount = map[int]bool{1: true, 2: false}
	model := walk(t, NewModel(snapshot), "enter", "right", "enter")
	if model.wizard.step != wizardCache || model.wizard.pos != 0 ||
		model.orderedChoices(model.rows[0], model.wizard)[0].cache {
		t.Fatalf("account 2 cache opens on chip %d, want its configured 5m first", model.wizard.pos)
	}
}

func TestWizardCancelLeavesNoChoice(t *testing.T) {
	model := walk(t, NewModel(wizardSnapshot([]int{1, 2}, []int{3}, nil)), "enter", "enter", "right", "enter")
	if !model.wizard.on || model.wizard.step != wizardCache || model.wizard.account != 2 {
		t.Fatalf(
			"setup: wizard on %t step %d account %d, want the cache step of account 2",
			model.wizard.on,
			model.wizard.step,
			model.wizard.account,
		)
	}
	model = walk(t, model, "esc", "esc", "esc")
	model, command := applyKey(t, model, wizardKeys["esc"])
	result := model.Result()
	if command == nil || result.Kind != OutcomeCancelled || result.PrimaryAccount != 1 ||
		!reflect.DeepEqual(result.Row, compose.Row{}) {
		t.Fatalf("cancelled after a walk = %#v, want Cancelled on the opening account with no row", result)
	}
}

func TestWizardClosesOnAnyOtherKey(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{specialKey(tea.KeyDown), specialKey(tea.KeyUp), printableKey('a'), specialKey(tea.KeyTab)} {
		model := walk(t, NewModel(wizardSnapshot([]int{1, 2}, []int{3}, nil)), "enter", "right")
		if !model.wizard.on || model.wizard.engine != pfmengine.Codex {
			t.Fatalf(
				"setup: wizard on %t engine %q, want the engine step on Codex",
				model.wizard.on,
				model.wizard.engine,
			)
		}
		model, _ = applyKey(t, model, key)
		if model.wizard.on {
			t.Fatalf("%q left the wizard open", key.String())
		}
	}
}

func TestWizardRowsSayWhichStepAndWhatIsChosen(t *testing.T) {
	model := NewModel(wizardSnapshot([]int{1, 2}, []int{3}, nil))
	row, _ := model.selectedRow()
	idle := ansi.Strip(model.renderGroupedRow(row, true, 100, false))
	if !strings.Contains(idle, "New chat → p  Claude · ") || strings.Contains(idle, "◖") {
		t.Errorf("idle ✦ row = %q, want the place and the remembered choice, no chips", idle)
	}
	model = walk(t, model, "enter")
	engineStep := ansi.Strip(model.renderGroupedRow(row, true, 100, false))
	if !strings.Contains(engineStep, "◖ Claude ◗") || !strings.Contains(engineStep, "[ Codex ]") {
		t.Errorf("engine step = %q, want Claude boxed and Codex bracketed", engineStep)
	}
	if footer := ansi.Strip(model.renderFooter(120)); !strings.Contains(footer, "new chat · step 1 of 3 · engine") ||
		!strings.Contains(footer, "choose") || !strings.Contains(footer, "back") {
		t.Errorf("footer = %q, want the step and the wizard's keys", footer)
	}
	model = walk(t, model, "enter")
	accountStep := ansi.Strip(model.renderGroupedRow(row, true, 100, false))
	if !strings.Contains(accountStep, "Claude ›") || !strings.Contains(accountStep, "◖ "+accountMedal(1)+" 1 ◗") ||
		!strings.Contains(accountStep, "[ "+accountMedal(2)+" 2 ]") {
		t.Errorf("account step = %q, want the engine chosen and the account chips", accountStep)
	}
	if footer := ansi.Strip(model.renderFooter(120)); !strings.Contains(footer, "step 2 of 3 · account") {
		t.Errorf("footer = %q, want step 2 of 3", footer)
	}
	if dossier := strings.Join(dossierText(model, row), "\n"); !strings.Contains(dossier, "Step 2 of 3") {
		t.Errorf("dossier = %q, want the step", dossier)
	}
	model = walk(t, model, "enter")
	cacheStep := ansi.Strip(model.renderGroupedRow(row, true, 100, false))
	if !strings.Contains(cacheStep, "◖ 1h ◗") || !strings.Contains(cacheStep, "[ 5m ]") ||
		!strings.Contains(cacheStep, "Claude · "+accountMedal(1)+" 1 ›") {
		t.Errorf("cache step = %q, want the choices so far and the 1h/5m chips", cacheStep)
	}
	if footer := ansi.Strip(model.renderFooter(120)); !strings.Contains(footer, "start") {
		t.Errorf("last step footer = %q, want ⏎ to say start", footer)
	}
	narrow := ansi.Strip(model.renderGroupedRow(row, true, 30, false))
	if ansi.StringWidth(narrow) != 30 {
		t.Errorf("a cramped wizard row = %q (%d cells), want it fitted to 30", narrow, ansi.StringWidth(narrow))
	}
}

func dossierText(model Model, row compose.Row) []string {
	lines := model.dossierLines(row, 40, 20)
	text := make([]string, 0, len(lines))
	for _, line := range lines {
		plain := ""
		for _, part := range line {
			plain += part.text
		}
		text = append(text, plain)
	}
	return text
}

// A launch memory that could not be read says so on the ✦ row instead of
// reading as "nothing launched yet".
func TestUnreadableLaunchMemoryIsNamedOnTheLaunchRow(t *testing.T) {
	snapshot := wizardSnapshot([]int{1, 2}, []int{3}, nil)
	snapshot.LaunchMemoryError = "parse launch memory: bad row"
	model := NewModel(snapshot)
	row, _ := model.selectedRow()
	idle := ansi.Strip(model.renderGroupedRow(row, true, 100, false))
	if !strings.Contains(idle, "last launch unreadable") {
		t.Errorf("idle ✦ row = %q, want the unreadable memory named", idle)
	}
	dossier := strings.Join(strings.Fields(strings.Join(dossierText(model, row), " ")), " ")
	if !strings.Contains(dossier, "parse launch memory: bad row") {
		t.Errorf("dossier = %q, want the read failure", dossier)
	}
}

func TestWizardOpensOnTheDefaultCacheOfAnUnnamedAccount(t *testing.T) {
	snapshot := wizardSnapshot([]int{1}, nil, nil)
	snapshot.AccountIDs, snapshot.PrimaryAccount = nil, 0
	snapshot.Cache1H, snapshot.Cache1HByAccount = true, map[int]bool{}
	model := walk(t, NewModel(snapshot), "enter")
	if model.wizard.step != wizardCache || model.wizard.account != 0 ||
		!model.orderedChoices(model.rows[0], model.wizard)[model.wizard.pos].cache {
		t.Fatalf("step %v account %d pos %d, want the cache step on unnamed account 0 opening on 1h",
			model.wizard.step, model.wizard.account, model.wizard.pos)
	}
}

// resumeSnapshot is a picker on one chat row, with two Claude and the given
// Codex accounts configured; the configured cache is 1h for every account.
func resumeSnapshot(row compose.Row, codex []int) Snapshot {
	snapshot := wizardSnapshot([]int{1, 2}, codex, nil)
	snapshot.Rows = []compose.Row{row}
	return snapshot
}

func TestResumeWalkPaths(t *testing.T) {
	claude := compose.Row{
		Kind: compose.ResumeClaude, ID: "resume-claude", Name: "port", Project: "p", CWD: "/p",
		Account: 2, C1H: false,
	}
	offRoster := claude
	offRoster.Account = 9
	codex := compose.Row{Kind: compose.ResumeCodex, ID: "resume-codex", Name: "port", Project: "p", CWD: "/p"}
	live := claude
	live.Kind = compose.LiveClaude
	native := live
	native.Backend = spawn.BackendHerdr
	nativeGone := claude
	nativeGone.Backend = spawn.BackendHerdr
	for _, test := range []struct {
		name  string
		row   compose.Row
		codex []int
		keys  []string
		want  *ResumeChoice // nil: selected at once, no walk
	}{
		{
			name: "enter through resumes on the chat's recorded account and cache",
			row:  claude, keys: []string{"enter", "enter", "enter"},
			want: &ResumeChoice{Account: 2, Cache1H: false},
		},
		{
			name: "a recorded account off the roster opens on the primary account",
			row:  offRoster, keys: []string{"enter", "enter", "enter"},
			want: &ResumeChoice{Account: 1, Cache1H: true},
		},
		{
			name: "another account and cache chosen reach the outcome",
			row:  claude, keys: []string{"enter", "right", "enter", "right", "enter"},
			want: &ResumeChoice{Account: 1, Cache1H: false},
		},
		{
			name: "back to the recorded account restores the chat's recorded cache",
			row:  claude, keys: []string{"enter", "right", "enter", "esc", "left", "enter", "enter"},
			want: &ResumeChoice{Account: 2, Cache1H: false},
		},
		{
			name: "Codex asks the account and never the cache",
			row:  codex, codex: []int{3, 4}, keys: []string{"enter", "right", "enter"},
			want: &ResumeChoice{Account: 4, Cache1H: true},
		},
		{
			name: "Codex with one account resumes at once",
			row:  codex, codex: []int{3}, keys: []string{"enter"},
		},
		{
			name: "a live chat attaches at once",
			row:  live, keys: []string{"enter"},
		},
		{
			name: "a live native seat's row opens at once",
			row:  native, keys: []string{"enter"},
		},
		{
			name: "a native seat that is gone walks the resume steps",
			row:  nativeGone, keys: []string{"enter", "right", "enter", "right", "enter"},
			want: &ResumeChoice{Account: 1, Cache1H: false},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := walk(t, NewModel(resumeSnapshot(test.row, test.codex)), test.keys...)
			result := model.Result()
			if result.Kind != OutcomeSelected || result.Row.ID != test.row.ID || result.Row.Kind != test.row.Kind ||
				result.ClaudePrimaryAccount != 1 || !reflect.DeepEqual(result.Resume, test.want) || model.wizard.on {
				t.Fatalf("result %#v resume %+v (wizard on %t), want %s %s selected with resume %+v and primary 1",
					result, result.Resume, model.wizard.on, test.row.Kind, test.row.ID, test.want)
			}
		})
	}
}

func TestResumeWalkShowsItsEngineAndEscLaunchesNothing(t *testing.T) {
	row := compose.Row{
		Kind: compose.ResumeClaude, ID: "resume-claude", Name: "port", Project: "p", CWD: "/p",
		Account: 2, C1H: false,
	}
	model := walk(t, NewModel(resumeSnapshot(row, []int{3})), "enter")
	if !model.wizard.on || model.wizard.step != wizardAccount || model.wizard.engine != pfmengine.Claude {
		t.Fatalf("enter on a resumable chat: wizard on %t step %d engine %q, want the account step of Claude",
			model.wizard.on, model.wizard.step, model.wizard.engine)
	}
	shown := ansi.Strip(model.renderGroupedRow(row, true, 120, false))
	if !strings.Contains(shown, "Resume Claude › ◖ "+accountMedal(2)+" 2 ◗ ") || strings.Contains(shown, "Codex") {
		t.Errorf("account step row = %q, want the fixed engine and the recorded account boxed", shown)
	}
	if footer := ansi.Strip(model.renderFooter(120)); !strings.Contains(footer, "resume · step 1 of 2 · account") {
		t.Errorf("footer = %q, want the resume step", footer)
	}
	model = walk(t, model, "enter")
	if shown := ansi.Strip(model.renderGroupedRow(row, true, 120, false)); !strings.Contains(
		shown, "Resume Claude · "+accountMedal(2)+" 2 › ◖ 5m ◗ ") {
		t.Errorf("cache step row = %q, want the recorded 5m boxed", shown)
	}
	model = walk(t, model, "esc", "esc")
	if model.wizard.on || model.Result().Kind != OutcomeNone {
		t.Fatalf("esc out of the walk: wizard on %t outcome %d, want it closed and nothing launched",
			model.wizard.on, model.Result().Kind)
	}
	cancelled, command := applyKey(t, model, wizardKeys["esc"])
	if result := cancelled.Result(); command == nil || result.Kind != OutcomeCancelled || result.Resume != nil {
		t.Fatalf("esc after the walk closed = %#v, want the picker cancelled with no resume", result)
	}
}

// A chat resumed elsewhere mid-walk turns live under the same id: the walk no
// longer draws on its row, and ⏎ attaches it.
func TestResumeWalkLeavesAChatTurnedLive(t *testing.T) {
	row := compose.Row{
		Kind: compose.ResumeClaude, ID: "resume-claude", Name: "port", Project: "p", CWD: "/p",
		Account: 2, C1H: false,
	}
	model := walk(t, NewModel(resumeSnapshot(row, nil)), "enter")
	if shown := ansi.Strip(model.renderGroupedRow(row, true, 120, false)); !strings.Contains(shown, "Resume Claude") {
		t.Fatalf("account step row = %q, want the resume walk drawn", shown)
	}
	live := row
	live.Kind = compose.LiveClaude
	next, _ := model.Update(RefreshMsg{Snapshot: resumeSnapshot(live, nil)})
	model = next.(Model)
	if shown := ansi.Strip(model.renderGroupedRow(live, true, 120, false)); strings.Contains(shown, "Resume Claude") {
		t.Errorf("live row = %q, want the walk gone from it", shown)
	}
	model = walk(t, model, "enter")
	if result := model.Result(); result.Kind != OutcomeSelected || result.Row.Kind != compose.LiveClaude ||
		result.Resume != nil {
		t.Fatalf("enter on the chat turned live = %#v, want it selected live with no resume", result)
	}
}

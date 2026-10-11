package ui

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const (
	scribeWorkbench   = "/work/acme/docs/scribe"
	labWorkbenchFault = "/work/acme/.professor/lab/.professor/workbench.json: \"prompt\" is required"
)

func workbenchGoldenModel(width int) Model {
	snapshot := fixtureSnapshot(width)
	snapshot.Height = 28
	snapshot.KilledCount = 0
	snapshot.MergeNewChat = true
	snapshot.Rows = []compose.Row{
		{Kind: compose.NewClaude, Name: "New Claude chat", Project: "~"},
		{Kind: compose.NewCodex, Name: "New Codex chat", Project: "~"},
		{Kind: compose.ResumeClaude, ID: "a", Name: "A", Project: "acme", CWD: "/work/acme"},
		{Kind: compose.ResumeClaude, ID: "scribe9", Name: "_SCRIBE:9", Project: "acme", CWD: "/work/acme"},
		{
			Kind: compose.NewCodex, Name: "New Codex chat", Project: "acme › Scribe", CWD: scribeWorkbench,
			Workbench: scribeWorkbench, Engines: []pfmengine.ID{pfmengine.Codex, pfmengine.Claude},
		},
		{
			Kind: compose.ResumeCodex, ID: "scribe1", Name: "_SCRIBE:1", Project: "acme › Scribe",
			CWD: scribeWorkbench, Workbench: scribeWorkbench,
		},
		{
			Kind: compose.ResumeClaude, ID: "scribe2", Name: "_SCRIBE:2", Project: "acme › Scribe",
			CWD: scribeWorkbench, Workbench: scribeWorkbench,
		},
		{
			Kind: compose.WorkbenchInvalid, Name: labWorkbenchFault, Project: "acme › lab",
			CWD: "/work/acme/.professor/lab", Workbench: "/work/acme/.professor/lab",
		},
	}
	return NewModel(snapshot)
}

// Isolate the selected-row handlers from rebuildOrder, tested separately below.
func workbenchSelectedModel(index int) Model {
	model := workbenchGoldenModel(120)
	model.filtered = []int{index}
	model.cursor = 0
	return model
}

func TestWorkbenchLaunchRowsKeepGroupPosition(t *testing.T) {
	model := workbenchGoldenModel(80)
	rows := model.VisibleRows()
	if len(rows) != 7 || rows[0].Kind != compose.NewClaude || rows[0].Workbench != "" ||
		rows[3].Workbench != scribeWorkbench || rows[3].Kind != compose.NewCodex {
		t.Fatalf("workbench launch row order = %#v", rows)
	}
	lines := strings.Split(ansi.Strip(model.View().Content), "\n")
	for index, line := range lines {
		if strings.Contains(line, "╭─ acme › Scribe") {
			if index+1 == len(lines) || !strings.Contains(lines[index+1], "✦") {
				t.Fatalf("workbench header is not followed by its launch row:\n%s", strings.Join(lines, "\n"))
			}
			return
		}
	}
	t.Fatalf("missing workbench header:\n%s", strings.Join(lines, "\n"))
}

func TestWorkbenchTopLaunchRowArrowsDoNothing(t *testing.T) {
	model := workbenchGoldenModel(80)
	place := model.place
	model, _ = applyKey(t, model, specialKey(tea.KeyRight))
	if model.newChatEngine != pfmengine.Claude || model.place != place || model.wizard.on {
		t.Fatalf(
			"→ on the launch row = engine %q place %q wizard %t, want nothing moved",
			model.newChatEngine,
			model.place,
			model.wizard.on,
		)
	}
	// A bench-only engine must not enlarge the fleet launcher's roster.
	model.rows[4].Kind = compose.NewOpenCode
	model.rows[4].Engines = []pfmengine.ID{pfmengine.OpenCode}
	if got := model.newChatEngines(); !reflect.DeepEqual(got, []pfmengine.ID{pfmengine.Claude, pfmengine.Codex}) {
		t.Fatalf("top engines = %v, want [cc cx]", got)
	}
}

func TestWorkbenchLaunchRowOffersOnlyItsOwnEngines(t *testing.T) {
	for _, test := range []struct {
		remembered pfmengine.ID
		label      string
	}{
		{pfmengine.Claude, "New chat → acme › Scribe  Claude"},
		{pfmengine.Codex, "New chat → acme › Scribe  Codex"},
		{pfmengine.OpenCode, "New chat → acme › Scribe  Claude"}, // the bench has no OpenCode: the configured default stands in
	} {
		t.Run(string(test.remembered), func(t *testing.T) {
			model := workbenchSelectedModel(4)
			model.remembered.Engine = string(test.remembered)
			row, _ := model.selectedRow()
			plain := ansi.Strip(model.renderGroupedRow(row, true, 120, false))
			if !strings.Contains(plain, test.label) {
				t.Errorf("workbench launch row = %q, want %q", plain, test.label)
			}
			model, _ = applyKey(t, model, specialKey(tea.KeyEnter))
			chips := ansi.Strip(model.renderGroupedRow(row, true, 120, false))
			if !strings.Contains(chips, "Codex") || !strings.Contains(chips, "Claude") ||
				strings.Contains(chips, "OpenCode") {
				t.Errorf("workbench wizard chips = %q, want only Codex and Claude", chips)
			}
		})
	}
}

func TestWorkbenchEnterEngine(t *testing.T) {
	for _, test := range []struct {
		engine  pfmengine.ID
		kind    compose.Kind
		name    string
		account int
	}{
		{pfmengine.Codex, compose.NewCodex, "New Codex chat", 3},
		{pfmengine.Claude, compose.NewClaude, "New Claude chat", 2},
		{pfmengine.OpenCode, compose.NewClaude, "New Claude chat", 2}, // the bench has no OpenCode
	} {
		t.Run(string(test.engine), func(t *testing.T) {
			model := workbenchSelectedModel(4)
			model.remembered.Engine = string(test.engine)
			model, _ = applyKey(t, model, specialKey(tea.KeyEnter))
			// The engine step opens on the remembered engine; a bench that does
			// not offer it opens on its own first one. ⏎ walks the rest.
			for step := 0; step < 3 && model.outcome == OutcomeNone; step++ {
				model, _ = applyKey(t, model, specialKey(tea.KeyEnter))
			}
			result := model.Result()
			if result.Kind != OutcomeSelected || result.Row.Kind != test.kind ||
				result.Row.Name != test.name || result.Row.Account != test.account || result.PrimaryAccount != test.account ||
				result.Row.CWD != scribeWorkbench || result.Row.Workbench != scribeWorkbench || result.Row.Project != "acme › Scribe" {
				t.Fatalf(
					"workbench Enter = %#v, want %s/%s/account %d in %s",
					result,
					test.kind,
					test.name,
					test.account,
					scribeWorkbench,
				)
			}
		})
	}
}

func TestWorkbenchInvalidNotice(t *testing.T) {
	model := workbenchSelectedModel(7)
	row, _ := model.selectedRow()
	for _, selected := range []bool{false, true} {
		pointer, style := "│ ", professorUpdateFailedStyle
		if selected {
			pointer, style = "› ", professorUpdateFailedSelectedStyle
		}
		want := style.Render(fillLine(pointer+"⚠ WORKBENCH ⚠  "+labWorkbenchFault, 160))
		if got := model.renderGroupedRow(row, selected, 160, false); got != want {
			t.Errorf("workbench notice = %q, want %q", got, want)
		}
	}
	selected, command := applyKey(t, model, specialKey(tea.KeyEnter))
	if command != nil || selected.outcome != OutcomeNone || !reflect.DeepEqual(selected.rows, model.rows) ||
		!reflect.DeepEqual(
			selected.filtered,
			model.filtered,
		) || !reflect.DeepEqual(selected.killChanges, model.killChanges) ||
		selected.cursor != model.cursor || selected.actionIndex != model.actionIndex || selected.killStatus != model.killStatus ||
		selected.newChatEngine != model.newChatEngine || selected.primary != model.primary || selected.codexPrimary != model.codexPrimary {
		t.Errorf("notice Enter changed model or launched: outcome %v command %v", selected.outcome, command)
	}
	if got := dossierPurpose(row); got != "This workbench cannot launch: "+labWorkbenchFault {
		t.Errorf("notice purpose = %q", got)
	}
	row.ActivityNS = fixtureNowNS
	if !isNoticeKind(row.Kind) || hasRecency(row) || model.enterLabel(row, true) != "—" ||
		model.dossierActionAvailable(0, row) || dossierStatus(row) != "WORKBENCH" {
		t.Errorf("invalid workbench must carry WORKBENCH notice badge and disabled actions")
	}
}

func TestWorkbenchInvalidKillRefused(t *testing.T) {
	model := workbenchSelectedModel(7)
	model, _ = applyKey(t, model, controlKey('x'))
	if model.killStatus != "⌃X refused — the workbench error row is a notice, not a chat" {
		t.Fatalf("kill refusal = %q", model.killStatus)
	}
}

func TestWorkbenchNameGroupsStayWithOwner(t *testing.T) {
	model := workbenchGoldenModel(80)
	for _, index := range []int{5, 6} {
		if got := model.nameGroups[index]; got != (nameGroup{name: "_SCRIBE", count: 2}) {
			t.Errorf("Scribe group for row %d = %#v, want _SCRIBE (2)", index, got)
		}
	}
	if got := model.nameGroups[3]; got.count != 1 {
		t.Errorf("plain _SCRIBE:9 folded with workbench: %#v", got)
	}
	plain := ansi.Strip(model.View().Content)
	if strings.Count(plain, "_SCRIBE (2)") != 1 || !strings.Contains(plain, "_SCRIBE (1)") {
		t.Errorf("workbench name groups:\n%s", plain)
	}
}

func TestWorkbenchDossierLaunchPurpose(t *testing.T) {
	model := workbenchSelectedModel(4)
	row, _ := model.selectedRow()
	want := "Enter starts a new Codex chat in /work/acme/docs/scribe on its workbench prompt."
	if got := dossierPurpose(row); got != want {
		t.Errorf("workbench purpose = %q, want %q", got, want)
	}
	lines := model.dossierLines(row, 100, 15)
	var plain []string
	for _, line := range lines {
		text := ""
		for _, part := range line {
			text += part.text
		}
		plain = append(plain, strings.TrimSpace(text))
	}
	if joined := strings.Join(plain, " "); !strings.Contains(
		joined,
		"Enter chooses the engine, account and cache of a new chat in "+scribeWorkbench+" on its workbench prompt",
	) {
		t.Errorf("dossier does not name the steps and the workbench: %s", joined)
	}
}

const ownLauncherHelper = "PFM_UI_OWN_LAUNCHER_HELPER"

// TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage pins that the
// new-chat picker reads the launcher registry, never a fixed engine list: an
// engine whose launcher is registered only in its own package (a fourth
// engine) is offered. The registration runs in a child process of this test
// binary, so every other test here keeps the shipped engines.
func TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage(t *testing.T) {
	const name = "TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage"
	if os.Getenv(ownLauncherHelper) != "1" {
		t.Parallel()
		command := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
		command.Env = append(os.Environ(), ownLauncherHelper+"=1")
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "--- PASS: "+name) {
			t.Fatalf("own-launcher proof failed: %v\n%s", err, output)
		}
		return
	}
	id := pfmengine.ID("zz")
	spawn.RegisterLauncher(id, ownLauncher{})
	if got := workbenchGoldenModel(80).newChatEngines(); !slices.Contains(got, id) {
		t.Fatalf("new-chat engines = %v, want the launcher registered as %s", got, id)
	}
}

type ownLauncher struct{}

func (ownLauncher) ComposerReady(string) bool { return true }

func (ownLauncher) Rename(
	context.Context, spawn.Tmux, string, string, string, spawn.Timings, spawn.Trace,
) (string, error) {
	return "", nil
}

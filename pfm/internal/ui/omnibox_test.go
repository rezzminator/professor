package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func typeText(t *testing.T, model Model, text string) Model {
	t.Helper()
	for _, character := range text {
		model, _ = applyKey(t, model, printableKey(character))
	}
	return model
}

func shiftKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Mod: tea.ModShift})
}

func placeModel(dirExists func(string) (bool, error)) Model {
	rows, dirs := placeFixtureRows()
	rows = append(rows[:1], append([]compose.Row{
		{Kind: compose.NewCodex, Name: "New Codex chat", Project: "app", CWD: "/src/app"},
	}, rows[1:]...)...)
	return NewModel(Snapshot{
		Rows:                rows,
		ProjectDirs:         dirs,
		Home:                "/jail/me",
		MergeNewChat:        true,
		PrimaryAccount:      1,
		AccountIDs:          []int{1},
		CodexPrimaryAccount: 2,
		CodexAccountIDs:     []int{2},
		DirExists:           dirExists,
		NowNS:               fixtureNowNS,
		Width:               120,
		Height:              30,
	})
}

func visibleNames(model Model) string {
	names := make([]string, 0, len(model.filtered))
	rows := model.VisibleRows()
	for index := range rows {
		if !isNewChatActionKind(rows[index].Kind) {
			names = append(names, rows[index].Name)
		}
	}
	return strings.Join(names, ",")
}

func launchNames(model Model) string {
	names := make([]string, 0, len(model.filtered))
	rows := model.VisibleRows()
	for index := range rows {
		names = append(names, rows[index].Name)
	}
	return strings.Join(names, ",")
}

func launchRow(t *testing.T, model Model) compose.Row {
	t.Helper()
	for index := range model.rows {
		if isFleetNewChatRow(model.rows[index]) {
			return model.rows[index]
		}
	}
	t.Fatal("no fleet launch row")
	return compose.Row{}
}

func TestPickerOpensOnItsPlaceWithNestedChatsAndNothingElse(t *testing.T) {
	model := placeModel(nil)
	if got := visibleNames(model); got != "here,nested" {
		t.Fatalf("opening place lists %q, want here,nested (ops stays off screen)", got)
	}
	rail := model.renderPlaceRail(120)
	if !strings.Contains(rail, " app ●1 ↻1 ") || !strings.Contains(rail, "ops ●1") {
		t.Fatalf("rail = %q", rail)
	}
}

func TestShiftArrowsSwapPlacesAndTheLaunchRowFollows(t *testing.T) {
	model, command := applyKey(t, placeModel(nil), shiftKey(tea.KeyRight))
	if command != nil || model.place != "/src/ops" || visibleNames(model) != "away" {
		t.Fatalf("⇧→ = place %q rows %q", model.place, visibleNames(model))
	}
	if row := launchRow(t, model); row.CWD != "/src/ops" || row.Project != "ops" {
		t.Fatalf("launch row still targets %s (%s)", row.CWD, row.Project)
	}
	// A find reaches the launch row under the place it now names.
	if found := typeText(t, model, "ops"); !strings.Contains(launchNames(found), "New Claude chat") {
		t.Fatalf("typing ops after ⇧→ lists %q, want the launch row that now starts in ops", launchNames(found))
	}
	model, _ = applyKey(t, model, shiftKey(tea.KeyRight))
	if model.place != allPlaceRoot || visibleNames(model) != "here,nested,away" {
		t.Fatalf("✱ all = place %q rows %q", model.place, visibleNames(model))
	}
	if row := launchRow(t, model); row.CWD != "/src/app" || row.Project != "app" {
		t.Fatalf("✱ all launch row = %s (%s), want the opening project back", row.CWD, row.Project)
	}
	model, _ = applyKey(t, model, shiftKey(tea.KeyLeft))
	if model.place != "/src/ops" {
		t.Fatalf("⇧← = %q, want back to ops", model.place)
	}
}

func TestAtQueryPreviewsThenCommitsAPlace(t *testing.T) {
	model := typeText(t, placeModel(nil), "@op")
	if model.place != "/src/app" || visibleNames(model) != "away" || model.query.Prompt != "go › " {
		t.Fatalf("@op preview = place %q rows %q prompt %q", model.place, visibleNames(model), model.query.Prompt)
	}
	model, command := applyKey(t, model, specialKey(tea.KeyEnter))
	if command != nil || model.place != "/src/ops" || model.query.Value() != "" || visibleNames(model) != "away" ||
		model.query.Prompt != findPrompt {
		t.Fatalf(
			"⏎ = place %q query %q rows %q quit=%v",
			model.place,
			model.query.Value(),
			visibleNames(model),
			command,
		)
	}
}

func TestEscBacksOutOfAModeBeforeItClosesThePicker(t *testing.T) {
	for _, query := range []string{"@op"} {
		model, command := applyKey(t, typeText(t, placeModel(nil), query), specialKey(tea.KeyEscape))
		if command != nil || model.query.Value() != "" || model.place != "/src/app" ||
			visibleNames(model) != "here,nested" {
			t.Fatalf("esc in %q = quit %v query %q place %q", query, command != nil, model.query.Value(), model.place)
		}
		if _, command = applyKey(t, model, specialKey(tea.KeyEscape)); command == nil {
			t.Fatalf("a second esc after %q did not close the picker", query)
		}
	}
}

func TestAtPathMakesAnyDirectoryAPlace(t *testing.T) {
	exists := func(path string) (bool, error) { return path == "/data/new", nil }
	model, _ := applyKey(t, typeText(t, placeModel(exists), "@/data/new"), specialKey(tea.KeyEnter))
	if model.place != "/data/new" || visibleNames(model) != "" || launchRow(t, model).CWD != "/data/new" {
		t.Fatalf("@/data/new = place %q rows %q launch %q", model.place, visibleNames(model), launchRow(t, model).CWD)
	}
	model, _ = applyKey(t, typeText(t, placeModel(exists), "@/data/none"), specialKey(tea.KeyEnter))
	if model.place != "/src/app" || !strings.Contains(model.killStatus, "no place or directory matches @/data/none") {
		t.Fatalf("a missing directory = place %q status %q, want a visible refusal", model.place, model.killStatus)
	}
	// A directory that could not be looked at says so; it is not "no match".
	unreadable := func(string) (bool, error) { return false, errors.New("permission denied") }
	model = typeText(t, placeModel(unreadable), "@/data/locked")
	if rail := model.renderPlaceRail(120); !strings.Contains(rail, "unreadable") {
		t.Fatalf("rail while an unreadable path is typed = %q, want it named unreadable", rail)
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyEnter))
	if model.place != "/src/app" || !strings.Contains(model.killStatus, "permission denied") ||
		strings.Contains(model.killStatus, "no place or directory matches") {
		t.Fatalf("an unreadable directory = place %q status %q, want the failure named", model.place, model.killStatus)
	}
}

// A picker opened in ~ sits on the everywhere stop: an `@./` path is read from
// the directory it was opened in, never fuzzy-matched as a place name.
func TestAtRelativePathFromTheEverywhereStopIsADirectory(t *testing.T) {
	exists := func(path string) (bool, error) { return path == "/jail/me/x", nil }
	model := NewModel(Snapshot{
		Rows: []compose.Row{
			{Kind: compose.NewClaude, Name: "New Claude chat", Project: "~", CWD: "/jail/me"},
			{
				Kind:       compose.LiveClaude,
				ID:         "dotted",
				Name:       "dotted",
				Project:    "x",
				CWD:        "/jail/me/my.app/x",
				ActivityNS: 9,
			},
		},
		ProjectDirs:  map[string]string{"x": "/jail/me/my.app/x"},
		Home:         "/jail/me",
		MergeNewChat: true, PrimaryAccount: 1, AccountIDs: []int{1},
		DirExists: exists, NowNS: fixtureNowNS, Width: 120, Height: 30,
	})
	if model.place != allPlaceRoot {
		t.Fatalf("setup: a picker opened in ~ is on %q, want the everywhere stop", model.place)
	}
	model, _ = applyKey(t, typeText(t, model, "@./x"), specialKey(tea.KeyEnter))
	if model.place != "/jail/me/x" {
		t.Fatalf("@./x from ~ = place %q status %q, want the directory /jail/me/x", model.place, model.killStatus)
	}
}

func TestAScheduleFiredChatPinsAtTheTopFromAnyPlace(t *testing.T) {
	rows, dirs := placeFixtureRows()
	for index := range rows {
		rows[index].ScheduleFired = rows[index].ID == "away"
	}
	model := NewModel(Snapshot{
		Rows: rows, ProjectDirs: dirs, Home: "/jail/me", MergeNewChat: true,
		PrimaryAccount: 1, AccountIDs: []int{1}, NowNS: fixtureNowNS, Width: 120, Height: 30,
	})
	if got := visibleNames(model); got != "away,here,nested" {
		t.Fatalf("opening place lists %q, want the scheduled chat from ops pinned first", got)
	}
	// A search reaches it once, not again as an out-of-place match.
	model = typeText(t, model, "away")
	if got := visibleNames(model); got != "away" {
		t.Fatalf("search lists %q, want the scheduled chat exactly once", got)
	}
}

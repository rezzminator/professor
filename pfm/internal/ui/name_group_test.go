package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func groupRow(kind compose.Kind, id, name, project string) compose.Row {
	return compose.Row{
		Kind: kind, ID: id, Name: name, Project: project,
		Socket: "cx-1800000000-1-1", CWD: "/work/" + project,
		ActivityNS: fixtureNowNS, Account: 1,
	}
}

func renderedList(t *testing.T, rows []compose.Row) string {
	t.Helper()
	snapshot := fixtureSnapshot(120)
	snapshot.Rows = rows
	snapshot.KilledCount = 0
	model := NewModel(snapshot)
	return ansi.Strip(model.View().Content)
}

func TestColonNamesFoldIntoOneGroupPanel(t *testing.T) {
	var views []string
	for _, rows := range [][]compose.Row{
		{groupRow(compose.LiveCodex, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "P:BUILDER", "alpha")},
		{
			groupRow(compose.LiveCodex, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "P:BUILDER", "alpha"),
			groupRow(compose.LiveClaude, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "P:TESTER", "alpha"),
		},
		{groupRow(compose.LiveCodex, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "BUILDER", "alpha")},
		{groupRow(compose.LiveCodex, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ":BUILDER", "alpha")},
	} {
		views = append(views, renderedList(t, rows))
	}
	golden(t, "ui_name_groups.txt", strings.Join(views, "\n"))
}

// A resumable namesake must join its live group panel, not render flat next
// to it: a ↻ resumable SOLO:BUILD sitting outside the SOLO panel its live
// SOLO:REFINE sibling opened is the isLiveNameGroupRow defect this pins.
func TestResumableNamesakeJoinsLiveGroupPanel(t *testing.T) {
	rows := []compose.Row{
		groupRow(compose.LiveCodex, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "SOLO:REFINE", "alpha"),
		groupRow(compose.ResumeCodex, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "SOLO:BUILD", "alpha"),
	}
	view := renderedList(t, rows)
	if !strings.Contains(view, "SOLO (2)") {
		t.Fatalf("live+resumable SOLO namesakes did not fold into one group of 2:\n%s", view)
	}
	if strings.Count(view, "SOLO (") != 1 {
		t.Fatalf("the SOLO group header was printed more than once:\n%s", view)
	}

	snapshot := fixtureSnapshot(120)
	snapshot.Rows = rows
	snapshot.KilledCount = 0
	model := NewModel(snapshot)
	if len(model.order) != 2 {
		t.Fatalf("model.order = %v, want both rows present", model.order)
	}
	if len(model.nameGroups) != 2 {
		t.Fatalf("model.nameGroups = %#v, want both rows carrying the SOLO group", model.nameGroups)
	}
	for _, index := range model.order {
		group, ok := model.nameGroups[index]
		if !ok || group.name != "SOLO" || group.count != 2 {
			t.Fatalf("model.nameGroups[%d] = %#v ok=%v, want SOLO count=2", index, group, ok)
		}
	}
}

// The group path emits the member list and skips the row it was called for, so
// a row that is NOT itself a member must never take it. Lowering the threshold
// to one member made that reachable for any single grouped chat, and a
// disappearing row is a worse bug than a missing indent.
func TestNonMemberRowSharingAPrefixIsNeverDropped(t *testing.T) {
	rows := []compose.Row{
		groupRow(compose.LiveCodex, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "P:BUILDER", "alpha"),
		groupRow(compose.NewCodex, "new-codex", "P:NEW", "alpha"),
	}
	snapshot := fixtureSnapshot(120)
	snapshot.Rows = rows
	snapshot.KilledCount = 0
	model := NewModel(snapshot)
	visible := model.VisibleRows()
	if len(visible) != len(rows) {
		names := make([]string, 0, len(visible))
		for _, row := range visible {
			names = append(names, row.Name)
		}
		t.Fatalf("%d of %d rows survived grouping: %v", len(visible), len(rows), names)
	}
}

// A colon inside prose is punctuation, not a group declaration. This is the
// control that keeps the one-member panel from turning every stray colon into
// a header — the exact case the full suite caught.
func TestProseColonsAreNotGroupDeclarations(t *testing.T) {
	for _, name := range []string{
		"fix: the bug",
		"flight 3: rework",
		"note:",
		":BUILDER",
		"P: BUILDER",
		"plain",
	} {
		t.Run(name, func(t *testing.T) {
			if prefix, grouped := nameGroupPrefix(name); grouped {
				t.Fatalf("%q was read as group %q", name, prefix)
			}
		})
	}
}

// The shapes that DO declare a group, so the rule above cannot be tightened
// into rejecting everything and still look correct.
func TestGroupDeclarationsAreRecognised(t *testing.T) {
	for name, want := range map[string]string{
		"P:BUILDER":          "P",
		"W5:TESTER":          "W5",
		"ccrt-fixes:lane2":   "ccrt-fixes",
		"P:BUILDER:NESTED":   "P",
		"GROUP:name with sp": "GROUP",
	} {
		t.Run(name, func(t *testing.T) {
			prefix, grouped := nameGroupPrefix(name)
			if !grouped || prefix != want {
				t.Fatalf("nameGroupPrefix(%q) = (%q, %v), want (%q, true)", name, prefix, grouped, want)
			}
		})
	}
}

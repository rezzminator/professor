package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

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

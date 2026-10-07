package ui

import (
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// TestRemindedRowsSortAboveEveryPinAndGroup pins the reminder order: a row
// with an unseen fired reminder precedes the pinned update and new-chat rows
// and every project and name group, and leaves its name group.
func TestRemindedRowsSortAboveEveryPinAndGroup(t *testing.T) {
	tests := []struct {
		name           string
		reminded       []string
		wantFirst      []string
		wantGroupCount int
		wantGrouped    []string
	}{
		{
			name:           "group member and later-project row",
			reminded:       []string{"grp:b", "late"},
			wantFirst:      []string{"grp:b", "late", "update", "new"},
			wantGroupCount: 2,
			wantGrouped:    []string{"grp:a", "grp:c"},
		},
		{
			name:           "later-project row only",
			reminded:       []string{"late"},
			wantFirst:      []string{"late", "update", "new"},
			wantGroupCount: 3,
			wantGrouped:    []string{"grp:a", "grp:b", "grp:c"},
		},
		{
			name:           "every group member reminded empties the group",
			reminded:       []string{"grp:a", "grp:b", "grp:c"},
			wantFirst:      []string{"grp:a", "grp:b", "grp:c", "update", "new"},
			wantGroupCount: 0,
			wantGrouped:    nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := fixtureSnapshot(120)
			snapshot.MergeNewChat = true
			snapshot.Rows = []compose.Row{
				{Kind: compose.ProfessorUpdate, ID: "update", Name: "update", Project: "alpha", ActivityNS: 1},
				{Kind: compose.NewClaude, ID: "new", Name: "new", Project: "alpha", ActivityNS: 2},
				{Kind: compose.LiveClaude, ID: "grp:a", Name: "grp:a", Project: "alpha", ActivityNS: 100},
				{Kind: compose.LiveClaude, ID: "grp:b", Name: "grp:b", Project: "alpha", ActivityNS: 90},
				{Kind: compose.LiveClaude, ID: "grp:c", Name: "grp:c", Project: "alpha", ActivityNS: 80},
				{Kind: compose.LiveClaude, ID: "late", Name: "late", Project: "omega", ActivityNS: 70},
			}
			for index := range snapshot.Rows {
				snapshot.Rows[index].Reminded = slices.Contains(test.reminded, snapshot.Rows[index].ID)
			}
			model := NewModel(snapshot)
			if len(model.filtered) < len(test.wantFirst) {
				t.Fatalf("filtered = %v, want at least %d rows", model.filtered, len(test.wantFirst))
			}
			for position, wantID := range test.wantFirst {
				if got := model.rows[model.filtered[position]].ID; got != wantID {
					t.Fatalf("filtered[%d] = %q, want %q (order %v)", position, got, wantID, model.filtered)
				}
			}
			var grouped []string
			for index, group := range model.nameGroups {
				if group.count != test.wantGroupCount {
					t.Fatalf("group %q count = %d, want %d", group.name, group.count, test.wantGroupCount)
				}
				grouped = append(grouped, model.rows[index].ID)
			}
			slices.Sort(grouped)
			if !slices.Equal(grouped, test.wantGrouped) {
				t.Fatalf("grouped rows = %v, want %v", grouped, test.wantGrouped)
			}
		})
	}
}

package themesync

import (
	"slices"
	"strings"
	"testing"
)

func TestWorkbenchGroupTakesTheLongestPrefixAtAWordBoundary(t *testing.T) {
	cases := map[string]string{
		"editor.background":                  "editor",
		"editorCursor.foreground":            "editor",
		"editorGutter.addedBackground":       "editor-gutter",
		"editorBracketMatch.border":          "editor-guides",
		"editorSuggestWidget.background":     "editor-widgets",
		"diffEditor.insertedTextBackground":  "diff-merge",
		"tab.activeBackground":               "tabs",
		"editorGroupHeader.tabsBackground":   "tabs",
		"sideBar.background":                 "sidebar-explorer",
		"list.activeSelectionBackground":     "sidebar-explorer",
		"statusBar.background":               "statusbar",
		"panel.background":                   "panel",
		"terminal.ansiRed":                   "terminal",
		"terminalSymbolIcon.aliasForeground": "symbols",
		"titleBar.activeBackground":          "titlebar-menus",
		"activityBar.background":             "activitybar",
		"focusBorder":                        "base",
	}
	for id, want := range cases {
		got, err := workbenchGroup(id)
		if err != nil || got != want {
			t.Errorf("workbenchGroup(%q) = %q, %v; want %q", id, got, err, want)
		}
	}
	for _, id := range []string{"editors.background", "tabby.background", "nothingClaims.this"} {
		if got, err := workbenchGroup(id); err == nil || !strings.Contains(err.Error(), "no vscode/workbench group") {
			t.Errorf("workbenchGroup(%q) = %q, %v; want the no-group error", id, got, err)
		}
	}
}

// TestEveryRegistryColourHasOneGroupAndEveryGroupAColour holds the table to
// the registry: every id in vscode_color_ids.txt lands in a group, no prefix
// is claimed twice (two groups could not both own an id), and no group is
// dead weight, a file every theme would carry empty forever.
func TestEveryRegistryColourHasOneGroupAndEveryGroupAColour(t *testing.T) {
	owners := map[string]string{}
	for _, group := range workbenchGroups {
		for _, prefix := range strings.Fields(group.prefixes) {
			if owner, taken := owners[prefix]; taken {
				t.Errorf("prefix %q claimed by both %s and %s", prefix, owner, group.name)
			}
			owners[prefix] = group.name
		}
	}
	used := map[string]int{}
	for id := range vscodeColorIDs {
		group, err := workbenchGroup(id)
		if err != nil {
			t.Error(err)
			continue
		}
		used[group]++
	}
	for _, name := range workbenchGroupNames() {
		if used[name] == 0 {
			t.Errorf("group %s claims no colour in vscode_color_ids.txt", name)
		}
	}
	if names := workbenchGroupNames(); !slices.IsSorted(names) {
		t.Errorf("workbenchGroups not sorted by name: %v", names)
	}
}

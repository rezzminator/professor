package themesync

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// workbenchGroups sorts a profile's VS Code workbench colours into the files
// of its directory's vscode/workbench/: each group is one {name}.json there,
// holding every colour whose id's first dotted segment its prefixes claim
// (workbenchGroup). The table is the whole rule: a new VS Code colour family
// joins a group here, and profile_groups_test.go holds every id in
// vscode_color_ids.txt to exactly one group.
var workbenchGroups = []struct {
	name     string
	prefixes string // space-separated
}{
	{"activitybar", "activity modernActivityBar"},
	{"base", "actionBar badge banner browser chart charts contrastActiveBorder contrastBorder " +
		"descriptionForeground disabledForeground errorForeground focusBorder foreground icon markdownAlert " +
		"modernSash modernUI profileBadge profiles progressBar sash scrollbar selection strongForeground surface " +
		"text toolbar walkThrough walkthrough welcomePage widget"},
	{"chat", "activeSessionView agent agents chat inactiveSessionView inlineChat interactive mcpIcon"},
	{"controls", "button checkbox dropdown extension input keybinding radio settings"},
	{"debug-testing", "debug ports problems testing"},
	{"diff-merge", "diffEditor merge multiDiffEditor"},
	{"editor", "editor"},
	{"editor-guides", "editorBracket editorIndentGuide"},
	{"editor-gutter", "editorGutter editorMinimap editorOverviewRuler minimap"},
	{"editor-widgets", "commentsView editorActionList editorCommentsWidget editorError editorHint " +
		"editorHoverWidget editorInfo editorLightBulb editorMarkerNavigation editorSuggestWidget editorWarning " +
		"editorWidget inlineEdit peekView simpleFindWidget"},
	{"notebook", "notebook"},
	{"notifications", "notification notifications"},
	{"panel", "modernPanel outputView panel"},
	{"quick-input", "pickerGroup quickInput"},
	{"sidebar-explorer", "git list scmGraph search sideBar tree"},
	{"statusbar", "statusBar"},
	{"symbols", "symbolIcon terminalSymbolIcon"},
	{"tabs", "breadcrumb editorGroup modernEditorTab modernTab sideBySideEditor tab"},
	{"terminal", "terminal"},
	{"titlebar-menus", "commandCenter menu menubar titleBar window"},
}

// workbenchGroupNames are workbenchGroups' names, in table order.
func workbenchGroupNames() []string {
	names := make([]string, len(workbenchGroups))
	for index, group := range workbenchGroups {
		names[index] = group.name
	}
	return names
}

// workbenchGroup is the group a VS Code colour id belongs to: the id's first
// dotted segment is claimed by the longest prefix that equals it or that it
// continues at a camelCase word boundary (`editor` claims `editorCursor`, not
// `editors`). An id no prefix claims is an error naming it.
func workbenchGroup(id string) (string, error) {
	segment, _, _ := strings.Cut(id, ".")
	best, bestLength := "", 0
	for _, group := range workbenchGroups {
		for _, prefix := range strings.Fields(group.prefixes) {
			if len(prefix) > bestLength && claimsSegment(prefix, segment) {
				best, bestLength = group.name, len(prefix)
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("VS Code colour %q: no vscode/workbench group claims %q (profile_groups.go)", id, segment)
	}
	return best, nil
}

// claimsSegment reports whether prefix equals segment or ends at one of its
// camelCase word boundaries.
func claimsSegment(prefix, segment string) bool {
	rest, found := strings.CutPrefix(segment, prefix)
	if !found {
		return false
	}
	if rest == "" {
		return true
	}
	next, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsUpper(next)
}

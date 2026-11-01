package gather

import (
	"slices"
	"strings"
	"testing"
)

// TestParseTmuxPanesCostsOnlyTheMalformedLine: a list-panes line that does not
// read costs that pane only; the readable panes come back with an error naming
// the loss, and an answer with no readable line is an error and no panes.
func TestParseTmuxPanesCostsOnlyTheMalformedLine(t *testing.T) {
	line := func(paneID, pid string) string {
		return strings.Join(
			[]string{"cc-1", "@1", "win", "title", "/dev/pts/7", pid, paneID, "1", "/work", "claude", "0", "3"},
			"\x1f",
		) + "\n"
	}
	for _, test := range []struct {
		name      string
		output    string
		wantPanes []string
		wantErr   string
	}{
		{name: "every line readable", output: line("%1", "41") + line("%2", "42"), wantPanes: []string{"%1", "%2"}},
		{
			name:      "a pid that is no number",
			output:    line("%1", "41") + line("%2", "zz") + line("%3", "43"),
			wantPanes: []string{"%1", "%3"},
			wantErr: `1 of 3 pane lines unreadable, the rest kept; the first: ` +
				`tmux x pane pid "zz": strconv.Atoi: parsing "zz": invalid syntax`,
		},
		{
			name:    "no readable line",
			output:  "garbage\n",
			wantErr: `tmux x returned 1 pane fields in "garbage"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			panes, err := parseTmuxPanes("x", []byte(test.output))
			var ids []string
			for _, pane := range panes {
				ids = append(ids, pane.PaneID)
			}
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if !slices.Equal(ids, test.wantPanes) || gotErr != test.wantErr {
				t.Fatalf("parseTmuxPanes() = %q, %q; want %q, %q", ids, gotErr, test.wantPanes, test.wantErr)
			}
		})
	}
}

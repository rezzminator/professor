package herdr

import (
	"context"
	"testing"
)

// A pane's workspace label comes from the live lists; a pane the server does
// not list is named, never an empty label.
func TestPaneWorkspaceLabelReadsTheLiveWorkspace(t *testing.T) {
	fake := serveHerdr(t, map[string]string{
		"pane.list": `{"type":"pane_list","panes":[` +
			`{"pane_id":"w1:p1","terminal_id":"term-1","workspace_id":"w1"},` +
			`{"pane_id":"w3:p9","terminal_id":"term-9","workspace_id":"w3"}]}`,
		"workspace.list": `{"type":"workspace_list","workspaces":[` +
			`{"workspace_id":"w1","label":"orchard"},{"workspace_id":"w3","label":"lab.wb"}]}`,
	})
	for _, test := range []struct{ pane, want, err string }{
		{pane: "w3:p9", want: "lab.wb"},
		{pane: "w1:p1", want: "orchard"},
		{pane: "w2:p4", err: "herdr workspace of pane w2:p4: the pane is not in " + fake.path + "'s pane list"},
	} {
		got, err := PaneWorkspaceLabel(context.Background(), fake.path, test.pane)
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if got != test.want || gotErr != test.err {
			t.Errorf("PaneWorkspaceLabel(%s) = %q, %q; want %q, %q", test.pane, got, gotErr, test.want, test.err)
		}
	}
}

package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestReadTabPanesReadsPaneListThroughItsOneParser: the tab painter's pane
// list is ParsePanes' reading, so a pane_list with no panes array is an error
// — never an empty tab roster that would read every owned tab as gone.
func TestReadTabPanesReadsPaneListThroughItsOneParser(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	fake := serveHerdr(t, map[string]string{"pane.list": `{"type":"pane_list"}`})
	panes, err := ReadTabPanes(context.Background(), fake.path)
	if err == nil || !strings.Contains(err.Error(), "panes missing or null") {
		t.Fatalf("ReadTabPanes() over a pane_list with no panes = %+v, %v; want ParsePanes' refusal", panes, err)
	}
}

// TestPaneRecordKeepsItsFieldNames: a nested shell's pane record
// (hookentry's pfm-shell-{pid}.pane) persists a Pane as JSON, so the wire
// names Herdr speaks never leak into it: a record written before ParsePanes
// decoded every PaneInfo field still reads back, and a new one keeps the
// field names an older reader knows.
func TestPaneRecordKeepsItsFieldNames(t *testing.T) {
	var recorded Pane
	if err := json.Unmarshal([]byte(`{"PaneID":"w1:p1","TerminalID":"term-1"}`), &recorded); err != nil ||
		recorded.PaneID != "w1:p1" || recorded.TerminalID != "term-1" {
		t.Fatalf("legacy pane record read back as %+v, %v", recorded, err)
	}
	data, err := json.Marshal(Pane{PaneID: "w1:p1", TerminalID: "term-1"})
	if err != nil || !strings.Contains(string(data), `"PaneID":"w1:p1"`) ||
		!strings.Contains(string(data), `"TerminalID":"term-1"`) {
		t.Fatalf("pane record = %s, %v; want the PaneID and TerminalID field names", data, err)
	}
}

// TestReadTabPanesKeepsCompletePanesBesideAnIncompleteOne: one pane listed
// without its tab is named, and every complete pane is still returned.
func TestReadTabPanesKeepsCompletePanesBesideAnIncompleteOne(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	fake := serveHerdr(t, map[string]string{"pane.list": `{"type":"pane_list","panes":[` +
		`{"pane_id":"w1:p1","terminal_id":"term-1","workspace_id":"w1","focused":false},` +
		`{"pane_id":"w1:p2","terminal_id":"term-2","workspace_id":"w1","tab_id":"w1:t2","focused":true}]}`})
	panes, err := ReadTabPanes(context.Background(), fake.path)
	var incomplete *IncompleteTabPanesError
	if !errors.As(err, &incomplete) || err.Error() != `tab panes: incomplete PaneInfo for "w1:p1"` {
		t.Fatalf("ReadTabPanes() error = %v, want the incomplete w1:p1 named", err)
	}
	if len(panes) != 1 || panes[0].Pane != "w1:p2" || panes[0].Tab != "w1:t2" {
		t.Fatalf("ReadTabPanes() panes = %+v, want the complete w1:p2 alone", panes)
	}
}

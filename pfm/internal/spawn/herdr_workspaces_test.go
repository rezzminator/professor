package spawn

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/place"
)

// A place's workspace is the one wearing its label; a place with none gets one,
// and the shell tab Herdr opened it with comes back for the chat to replace. A
// created workspace whose reply names no such tab is an error, never a
// workspace the chat lands beside a shell in.
func TestHerdrWorkspacesWorkspaceForNamesTheStartingTab(t *testing.T) {
	desk := place.Place{Root: "/fixture/fleet-desk", Label: "fleet-desk"}
	for _, test := range []struct {
		name                     string
		workspaces               [][3]any
		bare                     bool
		workspace, starting, err string
	}{
		{name: "a labelled workspace", workspaces: [][3]any{{"wE", "fleet-desk", 6}}, workspace: "wE"},
		{name: "a created workspace", workspaces: [][3]any{{"wF", "orchard", 7}}, workspace: "w91", starting: "w91:t1"},
		{
			name: "a created workspace whose reply names no tab", workspaces: [][3]any{{"wF", "orchard", 7}}, bare: true,
			err: `herdr workspace "fleet-desk" may exist: unexpected reply`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &placeHerdr{workspaces: test.workspaces, bareCreate: test.bare}
			socket := servePlaceHerdr(t, fake)
			listed, err := listHerdrWorkspaces(context.Background(), socket)
			if err != nil {
				t.Fatal(err)
			}
			workspace, starting, err := listed.workspaceFor(context.Background(), socket, desk)
			if test.err != "" {
				if err == nil || !strings.HasPrefix(err.Error(), test.err) || workspace != "" || starting != "" {
					t.Fatalf("workspaceFor = %q, %q, %v; want the error %q", workspace, starting, err, test.err)
				}
				return
			}
			if err != nil || workspace != test.workspace || starting != test.starting {
				t.Fatalf("workspaceFor = %q, %q, %v; want %q, %q (calls %v)",
					workspace, starting, err, test.workspace, test.starting, fake.methods())
			}
		})
	}
}

// The starting tab of a created workspace closes once a chat moved in beside
// it; a close Herdr refuses leaves the chat placed and the tab counted.
func TestHerdrWorkspacesCloseStarting(t *testing.T) {
	for _, test := range []struct {
		name   string
		refuse string
		panes  int
	}{
		{name: "closed", panes: 1},
		{name: "refused", refuse: "tab.close", panes: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &placeHerdr{refuse: test.refuse}
			socket := servePlaceHerdr(t, fake)
			listed := herdrWorkspaces{byLabel: map[string]string{}, panes: map[string]int{"w91": 2}}
			listed.closeStarting(context.Background(), socket, "w91", "w91:t1")
			closed, found := fake.call("tab.close")
			if want := map[string]any{"tab_id": "w91:t1"}; !found || !reflect.DeepEqual(closed.Params, want) {
				t.Fatalf("tab.close = %+v (found %v), want %v", closed.Params, found, want)
			}
			if listed.panes["w91"] != test.panes {
				t.Fatalf("panes of w91 = %d, want %d", listed.panes["w91"], test.panes)
			}
		})
	}
}

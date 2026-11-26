package spawn

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A new chat inherits the space it was started under, the launching chat's,
// or the launching shell's space workspace, recorded before its tab is born
// there; one started from a folder place's workspace or a chat with no space
// is born in its folder's place as before.
func TestBirthSpaceBornInTheSpaceItWasStartedFrom(t *testing.T) {
	for _, test := range []struct {
		name, pane, chosen string
		parent             string // the session of the live chat holding pane; "" for a shell
		space, workspace   string // the space recorded and the workspace the tab is born in
	}{
		{name: "a shell in a space's workspace", pane: "wG:p3", space: "glass", workspace: "wG"},
		{name: "a shell in a folder place's workspace", pane: "wF:p3", workspace: "wE"},
		{name: "a chat in a space", pane: "wF:p5", parent: "in-glass", space: "glass", workspace: "wG"},
		{name: "a chat with no space in a space's workspace", pane: "wG:p5", parent: "plain", workspace: "wE"},
		{name: "the space chosen in the picker", pane: "wF:p3", chosen: "glass", space: "glass", workspace: "wG"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fake := &placeHerdr{
				workspaces: [][3]any{{"wF", "orchard", 2}, {"wE", "fleet-desk", 1}, {"wG", "glass", 2}},
				panes:      map[string]string{test.pane: "term-launch"},
			}
			socket := servePlaceHerdr(t, fake)
			state := fleetdb.OpenSharedState(context.Background(), values)
			t.Cleanup(func() { _ = state.Close() })
			if test.parent != "" {
				if err := state.PutSeat(context.Background(), fleetdb.Seat{
					SeatID: "launching", SessionID: test.parent, Engine: "cc", Backend: BackendHerdr, State: "live",
					HerdrSocket: socket, HerdrPane: test.pane,
				}); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(values.Home, "native-claude")
			if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			fleet := fleetDesk()
			fleet.Spaces = map[string]string{"in-glass": "glass"}
			choice := BackendChoice{
				Backend: BackendHerdr, HerdrSocket: socket, HerdrPane: test.pane, Places: scanned(fleet),
			}
			space, err := BirthSpace(context.Background(), values, &choice, "sid1", test.chosen)
			if err != nil || space != test.space {
				t.Fatalf("BirthSpace = %q, %v; want %q", space, err, test.space)
			}
			spaces, err := state.Spaces(context.Background())
			if err != nil || spaces["sid1"].Space != test.space {
				t.Fatalf("recorded space of sid1 = %+v, %v; want %q", spaces["sid1"], err, test.space)
			}
			if _, err := SpawnHerdr(context.Background(), HerdrRequest{
				Paths: values, Choice: choice,
				Seat: fleetdb.Seat{SessionID: "sid1", Engine: "cc", Name: "BORN"},
				Run:  fleetdb.SeatRun{Binary: binary, CWD: "/fixture/fleet-desk"},
			}); err != nil {
				t.Fatal(err)
			}
			apply, found := fake.call("layout.apply")
			if !found || apply.Params["workspace_id"] != test.workspace {
				t.Fatalf("layout.apply = %+v (found %v), want workspace %s (calls %v)",
					apply.Params, found, test.workspace, fake.methods())
			}
		})
	}
}

package spawn

import (
	"cmp"
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// PlaceHerdrChat files the one chat that recorded a space, creating the space's
// workspace when no workspace wears the label, and leaves every other chat
// where it sits, though the folder places it elsewhere; an id that is no live
// native chat of the socket is an error naming it. The move is focused, and
// Herdr's view follows it, only when the chat's pane is the one Herdr has
// focused.
func TestPlaceHerdrChatMovesOnlyThatChat(t *testing.T) {
	const chat = "6fafd8a7-5059-4288-bf58-23e60c5fdb88"
	for _, test := range []struct {
		name, id, err, label string
		seatID               string // the seat the chat's row names, "ops-seat" when empty
		errPlain             bool   // err is the whole message, else err + the socket
		moves                int
		pane                 string
		homeless             bool   // the chat's space is cleared and its folder files under no place
		focused              string // the pane Herdr has focused before the move
		followed             bool   // the move was focused and Herdr focused the moved pane
		wantFocused          string // the pane Herdr has focused after
		space                string // the chat's recorded space, "glass" when empty
		focusless            bool   // Herdr's pane list omits each pane's focused field
		creates              int    // workspace.create calls
	}{
		{name: "the spaced chat", id: chat, moves: 1, pane: "wG:p8", label: "glass"},
		{
			name: "the spaced chat the user is looking at takes the view along", id: chat, moves: 1, pane: "wG:p8",
			label: "glass", focused: "wS:p1", followed: true, wantFocused: "wG:p8",
		},
		{
			name: "the spaced chat leaves the view on the pane the user is looking at", id: chat, moves: 1,
			pane: "wG:p8", label: "glass", focused: "wS:p2", wantFocused: "wS:p2",
		},
		{name: "a cleared chat filed under no place stays", id: chat, pane: "wS:p1", homeless: true},
		{
			name: "the spaced chat into a space with no workspace yet", id: chat, moves: 1, pane: "w93:p8",
			label: "quartz", space: "quartz", creates: 1,
		},
		{
			name: "a focus Herdr does not report moves nothing and creates no workspace", id: chat, pane: "wS:p1",
			label: "quartz", space: "quartz", focusless: true, errPlain: true,
			err: "herdr pane.list does not say whether pane wS:p1 is focused",
		},
		{
			name: "a chat that is no seat of the socket", id: "nope", pane: "wS:p1",
			err: "chat nope is not a live native Herdr chat on ",
		},
		{
			name: "a chat whose seat ended", id: chat, seatID: "ended-seat", pane: "wS:p1", errPlain: true, label: "glass",
			err: "chat " + chat + ": the seat ended, or does not prove to be this Herdr chat's",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			ctx := context.Background()
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fake := &placeHerdr{
				workspaces: [][3]any{{"wS", "scratch", 2}, {"wF", "orchard", 1}, {"wG", "glass", 1}},
				panes:      map[string]string{"wS:p1": "term-ops", "wS:p2": "term-grove", "wG:p1": "term-shell"},
				cwds:       map[string]string{"wS:p2": "/fixture/orchard"},
				focused:    test.focused,
				focusless:  test.focusless,
			}
			socket := servePlaceHerdr(t, fake)
			state := putOpsSeat(t, values, socket, "wS:p1")
			fleet := fleetDesk()
			space := cmp.Or(test.space, "glass")
			fleet.Spaces = map[string]string{chat: space}
			spaced := compose.Row{
				Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "fleet-desk", CWD: "/fixture/fleet-desk",
				ID: chat, Space: space, Name: "OPS", SeatID: cmp.Or(test.seatID, "ops-seat"), Socket: socket,
				PaneID: "wS:p1", HerdrWorkspace: "wS",
			}
			if test.homeless {
				fleet.Spaces, spaced.Space, spaced.Project, spaced.CWD = nil, "", "", ""
			}
			fleet.Rows = append(fleet.Rows,
				spaced,
				compose.Row{
					Kind: compose.LiveClaude, Backend: BackendHerdr, Project: "orchard", CWD: "/fixture/orchard",
					ID: "grove", Name: "GROVE", SeatID: "grove-seat", Socket: socket, PaneID: "wS:p2",
					HerdrWorkspace: "wS",
				})
			filed, followed, err := PlaceHerdrChat(ctx, values, socket, fleet, test.id)
			wantErr := test.err
			if !test.errPlain && wantErr != "" {
				wantErr += socket
			}
			if test.err == "" && err != nil || test.err != "" && (err == nil || err.Error() != wantErr) {
				t.Fatalf("PlaceHerdrChat = %v, want %q", err, wantErr)
			}
			if filed.Label != test.label {
				t.Fatalf("filed under %+v, want label %q", filed, test.label)
			}
			if followed != test.followed || fake.focused != test.wantFocused {
				t.Fatalf("followed = %v, Herdr focuses %q; want %v, %q (calls %v)",
					followed, fake.focused, test.followed, test.wantFocused, fake.methods())
			}
			if move, found := fake.call("pane.move"); found && move.Params["focus"] != (test.focused == "wS:p1") {
				t.Fatalf("pane.move focus = %v with %q focused, want %v", move.Params["focus"], test.focused,
					test.focused == "wS:p1")
			}
			moves, creates := 0, 0
			for _, method := range fake.methods() {
				switch method {
				case "pane.move":
					moves++
				case "workspace.create":
					creates++
				}
			}
			if moves != test.moves || creates != test.creates {
				t.Fatalf("pane.move ran %d times, workspace.create %d; want %d, %d (calls %v)", moves, creates,
					test.moves, test.creates, fake.methods())
			}
			seat, found, err := state.Seat(ctx, "ops-seat")
			if err != nil || !found || seat.HerdrPane != test.pane {
				t.Fatalf("seat = %+v (found %v, err %v), want pane %s", seat, found, err, test.pane)
			}
		})
	}
}

package reload

import (
	"bytes"
	"context"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// A native --new reboot's new session keeps the space of the chat it leaves
// behind; a chat with no space leaves the new one in its folder's place.
func TestInheritSpaceKeepsTheSpaceOfTheChatLeftBehind(t *testing.T) {
	for _, test := range []struct{ name, leftIn, want string }{
		{name: "a chat in a space", leftIn: "glass", want: "glass"},
		{name: "a chat with no space", leftIn: "", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			scheduleJail(t)
			ctx := context.Background()
			state := openScheduleState(t)
			const socket, pane = "/fixture/herdr.sock", "wG:p2"
			if err := state.PutSeat(ctx, fleetdb.Seat{
				SeatID: "reloading", SessionID: "left-behind", Engine: "cc", Backend: "herdr", State: "live",
				HerdrSocket: socket, HerdrPane: pane,
			}); err != nil {
				t.Fatal(err)
			}
			if test.leftIn != "" {
				if err := state.SetSpace(ctx, "left-behind", test.leftIn, 100); err != nil {
					t.Fatal(err)
				}
			}
			values, err := pfmconfig.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			inheritSpace(ctx, values, socket, pane, "reborn", &stderr)
			spaces, err := state.Spaces(ctx)
			if err != nil || spaces["reborn"].Space != test.want || stderr.String() != "" {
				t.Fatalf("space of reborn = %+v, %v, stderr %q; want %q",
					spaces["reborn"], err, stderr.String(), test.want)
			}
			if spaces["left-behind"].Space != test.leftIn {
				t.Fatalf("space of left-behind = %+v; want %q", spaces["left-behind"], test.leftIn)
			}
		})
	}
}

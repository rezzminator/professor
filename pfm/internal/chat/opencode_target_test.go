package chat

import (
	"testing"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

func liveOpenCodeRow() compose.Row {
	return compose.Row{
		Kind:        compose.LiveOpenCode,
		ID:          "ses_live",
		Name:        "P:OPENCODE",
		Socket:      "ox-1789813424-3207648-22387",
		SessionName: "ox-1789813424-3207648-22387",
		PaneID:      "%0",
		CWD:         "/work/api",
	}
}

// Every handle a human or an agent types for a live OpenCode chat resolves:
// its name, its session id, and its socket. Before LiveOpenCode existed each
// of these answered `no chat named …` about a TUI answering keystrokes.
func TestLiveOpenCodeSeatResolvesByNameIDAndSocket(t *testing.T) {
	row := liveOpenCodeRow()
	rows := []compose.Row{row}
	for _, handle := range []string{row.Name, row.ID, row.Socket} {
		found, ok, err := Match(rows, handle)
		if err != nil || !ok {
			t.Fatalf("Match(%q) = ok %v, err %v", handle, ok, err)
		}
		if found.ID != row.ID || found.Socket != row.Socket || found.Pane != "%0" {
			t.Fatalf("Match(%q) = %+v, want the live seat's address", handle, found)
		}
		if !found.Live {
			t.Fatalf("Match(%q).Live = false — a running OpenCode TUI is a live chat", handle)
		}
		if found.Engine != pfmengine.OpenCode {
			t.Fatalf("Match(%q).Engine = %q, want ox", handle, found.Engine)
		}
	}
}

// liveSeats is the filter inject's name lookup runs first; an addressable
// OpenCode row must survive it, and the engine filter must keep it apart from
// the other two engines' seats.
func TestLiveSeatsKeepsALiveOpenCodeRow(t *testing.T) {
	rows := []compose.Row{
		liveOpenCodeRow(),
		{Kind: compose.ResumeOpenCode, ID: "ses_cold", Name: "cold", Socket: "", PaneID: ""},
	}
	if seats := liveSeats(rows, ""); len(seats) != 1 || seats[0].ID != "ses_live" {
		t.Fatalf("liveSeats = %#v, want only the running seat", seats)
	}
	if seats := liveSeats(rows, string(pfmengine.OpenCode)); len(seats) != 1 {
		t.Fatalf("liveSeats(ox) = %#v, want the OpenCode seat", seats)
	}
	if seats := liveSeats(rows, string(pfmengine.Claude)); len(seats) != 0 {
		t.Fatalf("liveSeats(cc) = %#v, want none", seats)
	}
}

// seatTarget is what inject types into: the OpenCode seat's own socket path
// and pane, tagged with its own engine so the busy rule and the binary check
// downstream read the right TUI.
func TestSeatTargetAddressesTheOpenCodePane(t *testing.T) {
	values := paths.Values{TmuxDir: "/tmp/jail/tmux"}
	row := liveOpenCodeRow()
	target, code, detail, err := seatTarget(values, []compose.Row{row}, row.Name)
	if err != nil || code != 0 {
		t.Fatalf("seatTarget = code %d detail %q err %v", code, detail, err)
	}
	if target.SocketPath != "/tmp/jail/tmux/"+row.Socket || target.Pane != "%0" {
		t.Fatalf("target = %+v, want the seat's socket path and pane", target)
	}
	if target.Engine != string(pfmengine.OpenCode) || target.ID != row.ID {
		t.Fatalf("target = %+v, want the OpenCode engine and session id", target)
	}
}

// `pfm chat capture` refuses on chat.Live, and `pfm chat keys` addresses
// PaneTarget(chat) on the same resolved seat — neither has engine logic of its
// own, so both are exactly as broken as addressability is. This is the pin:
// over a live OpenCode row the capture gate opens and the key target is the
// seat's own pane.
func TestCaptureAndKeysAddressALiveOpenCodeSeat(t *testing.T) {
	found, ok, err := Match([]compose.Row{liveOpenCodeRow()}, "P:OPENCODE")
	if err != nil || !ok {
		t.Fatalf("Match = ok %v, err %v", ok, err)
	}
	if !found.Live {
		t.Fatal(`chat capture would refuse this seat with "is not running"`)
	}
	if got := PaneTarget(found); got != "%0" {
		t.Fatalf("PaneTarget = %q, want the seat's own pane", got)
	}
	if !KeyValid("Enter") || !KeyValid("C-c") {
		t.Fatal("chat keys lost its key contract")
	}
}

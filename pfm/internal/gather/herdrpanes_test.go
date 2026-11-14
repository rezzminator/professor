package gather

import (
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// TestHerdrTerminalListedNeverReadsABadListingAsAbsent: a terminal is listed
// or not only on a well-formed pane.list; a malformed or duplicate listing is
// an error, never "not listed".
func TestHerdrTerminalListedNeverReadsABadListingAsAbsent(t *testing.T) {
	const pane = `{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"term-1"}`
	cases := []struct {
		name    string
		list    string
		want    bool
		wantErr string
	}{
		{"listed", `{"result":{"type":"pane_list","panes":[` + pane + `]}}`, true, ""},
		{"absent", `{"result":{"type":"pane_list","panes":[]}}`, false, ""},
		{"duplicate", `{"result":{"type":"pane_list","panes":[` + pane + `,` + pane + `]}}`, false, "duplicate"},
		{"envelope", `{"result":{"type":"pane_info"}}`, false, "want pane_list"},
		{"refused", `{"error":{"code":"internal","message":"down"}}`, false, "list Herdr panes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket, _ := herdrRefusalSocket(t, tc.list, "")
			got, err := HerdrTerminalListed(context.Background(), socket, "term-1")
			if got != tc.want {
				t.Fatalf("HerdrTerminalListed() = %v %v, want %v", got, err, tc.want)
			}
			if tc.wantErr == "" && err != nil ||
				tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("HerdrTerminalListed() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestHerdrSeatBlockedReadsItsOwnPaneOfTheListing: a seat is blocked exactly
// when the pane.list its probe read shows its own pane's agent blocked; a
// working neighbour, a pane the listing lacks and a refused listing are not.
func TestHerdrSeatBlockedReadsItsOwnPaneOfTheListing(t *testing.T) {
	const list = `{"result":{"type":"pane_list","panes":[` +
		`{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"term-1","agent_status":"blocked"},` +
		`{"pane_id":"w1:p2","workspace_id":"w1","terminal_id":"term-2","agent_status":"working"}]}}`
	listed, _ := herdrRefusalSocket(t, list, "")
	refused, _ := herdrRefusalSocket(t, `{"error":{"code":"internal","message":"down"}}`, "")
	seat := func(socket, pane string) fleetdb.Seat {
		return fleetdb.Seat{Backend: nativeHerdrBackend, State: "live", HerdrSocket: socket, HerdrPane: pane}
	}
	rows := probeHerdrSeatRows(context.Background(), []fleetdb.Seat{
		seat(listed, "w1:p1"), seat(listed, "w1:p2"), seat(listed, "w1:p9"), seat(refused, "w1:p1"),
	})
	want := []bool{true, false, false, false}
	if len(rows) != len(want) {
		t.Fatalf("probeHerdrSeatRows() = %d rows, want %d", len(rows), len(want))
	}
	for index, row := range rows {
		if got := row.Blocked(); got != want[index] {
			t.Errorf("seat on %s pane %s (probe err %v): Blocked() = %v, want %v",
				row.Seat.HerdrSocket, row.Seat.HerdrPane, row.Err, got, want[index])
		}
	}
}

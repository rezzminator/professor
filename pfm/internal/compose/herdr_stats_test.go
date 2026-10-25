package compose

import (
	"errors"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// A Herdr seat's process root is its proven seat-host: with none, the Stats
// tab charged every native chat 0 B and 0% CPU.
func TestHerdrSeatRootsItsProvenSeatHost(t *testing.T) {
	seat := func(id, pane string, pid int) fleetdb.Seat {
		return fleetdb.Seat{
			SeatID: id, SessionID: id, Engine: "cc", Backend: "herdr", State: "live",
			HerdrSocket: "/jail/herdr.sock", HerdrPane: pane, SeatHostPID: pid,
		}
	}
	output := Compose(Input{
		Options: Options{View: AllView},
		Transcripts: []store.Transcript{
			{UUID: "proven", Path: "/jail/proven.jsonl"},
			{UUID: "unproven", Path: "/jail/unproven.jsonl"},
			{UUID: "legacy", Path: "/jail/legacy.jsonl"},
		},
		HerdrSeats: []gather.HerdrSeat{
			{Seat: seat("proven", "w1:p1", 4100)},
			{Seat: seat("unproven", "w1:p2", 4200), Err: errors.New("HERDR ERROR: ownership unproved")},
			{Seat: seat("legacy", "w1:p3", 0)},
		},
	})
	roots := map[string][]int{}
	for index := range output.Rows {
		roots[output.Rows[index].ID] = output.Rows[index].PanePIDs
	}
	if !slices.Equal(roots["proven"], []int{4100}) || roots["unproven"] != nil || roots["legacy"] != nil {
		t.Fatalf("seat roots=%v, want only the proven seat-host 4100", roots)
	}
}

func TestLiveSeatKeySeparatesHerdrPanesOnOneSocket(t *testing.T) {
	first := Row{Backend: "herdr", Socket: "/jail/herdr.sock", PaneID: "w1:p1"}
	second := Row{Backend: "herdr", Socket: "/jail/herdr.sock", PaneID: "w1:p2"}
	split := Row{Socket: "cc-1", PaneID: "%3"}
	if LiveSeatKey(first) == LiveSeatKey(second) || LiveSeatKey(split) != "cc-1" {
		t.Fatalf("keys=%q %q %q", LiveSeatKey(first), LiveSeatKey(second), LiveSeatKey(split))
	}
}

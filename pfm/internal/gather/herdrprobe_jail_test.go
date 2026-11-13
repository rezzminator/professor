package gather

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestProveHerdrSeatSupervisorIdentity(t *testing.T) {
	for _, test := range []struct {
		name        string
		terminal    string
		host        bool
		stamp       uint64
		owned, dead bool
	}{
		{"starting", "terminal-one", true, 9, true, false},
		{"retrying-empty-foreground", "terminal-one", true, 9, true, false},
		{"moved", "terminal-one", true, 9, true, false},
		{"recycled-terminal", "terminal-other", true, 9, false, false},
		{"dead", "terminal-one", false, 9, false, true},
		{"reused-pid", "terminal-one", true, 10, false, true},
		{"recycled-during-proof", "terminal-one", true, 9, false, false},
		// The chat exits during the proof: its supervisor is gone by the time its
		// still-listed pane answers with no shell, which proves the seat dead.
		{"shell-gone-supervisor-exited", "terminal-one", true, 9, false, true},
		// The kernel's own reader (sysctl on macOS, /proc on Linux) proves an
		// exited supervisor dead before its recycled pane is asked (forecast C30).
		{"dead-native-reader", "terminal-recycled", false, 9, false, true},
		{"legacy-exact-host", "", true, 0, true, false},
		{"legacy-launch", "", true, 0, true, false},
		{"ambiguous-legacy-launch", "", true, 0, false, false},
		{"historical-lost-launch", "", true, 0, true, false},
		{"historical-failed-launch", "", true, 0, true, false},
		{"failed-live-host-launch", "", true, 0, false, false},
		{"legacy-engine-only", "", false, 0, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			values := paths.Values{StateDB: filepath.Join(dir, "pfm.db")}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			canonical := "w1:p1"
			if test.name == "moved" {
				canonical = "w8:p4"
			}
			response := fmt.Sprintf(
				`{"result":{"type":"pane_list","panes":[{"pane_id":%q,"workspace_id":%q,"terminal_id":"terminal-one","tab_id":"w1:t1","focused":true}]}}`,
				canonical,
				strings.Split(canonical, ":")[0],
			)
			shell := 10
			if test.name == "shell-gone-supervisor-exited" {
				shell = 0
			}
			socket, _ := herdrOwnershipSocketShell(t, response, canonical, shell)
			seat := fleetdb.Seat{
				SeatID:          "owner",
				Backend:         "herdr",
				State:           "live",
				HerdrSocket:     socket,
				HerdrPane:       "w1:p1",
				HerdrWorkspace:  "w1",
				HerdrTerminalID: test.terminal,
			}
			if test.stamp > 0 {
				seat.SeatHostPID = 20
				seat.SeatHostStart = 9
			}
			if test.name == "dead-native-reader" {
				exited := exec.Command("true")
				if err := exited.Run(); err != nil {
					t.Fatalf("run an exiting supervisor: %v", err)
				}
				seat.SeatHostPID = exited.ProcessState.Pid()
			}
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			proc := &fakeProcFS{processes: map[int]fakeProcess{10: {stat: ProcStat{ParentPID: 1, StartTime: 3}}}}
			if test.host {
				proc.processes[20] = fakeProcess{
					cmdline: []string{"/jail/pfm", "internal", "seat-host", "owner"},
					stat:    ProcStat{ParentPID: 10, StartTime: test.stamp},
				}
				if test.stamp == 0 {
					p := proc.processes[20]
					p.stat.StartTime = 9
					proc.processes[20] = p
				}
			}

			if test.name == "legacy-launch" || test.name == "ambiguous-legacy-launch" ||
				(strings.HasPrefix(test.name, "historical-") || test.name == "failed-live-host-launch") {
				p := proc.processes[20]
				p.cmdline = []string{"/jail/pfm", "internal", "launch", "--engine", "cc"}
				proc.processes[20] = p
			}
			if test.name == "ambiguous-legacy-launch" ||
				(strings.HasPrefix(test.name, "historical-") || test.name == "failed-live-host-launch") {
				other := seat
				other.SeatID = "other"
				other.State = "live"
				other.HerdrPane = "w1:p9"
				other.HerdrTerminalID = "terminal-one"
				if test.name == "historical-lost-launch" {
					other.State = "lost"
					other.HerdrPane = seat.HerdrPane
				}
				if test.name == "historical-failed-launch" {
					other.State = "failed"
					other.HerdrPane = seat.HerdrPane
				}
				if test.name == "failed-live-host-launch" {
					other.State = "failed"
					other.SeatHostPID = 20
					other.SeatHostStart = 9
				}
				if err := db.PutSeat(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			var reader ProcFS = proc
			if test.name == "recycled-during-proof" {
				reader = &recyclingHerdrProofProc{ProcFS: proc}
			}
			if test.name == "shell-gone-supervisor-exited" {
				reader = &exitingHerdrProofProc{ProcFS: proc}
			}
			if test.name == "dead-native-reader" {
				reader = nativeProcFS()
			}
			got, owned, err := ProveHerdrSeat(context.Background(), db, reader, seat)
			if owned != test.owned || (err == nil) != (test.owned || test.dead) {
				t.Fatalf("proof owned=%v err=%v; want owned=%v dead=%v", owned, err, test.owned, test.dead)
			}
			if owned &&
				(got.HerdrPane != canonical || got.HerdrTerminalID != "terminal-one" || got.SeatHostPID != 20 || got.SeatHostStart != 9) {
				t.Fatalf("canonical proof=%+v", got)
			}
			stored, _, readErr := db.Seat(context.Background(), seat.SeatID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if owned && stored.HerdrPane != canonical {
				t.Fatalf("move not persisted: %+v", stored)
			}
		})
	}
}

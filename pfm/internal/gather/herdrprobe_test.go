package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestProbeHerdrSeatsKeepsHealthyAndUnreachable(t *testing.T) {
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string `json:"id"`
					Params struct {
						Pane string `json:"pane_id"`
					} `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				_, _ = fmt.Fprintf(
					conn,
					`{"id":%q,"result":{"type":"pane_list","panes":[{"pane_id":"w6:p3","foreground_cwd":"/jail/foreground","cwd":"/jail/shell"}]}}`+"\n",
					req.ID,
				)
			}()
		}
	}()
	seats := []fleetdb.Seat{
		{SeatID: "healthy", Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: "w6:p3"},
		{
			SeatID:      "dead",
			Backend:     "herdr",
			State:       "live",
			HerdrSocket: filepath.Join(t.TempDir(), "dead.sock"),
			HerdrPane:   "w6:p4",
		},
		{SeatID: "tmux", Backend: "tmux", State: "live"},
		{SeatID: "lost", Backend: "herdr", State: "lost"},
	}
	got := probeHerdrSeatRows(context.Background(), seats)
	if len(got) != 2 {
		t.Fatalf("native seats=%+v; want both healthy and unreachable", got)
	}
	if got[0].CWD != "/jail/foreground" || got[0].Err != nil || got[0].Seat.SeatID != "healthy" ||
		got[1].Seat.SeatID != "dead" ||
		got[1].Err == nil ||
		!strings.Contains(got[1].Err.Error(), "HERDR UNREACHABLE "+seats[1].HerdrSocket) {
		t.Fatalf("native seats=%+v", got)
	}
}

func TestProbeHerdrSeatsReusesSocketFailure(t *testing.T) {
	socket := filepath.Join(testjail.ShortRoot(t), "silent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var accepts atomic.Int32
	var peers sync.WaitGroup
	release, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			peers.Add(1)
			go func() { defer peers.Done(); defer func() { _ = conn.Close() }(); <-release }()
		}
	}()
	defer func() { _ = listener.Close(); <-done; close(release); peers.Wait() }()
	rows := probeHerdrSeatRows(context.Background(), []fleetdb.Seat{
		{SeatID: "one", Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: "w1:p1"},
		{SeatID: "two", Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: "w1:p2"},
	})
	if len(rows) != 2 || rows[0].Err == nil || rows[1].Err == nil || accepts.Load() != 1 {
		t.Fatalf("socket failure multiplied per seat: rows=%+v accepts=%d", rows, accepts.Load())
	}
}

func TestProbeHerdrSeatsDistinguishesAnsweredErrors(t *testing.T) {
	for _, test := range []struct{ name, response, detail string }{
		{"missing-pane-refusal", `{"error":{"code":"pane_not_found","message":"pane not found"}}`, "pane_not_found: pane not found"},
		{"invalid-params", `{"error":{"code":"invalid_params","message":"invalid pane id"}}`, "invalid_params: invalid pane id"},
		{"malformed-envelope", `{`, "decode response"},
		{"wrong-list-type", `{"result":{"type":"surprise","panes":[]}}`, "want pane_list"},
		{"missing-list", `{"result":{"type":"pane_list"}}`, "panes"},
		{"null-list", `{"result":{"type":"pane_list","panes":null}}`, "panes"},
		{"missing-pane", `{"result":{"type":"pane_list","panes":[]}}`, "pane_not_found"},
		{"missing-pane-id", `{"result":{"type":"pane_list","panes":[{"cwd":"/jail/shell"}]}}`, "pane_id"},
		{"duplicate-pane-id", `{"result":{"type":"pane_list","panes":[{"pane_id":"w6:p3"},{"pane_id":"w6:p3"}]}}`, "duplicate pane_id"},
		{"invalid-cwd", `{"result":{"type":"pane_list","panes":[{"pane_id":"w6:p3","cwd":12}]}}`, "decode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket, count := herdrProbeListSocket(t, test.response)
			rows := probeHerdrSeatRows(context.Background(), []fleetdb.Seat{
				{SeatID: "failed", Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: "w6:p3"},
				{SeatID: "also-failed", Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: "w6:p4"},
			})
			if len(rows) != 2 {
				t.Fatalf("probe rows=%+v", rows)
			}
			if rows[0].Err == nil || !strings.HasPrefix(rows[0].Err.Error(), "HERDR ERROR "+socket+":") ||
				!strings.Contains(rows[0].Err.Error(), test.detail) {
				t.Fatalf("answered error mislabeled: %v; want HERDR ERROR and %q", rows[0].Err, test.detail)
			}
			if rows[1].Err == nil || count.Load() != 1 {
				t.Fatalf("socket error was not cached visibly: rows=%+v requests=%d", rows, count.Load())
			}
		})
	}
}

func herdrProbeListSocket(t *testing.T, response string) (string, *atomic.Int32) {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int32{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() {
					if err := conn.Close(); err != nil {
						t.Errorf("close pane list peer: %v", err)
					}
				}()
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Errorf("decode pane list request: %v", err)
					return
				}
				count.Add(1)
				reply := response
				if req.Method != "pane.list" {
					reply = `{"error":{"code":"invalid_method","message":"expected pane.list"}}`
				}
				var envelope map[string]any
				if err := json.Unmarshal([]byte(reply), &envelope); err == nil {
					envelope["id"] = req.ID
					body, err := json.Marshal(envelope)
					if err != nil {
						t.Errorf("encode pane list envelope: %v", err)
						return
					}
					reply = string(body)
				}
				if _, err := fmt.Fprintln(conn, reply); err != nil {
					t.Errorf("reply pane list: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	return socket, count
}

func TestProbeHerdrSeatsListsOncePerSocket(t *testing.T) {
	a, ac := herdrProbeListSocket(
		t,
		`{"result":{"type":"pane_list","panes":[{"pane_id":"w6:p3","foreground_cwd":"/jail/foreground","cwd":"/jail/shell"}]}}`,
	)
	b, bc := herdrProbeListSocket(
		t,
		`{"result":{"type":"pane_list","panes":[{"pane_id":"w7:p4","foreground_cwd":null,"cwd":"/jail/other"}]}}`,
	)
	seats := []fleetdb.Seat{}
	for i := 0; i < 40; i++ {
		seats = append(
			seats,
			fleetdb.Seat{
				SeatID:      fmt.Sprintf("a-%d", i),
				Backend:     "herdr",
				State:       "live",
				HerdrSocket: a,
				HerdrPane:   "w6:p3",
			},
			fleetdb.Seat{
				SeatID:      fmt.Sprintf("b-%d", i),
				Backend:     "herdr",
				State:       "live",
				HerdrSocket: b,
				HerdrPane:   "w7:p4",
			},
		)
	}
	seats = append(
		seats,
		fleetdb.Seat{SeatID: "gone", Backend: "herdr", State: "live", HerdrSocket: a, HerdrPane: "w6:p9"},
		fleetdb.Seat{
			SeatID:      "dead",
			Backend:     "herdr",
			State:       "live",
			HerdrSocket: filepath.Join(t.TempDir(), "dead.sock"),
			HerdrPane:   "w6:p1",
		},
		fleetdb.Seat{
			SeatID:  "tmux",
			Backend: "tmux",
			State:   "live",
		},
		fleetdb.Seat{SeatID: "lost", Backend: "herdr", State: "lost"},
	)
	rows := probeHerdrSeatRows(context.Background(), seats)
	if len(rows) != 82 {
		t.Fatalf("healthy plus failed rows=%d; want 82", len(rows))
	}
	for i, row := range rows[:80] {
		want := "/jail/foreground"
		if i%2 != 0 {
			want = "/jail/other"
		}
		if row.Err != nil || row.CWD != want || row.Seat.SeatID != seats[i].SeatID {
			t.Fatalf("healthy row %d=%+v; want cwd=%q", i, row, want)
		}
	}
	if rows[80].Err == nil || !strings.Contains(rows[80].Err.Error(), "HERDR ERROR "+a+": pane_not_found") ||
		rows[81].Err == nil ||
		!strings.Contains(rows[81].Err.Error(), "HERDR UNREACHABLE") {
		t.Fatalf("missing pane and dead socket collapsed: missing=%v dead=%v", rows[80].Err, rows[81].Err)
	}
	if ac.Load() != 1 || bc.Load() != 1 {
		t.Fatalf("enumeration multiplied by seats: a=%d b=%d; want 1 each", ac.Load(), bc.Load())
	}
}

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
			socket, _ := herdrOwnershipSocket(t, response, canonical)
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

func herdrOwnershipSocket(t *testing.T, list, pane string, beforeInfo ...func()) (string, *atomic.Int32) {
	t.Helper()
	return herdrOwnershipSocketShell(t, list, pane, 10, beforeInfo...)
}

func herdrOwnershipSocketShell(
	t *testing.T,
	list, pane string,
	shell int,
	beforeInfo ...func(),
) (string, *atomic.Int32) {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				calls.Add(1)
				var body map[string]any
				if req.Method == "pane.list" {
					_ = json.Unmarshal([]byte(list), &body)
				} else {
					for _, before := range beforeInfo {
						before()
					}
					body = map[string]any{
						"result": map[string]any{
							"type":         "pane_process_info",
							"process_info": map[string]any{"pane_id": pane, "shell_pid": shell},
						},
					}
				}
				body["id"] = req.ID
				_ = json.NewEncoder(conn).Encode(body)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return socket, &calls
}

func TestProvedListingKeepsMovedSupervisor(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(readOnly), func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "pfm.db")
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: dbPath})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			socket, _ := herdrOwnershipSocket(
				t,
				`{"result":{"type":"pane_list","panes":[{"pane_id":"w8:p4","workspace_id":"w8","terminal_id":"stable","cwd":"/jail/project"}]}}`,
				"w8:p4",
			)
			seat := fleetdb.Seat{
				SeatID:          "owner",
				Backend:         "herdr",
				State:           "live",
				HerdrSocket:     socket,
				HerdrPane:       "w1:p1",
				HerdrWorkspace:  "w1",
				HerdrTerminalID: "stable",
				SeatHostPID:     20,
				SeatHostStart:   9,
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			proc := &fakeProcFS{
				processes: map[int]fakeProcess{
					10: {stat: ProcStat{ParentPID: 1, StartTime: 3}},
					20: {
						cmdline: []string{"pfm", "internal", "seat-host", "owner"},
						stat:    ProcStat{ParentPID: 10, StartTime: 9},
					},
				},
			}
			proofDB := db
			if readOnly {
				proofDB = fleetdb.OpenSharedStateReadOnly(ctx, paths.Values{StateDB: dbPath})
				defer func() {
					if err := proofDB.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			rows := proveHerdrSeats(ctx, proofDB, proc, probeHerdrSeatRows(ctx, []fleetdb.Seat{seat}), readOnly)
			if len(rows) != 1 || rows[0].Err != nil || rows[0].Seat.HerdrPane != "w8:p4" ||
				rows[0].CWD != "/jail/project" {
				t.Fatalf("moved listing=%+v", rows)
			}
			stored, _, err := db.Seat(ctx, seat.SeatID)
			wantPane := "w8:p4"
			if readOnly {
				wantPane = "w1:p1"
			}
			if err != nil || stored.HerdrPane != wantPane {
				t.Fatalf("durable moved listing=%+v err=%v", stored, err)
			}
		})
	}
}

func TestProveHerdrSeatReadOnlyUnchangedOwnership(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		moved, metadata bool
		write, pending  bool
	}{
		{name: "unchanged"},
		{name: "moved", moved: true},
		{name: "concurrent-metadata", metadata: true},
		{name: "moved-concurrent-metadata", moved: true, metadata: true, write: true},
		{name: "moved-concurrent-end", moved: true, pending: true, write: true},
		{name: "unchanged-concurrent-end", pending: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
			writer := fleetdb.OpenSharedState(ctx, values)
			defer func() {
				if err := writer.Close(); err != nil {
					t.Error(err)
				}
			}()
			pane := "w1:p1"
			if scenario.moved {
				pane = "w9:p2"
			}
			var seat fleetdb.Seat
			socket, _ := herdrOwnershipSocket(
				t,
				fmt.Sprintf(
					`{"result":{"type":"pane_list","panes":[{"pane_id":%q,"workspace_id":%q,"terminal_id":"stable"}]}}`,
					pane,
					strings.Split(pane, ":")[0],
				),
				pane,
				func() {
					if scenario.metadata || scenario.pending {
						next := seat
						if scenario.metadata {
							next.State = "failed"
							next.ReloadPending = true
						}
						next.EndPending = scenario.pending
						if err := writer.UpdateSeat(ctx, seat, next); err != nil {
							t.Error(err)
						}
					}
				},
			)
			seat = fleetdb.Seat{
				SeatID:          "owner",
				Backend:         "herdr",
				State:           "live",
				HerdrSocket:     socket,
				HerdrPane:       "w1:p1",
				HerdrWorkspace:  "w1",
				HerdrTerminalID: "stable",
				SeatHostPID:     20,
				SeatHostStart:   9,
			}
			if err := writer.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			reader := fleetdb.OpenSharedStateReadOnly(ctx, values)
			defer func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
			}()
			proc := &fakeProcFS{
				processes: map[int]fakeProcess{
					10: {stat: ProcStat{ParentPID: 1, StartTime: 3}},
					20: {
						cmdline: []string{"pfm", "internal", "seat-host", "owner"},
						stat:    ProcStat{ParentPID: 10, StartTime: 9},
					},
				},
			}
			proofDB := reader
			if scenario.write {
				proofDB = writer
			}
			got, owned, err := ProveHerdrSeat(ctx, proofDB, proc, seat)
			if scenario.pending {
				if err == nil || owned {
					t.Fatalf(
						"concurrent end intent accepted as proved owner: owned=%t seat=%+v err=%v",
						owned,
						got,
						err,
					)
				}
				return
			}
			want := seat
			if scenario.metadata {
				want.State = "failed"
				want.ReloadPending = true
			}
			if scenario.write {
				want.HerdrPane = pane
				want.HerdrWorkspace = strings.Split(pane, ":")[0]
			}
			if (!scenario.moved || scenario.write) && (err != nil || !owned || got != want) {
				t.Fatalf(
					"unchanged ownership rejected or metadata lost: owned=%t seat=%+v want=%+v err=%v",
					owned,
					got,
					want,
					err,
				)
			}
			if scenario.moved && !scenario.write && (!errors.Is(err, ErrHerdrProofPersistence) || owned) {
				t.Fatalf("unpersisted move accepted through read-only state: owned=%t seat=%+v err=%v", owned, got, err)
			}
		})
	}
}

type recyclingHerdrProofProc struct {
	ProcFS
	hostReads int
}

func (proc *recyclingHerdrProofProc) Stat(pid int) (ProcStat, error) {
	stat, err := proc.ProcFS.Stat(pid)
	if pid == 20 {
		proc.hostReads++
		if proc.hostReads >= 3 {
			stat.StartTime++
		}
	}
	return stat, err
}

func TestProveHerdrSeatClosure(t *testing.T) {
	for _, mode := range []string{"shell-negative", "shell-zero", "shell-one", "valid", "socket-rebind"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "state.db")})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			shell := 10
			switch mode {
			case "shell-negative":
				shell = -1
			case "shell-zero":
				shell = 0
			case "shell-one":
				shell = 1
			}
			list := `{"type":"pane_list","panes":[{"pane_id":"w8:p4","workspace_id":"w8","tab_id":"w8:t1","terminal_id":"stable","focused":true}]}`
			socket, _ := herdrOwnershipSocketShell(t, `{"result":`+list+`}`, "w8:p4", shell)
			seat := fleetdb.Seat{
				SeatID:          "owner",
				Backend:         "herdr",
				State:           "live",
				HerdrSocket:     socket,
				HerdrPane:       "w8:p4",
				HerdrWorkspace:  "w8",
				HerdrTerminalID: "stable",
				SeatHostPID:     20,
				SeatHostStart:   9,
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			proc := &fakeProcFS{
				processes: map[int]fakeProcess{
					10: {stat: ProcStat{ParentPID: 1, StartTime: 3}},
					20: {
						cmdline: []string{"pfm", "internal", "seat-host", "owner"},
						stat:    ProcStat{ParentPID: 10, StartTime: 9},
					},
				},
			}
			current := seat
			var got fleetdb.Seat
			var owned bool
			var err error
			if mode == "socket-rebind" {
				current.HerdrSocket, _ = herdrOwnershipSocket(t, `{"result":`+list+`}`, "w8:p4")
				current.HerdrPane, current.HerdrWorkspace = "w9:p2", "w9"
				if err := db.UpdateSeat(ctx, seat, current); err != nil {
					t.Fatal(err)
				}
				got, owned, err = proveHerdrSeatSnapshot(ctx, db, proc, seat, json.RawMessage(list), false)
			} else {
				got, owned, err = ProveHerdrSeat(ctx, db, proc, seat)
			}
			if mode == "valid" {
				if err != nil || !owned || got != seat {
					t.Fatalf("valid proof=%+v owned=%t err=%v", got, owned, err)
				}
				return
			}
			stored, found, readErr := db.Seat(ctx, seat.SeatID)
			if err == nil || owned || readErr != nil || !found || stored != current {
				t.Fatalf(
					"unsafe %s proof=%+v owned=%t err=%v stored=%+v want=%+v read=%v",
					mode,
					got,
					owned,
					err,
					stored,
					current,
					readErr,
				)
			}
		})
	}
}

// procargs2 returns EINVAL for both foreign and vanished processes on Darwin.
// The independent kill and kinfo identity probes decide which evidence it is.
type unreadableSupervisorProc struct {
	*fakeProcFS
	probeErr, identityErr error
	uid                   uint32
}

func (p unreadableSupervisorProc) Cmdline(pid int) ([]string, error) {
	return nil, processReadError(pid, syscall.EINVAL, p.probeErr)
}

func (p unreadableSupervisorProc) ProcessIdentity(_ int) (ProcessIdentity, error) {
	return ProcessIdentity{EffectiveUID: p.uid}, p.identityErr
}

func TestHerdrSupervisorAbsenceDistinguishesDarwinUnreadability(t *testing.T) {
	for _, tc := range []struct {
		name            string
		uid             uint32
		probe, identity error
		absent          bool
	}{
		{"foreign EINVAL", uint32(os.Geteuid()) + 1, syscall.EPERM, nil, true},
		{"same uid EINVAL", uint32(os.Geteuid()), nil, nil, false},
		{"vanished EINVAL", uint32(os.Geteuid()), syscall.ESRCH, nil, true},
		{"unknown identity", uint32(os.Geteuid()) + 1, syscall.EPERM, syscall.EIO, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := unreadableSupervisorProc{
				fakeProcFS:  &fakeProcFS{processes: map[int]fakeProcess{42: {}}},
				uid:         tc.uid,
				probeErr:    tc.probe,
				identityErr: tc.identity,
			}
			absent, err := HerdrSupervisorAbsent(
				proc,
				fleetdb.Seat{SeatID: "restored", HerdrSocket: "fixture", HerdrPane: "w1:p1"},
			)
			if absent != tc.absent || (err == nil) != tc.absent {
				t.Fatalf("absence=%t err=%v want absence=%t", absent, err, tc.absent)
			}
		})
	}
}

// unreadableHostProc models a saved supervisor whose kernel birth cannot be
// read: unknown evidence, never positive death.
type unreadableHostProc struct {
	ProcFS
	pid int
}

func (proc unreadableHostProc) Stat(pid int) (ProcStat, error) {
	if pid == proc.pid {
		return ProcStat{}, fs.ErrPermission
	}
	return proc.ProcFS.Stat(pid)
}

func TestProvedListingRetiresDeadSeatOnly(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("read-only=%t", readOnly), func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "pfm.db")
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: dbPath})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			// The dead seat's old pane id now hosts a recycled occupant.
			socket, _ := herdrOwnershipSocket(
				t,
				`{"result":{"type":"pane_list","panes":[`+
					`{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"healthy","cwd":"/jail/healthy"},`+
					`{"pane_id":"w1:p2","workspace_id":"w1","terminal_id":"recycled","cwd":"/jail/recycled"},`+
					`{"pane_id":"w1:p3","workspace_id":"w1","terminal_id":"unknown","cwd":"/jail/unknown"}]}}`,
				"w1:p1",
			)
			seat := func(id, pane, terminal string, pid int) fleetdb.Seat {
				return fleetdb.Seat{
					SeatID: id, SessionID: id, Engine: "cc", Backend: "herdr", State: "live",
					HerdrSocket: socket, HerdrPane: pane, HerdrWorkspace: "w1", HerdrTerminalID: terminal,
					SeatHostPID: pid, SeatHostStart: 9,
				}
			}
			seats := []fleetdb.Seat{
				seat("healthy", "w1:p1", "healthy", 20),
				seat("dead", "w1:p2", "dead-terminal", 30),
				seat("unknown", "w1:p3", "unknown", 40),
			}
			for _, item := range seats {
				if err := db.PutSeat(ctx, item); err != nil {
					t.Fatal(err)
				}
			}
			proc := unreadableHostProc{pid: 40, ProcFS: &fakeProcFS{
				processes: map[int]fakeProcess{
					10: {stat: ProcStat{ParentPID: 1, StartTime: 3}},
					20: {
						cmdline: []string{"pfm", "internal", "seat-host", "healthy"},
						stat:    ProcStat{ParentPID: 10, StartTime: 9},
					},
				},
			}}
			proofDB := db
			if readOnly {
				proofDB = fleetdb.OpenSharedStateReadOnly(ctx, paths.Values{StateDB: dbPath})
				defer func() {
					if err := proofDB.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			rows := proveHerdrSeats(ctx, proofDB, proc, probeHerdrSeatRows(ctx, seats), readOnly)
			if len(rows) != 3 {
				t.Fatalf("listing dropped a seat: %+v", rows)
			}
			healthy, dead, unknown := rows[0], rows[1], rows[2]
			if healthy.Err != nil || healthy.Seat.State != "live" || healthy.CWD != "/jail/healthy" {
				t.Fatalf("healthy seat lost beside a dead one: %+v", healthy)
			}
			if dead.Err == nil || !strings.Contains(dead.Err.Error(), "HERDR DEAD dead") {
				t.Fatalf("dead seat diagnostic lost: %+v", dead)
			}
			if dead.Seat.State == "live" {
				t.Fatalf("positively dead seat still presents as live: %+v", dead.Seat)
			}
			if dead.CWD != "" || dead.Seat.HerdrTerminalID != "dead-terminal" || dead.Seat.HerdrPane != "w1:p2" {
				t.Fatalf("dead seat imported recycled pane metadata: %+v", dead)
			}
			if unknown.Err == nil || !strings.Contains(unknown.Err.Error(), "HERDR ERROR") ||
				unknown.Seat.State != "live" || unknown.CWD != "" {
				t.Fatalf("unknown ownership classified as dead or healthy: %+v", unknown)
			}
			for _, want := range seats {
				stored, found, err := db.Seat(ctx, want.SeatID)
				if err != nil || !found || stored.State != "live" || stored.LostNote != "" {
					t.Fatalf("listing mutated durable seat %s: %+v found=%t err=%v", want.SeatID, stored, found, err)
				}
			}
		})
	}
}

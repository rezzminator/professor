package gather

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
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
	got := ProbeHerdrSeats(context.Background(), seats)
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
	rows := ProbeHerdrSeats(context.Background(), []fleetdb.Seat{
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
			rows := ProbeHerdrSeats(context.Background(), []fleetdb.Seat{
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
	rows := ProbeHerdrSeats(context.Background(), seats)
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

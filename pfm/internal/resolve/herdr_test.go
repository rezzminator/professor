package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestWhoamiHerdrRequiresPersistedSeatAndEngineAncestor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store bool
		pid   int
		want  bool
	}{
		{"verified", true, 40, true}, {"unnamed", true, 40, true}, {"unrelated", true, 88, false}, {"unrecorded", false, 40, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
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
							ID string `json:"id"`
						}
						if json.NewDecoder(conn).Decode(&req) != nil {
							return
						}
						_, _ = fmt.Fprintf(
							conn,
							`{"id":%q,"ok":true,"result":{"process_info":{"pane_id":"w6:p3","foreground_processes":[{"pid":%d,"argv0":"claude"}]}}}`+"\n",
							req.ID,
							tc.pid,
						)
					}()
				}
			}()
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			state := fleetdb.OpenSharedState(context.Background(), values)
			t.Cleanup(func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			})
			name, session := "REVIEW", "REVIEW"
			if tc.name == "unnamed" {
				name, session = "", "seat1"
			}
			if tc.store {
				if err := state.PutSeat(
					context.Background(),
					fleetdb.Seat{
						State:          "live",
						SeatID:         "seat1",
						SessionID:      "sid1",
						Engine:         "cc",
						Backend:        "herdr",
						HerdrSocket:    socket,
						HerdrPane:      "w6:p3",
						HerdrWorkspace: "w6",
						Name:           name,
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			identifier, err := NewWhoami(
				WhoamiDependencies{
					Processes: fakeProcTree{parents: map[int]int{99: 40, 40: 1}},
					SelfPID:   99,
					TmuxDir:   t.TempDir(),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := identifier.Identify(context.Background())
			if !tc.want {
				if !errors.Is(err, ErrNoTmux) || !strings.Contains(err.Error(), "nor a pfm Herdr seat") {
					t.Fatalf("identity=%+v err=%v", identity, err)
				}
				return
			}
			if err != nil || identity.Backend != "herdr" || identity.SeatID != "seat1" || identity.ID != "sid1" ||
				identity.Session != session ||
				identity.SocketPath != socket ||
				identity.Pane != "w6:p3" {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
		})
	}
}

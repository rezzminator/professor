package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestKillServerRecordsAChatStateTransition: KillServer walks the state door
// (spec § Middleware, `state`) — live to ended, comp=state, kind=chat —
// never the socket name it closed.
func TestKillServerRecordsAChatStateTransition(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	tmuxDir := filepath.Join(root, "tmux")
	sidDir := filepath.Join(root, "sid")
	for _, dir := range []string{tmuxDir, sidDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	socket := "cc-obs-test-1-1"
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})

	resolved := paths.Values{
		TmuxDir: tmuxDir,
		SIDDir:  sidDir,
		StateDB: filepath.Join(root, "pfm.db"),
	}
	ctx, recorder := obs.Test(t)
	if err := KillServer(ctx, resolved, socket); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "chat" {
			continue
		}
		if next, _ := record.Field("next"); next == "ended" {
			found = true
		}
	}
	if !found {
		t.Fatalf("KillServer() wrote no chat->ended transition: %s", recorder.Raw())
	}
}

func TestKillServerRefusesNativeSocket(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "herdr.sock")
	if err := os.WriteFile(socket, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := KillServer(
		context.Background(),
		paths.Values{TmuxDir: root, StateDB: filepath.Join(root, "state.db")},
		socket,
	)
	body, readErr := os.ReadFile(socket)
	if err == nil || !strings.Contains(err.Error(), "use EndResolved for a native Herdr seat") || readErr != nil ||
		string(body) != "preserve" {
		t.Fatalf("native end err=%v body=%q read=%v", err, body, readErr)
	}
}

func TestEndHerdrClosesOnlyItsSeat(t *testing.T) {
	for _, scenario := range []struct {
		name, state, refusal string
		want                 int
	}{
		{name: "live", state: "live"},
		{name: "early startup without session", state: "failed"},
		{name: "lost missing pane", state: "lost", refusal: "pane_not_found", want: 1},
		{name: "failed missing pane", state: "failed", refusal: "pane_not_found", want: 1},
		{name: "close error", state: "live", refusal: "permission_denied", want: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testjail.Fleet(t)
			calls := make(chan nativeChatRequest, 8)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				calls <- req
				return map[string]any{"type": "ok"}, scenario.refusal
			})
			runtime, db, seat := nativeChatSeat(t, socket, scenario.state)
			seat = nativeChatHost(t, db, seat, true)
			if scenario.name == "early startup without session" {
				seat.SessionID = ""
				if err := db.PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
			}
			other := seat
			other.SeatID = "other-seat"
			other.SessionID = "other-session"
			other.HerdrPane = "w6:p4"
			if err := db.PutSeat(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			code := 0
			if err := endHerdr(
				context.Background(),
				headless.Chat{
					Backend: "herdr",
					SeatID:  seat.SeatID,
					ID:      seat.SessionID,
					Socket:  socket,
					Pane:    seat.HerdrPane,
				},
				runtime.Paths,
			); err != nil {
				code = 1
			}
			if code != scenario.want {
				t.Fatalf("native worker code=%d want=%d", code, scenario.want)
			}

			_, found, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || found != (scenario.want != 0) {
				t.Fatalf("ended seat found=%v err=%v", found, err)
			}
			if _, found, err := db.Seat(context.Background(), other.SeatID); err != nil || !found {
				t.Fatalf("other seat found=%v err=%v", found, err)
			}
			if scenario.want == 0 {
				if _, err := db.ReadSeatRun(seat.SeatID); err == nil {
					t.Fatal("ended seat recipe still present")
				}
			}
			select {
			case req := <-calls:
				if req.Method != "pane.close" || string(req.Params["pane_id"]) != `"w6:p3"` {
					t.Fatalf("close request=%+v", req)
				}
			default:
				t.Fatal("pane.close was not sent")
			}
		})
	}
}

func TestNativeEndRefusesRecycledOwnerAndKeepsRecoverableIntent(t *testing.T) {
	for _, mode := range []string{"recycled", "unknown-close", "moved"} {
		t.Run(mode, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if req.Method == "pane.list" && mode == "recycled" {
					return map[string]any{
						"type":  "pane_list",
						"panes": []any{testjail.HerdrPaneInfo("w6:p9")["pane"]},
					}, ""
				}
				pane := "w6:p3"
				if mode == "moved" {
					pane = "w9:p2"
				}
				if value, code, ok := nativeChatProof(req, pane); ok {
					return value, code
				}
				if req.Method == "pane.close" {
					if mode == "unknown-close" {
						return nil, "permission_denied"
					}
					if string(req.Params["pane_id"]) != `"w9:p2"` {
						t.Errorf("end used stale target: %s", req.Params["pane_id"])
					}
					return map[string]any{"type": "ok"}, ""
				}
				return nil, "unexpected_method"
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, true)
			seat.EndPending = mode != "recycled"
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			err := endHerdr(
				context.Background(),
				headless.Chat{
					Backend: "herdr",
					SeatID:  seat.SeatID,
					ID:      seat.SessionID,
					Socket:  socket,
					Pane:    seat.HerdrPane,
				},
				runtime.Paths,
			)
			current, found, readErr := db.Seat(context.Background(), seat.SeatID)
			if mode == "moved" {
				if err != nil || found || readErr != nil {
					t.Fatalf("canonical end err=%v found=%v read=%v", err, found, readErr)
				}
			} else if err == nil || !found || readErr != nil || current.EndPending != seat.EndPending {
				t.Fatalf("unsafe end err=%v found=%v read=%v seat=%+v", err, found, readErr, current)
			}
		})
	}
}

func TestNativePendingEndReconcilesOnlyConfirmedDisappearance(t *testing.T) {
	for _, reply := range []string{"absent", "present", "malformed", "transport"} {
		t.Run(reply, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if req.Method != "pane.list" {
					return nil, "unexpected_mutation"
				}
				switch reply {
				case "absent":
					return map[string]any{"type": "pane_list", "panes": []any{}}, ""
				case "present":
					return map[string]any{
						"type":  "pane_list",
						"panes": []any{testjail.HerdrPaneInfo("w6:p3")["pane"]},
					}, ""
				case "malformed":
					return map[string]any{"type": "ok"}, ""
				default:
					return nil, "permission_denied"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, false)
			seat.EndPending = true
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			err := endHerdr(
				context.Background(),
				headless.Chat{Backend: "herdr", SeatID: seat.SeatID, ID: seat.SessionID},
				runtime.Paths,
			)
			current, found, readErr := db.Seat(context.Background(), seat.SeatID)
			if reply == "absent" {
				if err != nil || found || readErr != nil {
					t.Fatalf("pending disappearance not retired err=%v found=%v read=%v", err, found, readErr)
				}
			} else if err == nil || !found || !current.EndPending || readErr != nil {
				t.Fatalf(
					"unknown discarded pending owner err=%v found=%v seat=%+v read=%v",
					err,
					found,
					current,
					readErr,
				)
			}
		})
	}
}

func TestNativePendingEndReprovesDowngradedBinding(t *testing.T) {
	for _, entry := range []string{"lookup", "schedule"} {
		t.Run(entry, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if req.Method == "pane.list" {
					return map[string]any{
						"type":  "pane_list",
						"panes": []any{testjail.HerdrPaneInfo("w9:p8")["pane"]},
					}, ""
				}
				return nil, "unexpected_method"
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, true)
			seat.EndPending = true
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			var err error
			if entry == "lookup" {
				_, _, err = PendingNativeEnd(context.Background(), seat.SeatID, runtime.Paths)
			} else {
				err = ScheduleHerdrEnd(
					context.Background(),
					headless.Chat{ID: seat.SessionID, SeatID: seat.SeatID},
					runtime,
				)
			}
			if err == nil {
				t.Fatalf("stale end_pending binding authorized %s", entry)
			}
			current, found, readErr := db.Seat(context.Background(), seat.SeatID)
			if readErr != nil || !found || current != seat {
				t.Fatalf("stale owner not retained: %+v %v", current, readErr)
			}
		})
	}
}

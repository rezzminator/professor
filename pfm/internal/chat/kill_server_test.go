package chat

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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

func TestEndResolvedNativeClosesOnlyItsSeat(t *testing.T) {
	for _, scenario := range []struct {
		name, state, refusal string
		want                 int
	}{
		{name: "live", state: "live"},
		{name: "lost missing pane", state: "lost", refusal: "pane_not_found"},
		{name: "failed missing pane", state: "failed", refusal: "pane_not_found"},
		{name: "close error", state: "live", refusal: "permission_denied", want: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testjail.Fleet(t)
			calls := make(chan nativeChatRequest, 8)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				calls <- req
				return map[string]any{"type": "ok"}, scenario.refusal
			})
			runtime, db, seat := nativeChatSeat(t, socket, scenario.state)
			other := seat
			other.SeatID = "other-seat"
			other.SessionID = "other-session"
			other.HerdrPane = "w6:p4"
			if err := db.PutSeat(context.Background(), other); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := EndResolved(
				context.Background(),
				headless.Chat{
					Backend: "herdr",
					SeatID:  seat.SeatID,
					ID:      seat.SessionID,
					Engine:  pfmengine.Claude,
					Live:    scenario.state == "live",
					Socket:  socket,
					Pane:    seat.HerdrPane,
				},
				&stdout,
				&stderr,
				&runtime,
			)
			if code != scenario.want {
				t.Fatalf(
					"native end code=%d want=%d out=%q err=%q",
					code,
					scenario.want,
					stdout.String(),
					stderr.String(),
				)
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

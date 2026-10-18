package picker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The picker's reboot kills the seat's server and reopens the SAME chat. The
// reopened row must belong to the same engine: the if/else this used to spell
// sent every non-Codex live row to ResumeClaude, so rebooting a live OpenCode
// chat would have relaunched it as a Claude resume of an id Claude never had.
func TestRebootRowDemotesToTheSeatsOwnEngine(t *testing.T) {
	root := t.TempDir()
	resolved := paths.Values{
		TmuxDir: filepath.Join(root, "tmux"),
		SIDDir:  filepath.Join(root, "sid"),
	}
	for _, test := range []struct {
		kind   compose.Kind
		socket string
		want   compose.Kind
	}{
		{compose.LiveClaude, "cc-1-2-3", compose.ResumeClaude},
		{compose.LiveCodex, "cx-1-2-3", compose.ResumeCodex},
		{compose.LiveOpenCode, "ox-1-2-3", compose.ResumeOpenCode},
	} {
		var stderr bytes.Buffer
		row, err := rebootRow(context.Background(), resolved, compose.Row{
			Kind: test.kind, ID: "seat-id", Socket: test.socket,
			SessionName: test.socket, PaneID: "%0", CWD: "/work",
		}, &stderr)
		if err != nil {
			t.Fatalf("rebootRow(%s): %v", test.kind, err)
		}
		if row.Kind != test.want {
			t.Fatalf("rebootRow(%s).Kind = %s, want %s", test.kind, row.Kind, test.want)
		}
		if compose.EngineForKind(row.Kind) != compose.EngineForKind(test.kind) {
			t.Fatalf("rebootRow(%s) changed engine to %s", test.kind, compose.EngineForKind(row.Kind))
		}
		if row.Socket != "" || row.PaneID != "" || row.SessionName != "" {
			t.Fatalf("rebootRow(%s) kept a dead address: %+v", test.kind, row)
		}
	}
}

func TestNativeLifecycleUsesSelectedPane(t *testing.T) {
	jailTest(t)
	marker := filepath.Join(t.TempDir(), "worker")
	t.Setenv("PFM_PICKER_END_WORKER", marker)
	resolved := jailPaths(t)
	socket := filepath.Join(testjail.ShortRoot(t), "picker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req map[string]any
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				var result any
				switch req["method"] {
				case "pane.list":
					result = map[string]any{"type": "pane_list", "panes": []any{}}
				default:
					result = map[string]any{"type": "ok"}
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": req["id"], "result": result})
			}()
		}
	}()

	row := compose.Row{
		Kind:    compose.LiveClaude,
		ID:      "native",
		Name:    "NATIVE",
		Backend: "herdr",
		SeatID:  "seat1",
		Socket:  socket,
		PaneID:  "w6:p3",
	}
	db := fleetdb.OpenSharedState(context.Background(), resolved)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := db.PutSeat(
		context.Background(),
		fleetdb.Seat{
			SeatID:          row.SeatID,
			SessionID:       row.ID,
			Engine:          "cc",
			Backend:         row.Backend,
			State:           "live",
			HerdrSocket:     socket,
			HerdrPane:       row.PaneID,
			HerdrTerminalID: "fixture-" + row.PaneID, SeatHostPID: 900003, SeatHostStart: 100,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := deactivateApplier(context.Background(), resolved)(row); err != nil {
		t.Fatalf("native deactivate: %v", err)
	}
	waitPickerEndWorker(t, marker, row.SeatID)
	current, found, err := db.Seat(context.Background(), row.SeatID)
	if err != nil || !found || !current.EndPending || current.HerdrPane != row.PaneID {
		t.Fatalf("selected native intent=%+v found=%v err=%v", current, found, err)
	}

	row.Kind = compose.ResumeClaude
	var stderr bytes.Buffer
	got, err := rebootRow(context.Background(), resolved, row, &stderr)
	if err != nil || !reflect.DeepEqual(got, row) {
		t.Fatalf("dead native reopen changed address: %+v err=%v", got, err)
	}
}

func TestNativeLiveRebootUsesSamePaneReload(t *testing.T) {
	jailTest(t)
	resolved := jailPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	row := compose.Row{
		Kind:    compose.LiveClaude,
		ID:      "native",
		Backend: "herdr",
		Socket:  "/fixture/herdr.sock",
		PaneID:  "w6:p3",
	}
	_, err := rebootRow(
		ctx,
		resolved,
		row,
		&bytes.Buffer{},
		pfmconfig.Runtime{Paths: resolved, Config: pfmconfig.Defaults(resolved.Home, nil)},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("native reload error=%v want canceled delegated reload", err)
	}
}

// A test selection flag bounds the old binary to this assertion, not the suite.
func TestNativeEndWorkerInvocationIsOwned(t *testing.T) {
	if os.Getenv("PFM_PICKER_WORKER_PROBE") == "1" {
		t.Fatal("unowned worker re-entered picker test suite")
	}
	marker := filepath.Join(t.TempDir(), "worker")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestNativeEndWorkerInvocationIsOwned$",
		"chat",
		"end",
		"--native-end-worker",
		"probe-seat",
	)
	command.Env = append(os.Environ(), "PFM_PICKER_WORKER_PROBE=1", "PFM_PICKER_END_WORKER="+marker)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("worker invocation was not owned: %v: %s", err, out)
	}
	body, err := os.ReadFile(marker)
	if err != nil || !strings.HasSuffix(string(body), " probe-seat") {
		t.Fatalf("worker acknowledgement=%q err=%v", body, err)
	}
}

func waitPickerEndWorker(t *testing.T, marker, seat string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		body, err := os.ReadFile(marker)
		if err == nil {
			fields := strings.Fields(string(body))
			if len(fields) != 2 || fields[1] != seat {
				t.Fatalf("worker acknowledgement=%q", body)
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			child, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			state, err := child.Wait()
			if err != nil || !state.Success() {
				t.Fatalf("owned worker exit=%v err=%v", state, err)
			}
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("owned worker acknowledgement timed out")
		case <-tick.C:
		}
	}
}

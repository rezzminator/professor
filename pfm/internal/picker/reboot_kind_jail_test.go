package picker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

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
			// The marker is written only on the fixture's exit-0 path; the
			// runner's reaper collects the detached worker, so it must be gone.
			for syscall.Kill(pid, 0) == nil {
				select {
				case <-deadline.C:
					t.Fatalf("owned worker %d still in the process table after its marker", pid)
				case <-tick.C:
				}
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

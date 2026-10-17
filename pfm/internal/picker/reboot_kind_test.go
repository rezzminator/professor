package picker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"testing"

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
	resolved := jailPaths(t)
	socket := filepath.Join(testjail.ShortRoot(t), "picker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	requests := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req map[string]any
		if json.NewDecoder(conn).Decode(&req) != nil {
			return
		}
		requests <- req
		_, _ = fmt.Fprintf(
			conn,
			`{"id":%q,"ok":true,"result":{"type":"pane_closed","pane_id":"w6:p3"}}`+"\n",
			req["id"],
		)
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
			SeatID:      row.SeatID,
			SessionID:   row.ID,
			Engine:      "cc",
			Backend:     row.Backend,
			State:       "live",
			HerdrSocket: socket,
			HerdrPane:   row.PaneID,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := deactivateApplier(context.Background(), resolved)(row); err != nil {
		t.Fatalf("native deactivate: %v", err)
	}
	req := <-requests
	if req["method"] != "pane.close" || req["params"].(map[string]any)["pane_id"] != row.PaneID {
		t.Fatalf("selected native pane=%v", req)
	}
	if _, found, err := db.Seat(context.Background(), row.SeatID); err != nil || found {
		t.Fatalf("ended seat found=%v err=%v", found, err)
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

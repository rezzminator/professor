package hookentry

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestSeatHostRejectsMissingSeat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if rc := SeatHost(
		nil,
		&stdout,
		&stderr,
		config.Runtime{},
	); rc != 1 ||
		!strings.Contains(stderr.String(), "expected one seat id") {
		t.Fatalf("SeatHost = %d, %q", rc, stderr.String())
	}
}

func TestSeatCommandRefreshesFullPaneContext(t *testing.T) {
	for _, saved := range [][]string{
		{"KEEP=engine"},
		{"KEEP=engine", "HERDR_WORKSPACE_ID=w1", "HERDR_TAB_ID=w1:t1", "HERDR_PANE_ID=w1:p1"},
	} {
		t.Run(strings.Join(saved, ";"), func(t *testing.T) {
			testjail.Fleet(t)
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", "/fixture/herdr.sock")
			t.Setenv("HERDR_WORKSPACE_ID", "w9")
			t.Setenv("HERDR_TAB_ID", "w9:t4")
			t.Setenv("HERDR_PANE_ID", "w9:p2")
			file, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			_, options, err := seatCommand(
				fleetdb.SeatRun{Binary: "/fixture/claude", CWD: "/fixture/work", Env: saved},
				"claude",
				file,
				nil,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"KEEP=engine", "HERDR_WORKSPACE_ID=w9", "HERDR_TAB_ID=w9:t4", "HERDR_PANE_ID=w9:p2"} {
				if !slices.Contains(options.Env, want) {
					t.Errorf("seat env=%v, missing %s", options.Env, want)
				}
			}
			for _, entry := range options.Env {
				if strings.HasPrefix(entry, "HERDR_WORKSPACE_ID=") && entry != "HERDR_WORKSPACE_ID=w9" ||
					strings.HasPrefix(entry, "HERDR_TAB_ID=") && entry != "HERDR_TAB_ID=w9:t4" {
					t.Errorf("seat env retained stale identity %s", entry)
				}
			}
		})
	}
}

func seatReloadFailure(t *testing.T, root, socket string) (reload.Request, reload.Options) {
	t.Helper()
	account := filepath.Join(root, "account")
	if err := os.Mkdir(account, 0o700); err != nil {
		t.Fatal(err)
	}
	request := reload.Request{
		Engine:       pfmengine.Claude,
		SocketPath:   socket,
		Pane:         "w6:p3",
		CWD:          root,
		Home:         root,
		Account:      1,
		AccountGiven: true,
		AccountIDs:   []int{1},
		CacheGiven:   true,
		Machine: config.Config{
			Claude: config.ClaudePrefs{
				Binary:         filepath.Join(root, "missing-next-engine"),
				PermissionMode: config.PermissionBypass,
			},
			Accounts: []config.Account{{ID: 1, ConfigDir: account}},
		},
	}
	options := reload.Options{
		Home:           root,
		SIDDir:         filepath.Join(root, "sid"),
		Delay:          -1,
		Poll:           10 * time.Millisecond,
		IdleTries:      10,
		ExitTries:      50,
		ExitGraceTries: 5,
	}
	return request, options
}

func TestSeatHostPreBindFailurePreservesLiveSeat(t *testing.T) {
	testjail.Fleet(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat := fleetdb.Seat{
		SeatID:          "bound",
		Backend:         "herdr",
		State:           "live",
		HerdrSocket:     "/fixture/owner.sock",
		HerdrPane:       "w1:p1",
		HerdrTerminalID: "terminal",
		SeatHostPID:     99,
		SeatHostStart:   7,
	}
	if err := db.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_ENV", "")
	var stderr bytes.Buffer
	if rc := SeatHost([]string{seat.SeatID}, &bytes.Buffer{}, &stderr, runtime); rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
	got, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil || !found || got != seat {
		t.Fatalf("stray pre-bind invocation mutated live owner: got=%+v want=%+v err=%v", got, seat, err)
	}
	if !strings.Contains(stderr.String(), "current Herdr pane identity is required") {
		t.Fatalf("missing local failure: %s", &stderr)
	}
}

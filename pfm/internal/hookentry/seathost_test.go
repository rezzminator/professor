package hookentry

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

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
		stderr.String() != "pfm internal seat-host: expected one seat id\n" {
		t.Fatalf("SeatHost = %d, %q", rc, stderr.String())
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

// The hangup probe must see a PTY's master close on every kernel: Darwin's poll never
// reported POLLHUP unasked, so a seat whose Herdr stopped waited out its retirement.
func TestSeatTerminalHungUpSeesMasterClose(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			t.Error(err)
		}
	}()
	if hungUp, err := seatTerminalHungUp(int(terminal.Fd())); err != nil || hungUp {
		t.Fatalf("open PTY: hungUp=%v err=%v, want false <nil>", hungUp, err)
	}
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	if hungUp, err := seatTerminalHungUp(int(terminal.Fd())); err != nil || !hungUp {
		t.Fatalf("closed master: hungUp=%v err=%v, want true <nil>", hungUp, err)
	}
}

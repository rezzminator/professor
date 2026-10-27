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

// seatPaneCommand builds the exec'd command for saved inside a fixture Herdr
// pane, the way seat-host does at every door that runs a recipe.
func seatPaneCommand(t *testing.T, saved []string) []string {
	t.Helper()
	testjail.Fleet(t)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", "/fixture/herdr.sock")
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
	return options.Env
}

// A recipe captured in the launchd daemon carries no terminal; the chat takes
// the terminal identity of the pane seat-host runs in.
func TestSeatCommandTakesPaneTerminalIdentity(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM_PROGRAM", "herdr")
	env := seatPaneCommand(t, []string{"KEEP=engine", "XPC_SERVICE_NAME=com.fixture.daemon"})
	for _, want := range []string{
		"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=herdr",
		"KEEP=engine", "XPC_SERVICE_NAME=com.fixture.daemon",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("seat env=%v, missing %s", env, want)
		}
	}
}

// The pane's terminal wins over the recipe's, and a terminal variable the pane
// does not set is dropped rather than revived from the recipe.
func TestSeatCommandPaneTerminalBeatsRecipe(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("LC_CTYPE", "en_US.UTF-8")
	t.Setenv("COLORTERM", "")
	if err := os.Unsetenv("COLORTERM"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LC_ALL", "")
	if err := os.Unsetenv("LC_ALL"); err != nil {
		t.Fatal(err)
	}
	env := seatPaneCommand(t, []string{
		"TERM=dumb", "LANG=en_GB.ISO8859-1", "LC_ALL=POSIX", "COLORTERM=24bit", "KEEP=engine",
	})
	want := []string{"KEEP=engine", "TERM=xterm-256color", "LANG=C.UTF-8", "LC_CTYPE=en_US.UTF-8"}
	for _, entry := range want {
		if !slices.Contains(env, entry) {
			t.Errorf("seat env=%v, missing %s", env, entry)
		}
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM", "LANG", "LC_CTYPE":
			if !slices.Contains(want, entry) {
				t.Errorf("seat env kept recipe terminal %s", entry)
			}
		case "COLORTERM", "LC_ALL":
			t.Errorf("seat env revived recipe terminal %s the pane does not set", entry)
		}
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

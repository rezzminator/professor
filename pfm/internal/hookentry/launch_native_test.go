package hookentry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

func TestLaunchNativeBackendRefusal(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	env := &paths.MapEnv{
		Values: map[string]string{
			"HERDR_ENV":         "1",
			"HERDR_PANE_ID":     "w6:p3",
			"HERDR_SOCKET_PATH": filepath.Join(root, "dead.sock"),
		},
	}
	rc := Launch(
		[]string{"--real", "/unneeded/claude", "--cwd", root, "--backend", "herdr", "--"},
		&stdout,
		&stderr,
		config.Runtime{},
		env,
	)
	if rc != 1 ||
		!strings.Contains(stderr.String(), "Herdr pane w6:p3 unreachable") &&
			!strings.Contains(stderr.String(), "herdr pane w6:p3 unreachable") {
		t.Fatalf("explicit native backend = %d %q", rc, &stderr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("refusal emitted a birth: %s", &stdout)
	}
}

func TestLaunchNativeRefusesOccupiedPane(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	seat := fleetdb.Seat{
		SeatID:      "parent",
		Backend:     "herdr",
		State:       "live",
		HerdrSocket: "/test.sock",
		HerdrPane:   "w1:p1",
	}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	rc := launchNative(
		spawn.BackendChoice{Backend: "herdr", HerdrSocket: seat.HerdrSocket, HerdrPane: seat.HerdrPane},
		claudelaunch.Request{
			Binary: "/bin/echo",
			Home:   root,
		},
		root,
		&bytes.Buffer{},
		&stderr,
		config.Runtime{Paths: values},
	)
	rows, err := db.Seats(context.Background())
	if rc != 1 || !strings.Contains(stderr.String(), "already owned") || err != nil || len(rows) != 1 {
		t.Fatalf("nested launch changed parent ownership: rc=%d rows=%+v err=%v stderr=%s", rc, rows, err, &stderr)
	}
}

func TestLaunchNativeReexecsExactSupervisorRole(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "pfm.db")}
	oldArgs := os.Args[0]
	os.Args[0] = "pfm"
	defer func() { os.Args[0] = oldArgs }()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := LaunchExec
	defer func() { LaunchExec = old }()
	called := false
	LaunchExec = func(binary string, argv, _ []string) error {
		called = true
		if binary != executable || len(argv) != 4 || argv[1] != "internal" || argv[2] != "seat-host" || argv[3] == "" {
			t.Errorf("not a supervisor exec: %s %q", binary, argv)
		}
		return errors.New("fixture exec stopped")
	}
	var stderr bytes.Buffer
	rc := launchNative(
		spawn.BackendChoice{Backend: "herdr", HerdrSocket: "/fixture.sock", HerdrPane: "w1:p1"},
		claudelaunch.Request{Binary: "/bin/echo", Home: root},
		root,
		&bytes.Buffer{},
		&stderr,
		config.Runtime{Paths: values},
	)
	if !called || rc != 1 || !strings.Contains(stderr.String(), "fixture exec stopped") {
		t.Fatalf("launch never reexeced supervisor: called=%v rc=%d err=%s", called, rc, &stderr)
	}
}

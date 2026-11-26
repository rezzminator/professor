package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestLaunchNativeBackendRefusal(t *testing.T) {
	root := t.TempDir()
	// The caller lookup scans an empty tmux dir of its own, never the fence's shared one.
	t.Setenv(paths.EnvTmuxDir, t.TempDir())
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
	dead := filepath.Join(root, "dead.sock")
	want := "pfm internal launch: host refused at hook.launch: herdr backend asked for, but no Herdr server runs at " +
		dead + ": stat " + dead + ": no such file or directory\n"
	if rc != 10 || stderr.String() != want {
		t.Fatalf("explicit native backend = %d %q, want 10 %q", rc, &stderr, want)
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
		HerdrSocket: newFakeHerdr(t).path,
		HerdrPane:   "w1:p1",
	}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	rc := launchNative(
		spawn.BackendChoice{
			Backend:        "herdr",
			InPlace:        true,
			HerdrSocket:    seat.HerdrSocket,
			HerdrPane:      seat.HerdrPane,
			HerdrWorkspace: "w1",
		},
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
		spawn.BackendChoice{
			Backend:        "herdr",
			InPlace:        true,
			HerdrSocket:    newFakeHerdr(t).path,
			HerdrPane:      "w1:p1",
			HerdrWorkspace: "w1",
		},
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

func TestLaunchNativeNewTabRefusesBirthFailure(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "pfm.db")}
	var stderr bytes.Buffer
	code := launchNative(
		spawn.BackendChoice{
			Backend:        "herdr",
			HerdrSocket:    filepath.Join(root, "absent.sock"),
			HerdrPane:      "w1:p1",
			HerdrWorkspace: "w1",
		},
		claudelaunch.Request{
			Binary: "/bin/echo",
			Home:   root,
		},
		root,
		&bytes.Buffer{},
		&stderr,
		config.Runtime{Paths: values},
	)
	if code != 1 ||
		stderr.String() != "pfm internal launch: birth native chat: herdr birth has no fleet places to file the chat under\n" {
		t.Fatalf("new tab failure code=%d stderr=%s", code, &stderr)
	}
}

// A new chat a chat in space "zz" launches is born in "zz": a fresh launch, whose
// session id launch.go mints before the native birth, and a chosen --session-id;
// a continuation keeps its own space, so none is recorded for it.
func TestLaunchNativeNewTabBirthKeepsContinuationsLaunchMetadataAndSpace(t *testing.T) {
	for _, row := range []struct {
		name      string
		args      []string
		sessionID string
		space     string
	}{
		{name: "fresh", sessionID: "fresh-session", space: "zz"},
		{name: "continue", args: []string{"--continue"}},
		{name: "resume picker", args: []string{"--resume"}},
		{name: "fork", args: []string{"--resume", "parent", "--fork-session"}},
		{name: "chosen session id", args: []string{"--session-id", "known-session"}, space: "zz"},
	} {
		t.Run(row.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(testjail.ShortRoot(t), "native.sock")
			state := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = state.Close() }()
			launcher := fleetdb.Seat{
				SeatID: "launcher", SessionID: "elder", Backend: "herdr", State: "live",
				HerdrSocket: socket, HerdrPane: "w1:p1",
			}
			if err := state.PutSeat(context.Background(), launcher); err != nil {
				t.Fatal(err)
			}
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
						var request struct {
							ID     string
							Method string
							Params struct {
								Label string `json:"label"`
							}
						}
						if json.NewDecoder(conn).Decode(&request) != nil {
							return
						}
						var result any = map[string]any{"type": "pong", "version": "fixture", "protocol": herdr.SupportedProtocol}
						if placed, handled := testjail.HerdrWorkspaceReply(
							request.Method,
							request.Params.Label,
							"w1",
						); handled {
							result = placed
						}
						if request.Method == "layout.apply" {
							result = map[string]any{
								"layout": map[string]any{
									"workspace_id": "w1",
									"root":         map[string]any{"type": "pane", "pane_id": "w1:p2"},
								},
							}
						}
						_ = json.NewEncoder(conn).Encode(map[string]any{"id": request.ID, "ok": true, "result": result})
					}()
				}
			}()
			cache := true
			var stderr bytes.Buffer
			code := launchNative(
				spawn.BackendChoice{
					Backend: "herdr", HerdrSocket: socket, HerdrPane: "w1:p1", HerdrWorkspace: "w1",
					Places: func(context.Context) (*place.Fleet, error) {
						return &place.Fleet{Spaces: map[string]string{"elder": "zz"}}, nil
					},
				},
				claudelaunch.Request{
					Binary:    "/bin/echo",
					Home:      values.Home,
					Args:      row.args,
					SessionID: row.sessionID,
					Account:   2,
					Cache1H:   &cache,
				},
				values.Home,
				io.Discard,
				&stderr,
				config.Runtime{Paths: values},
			)
			if code != 0 {
				t.Fatalf("birth code=%d: %s", code, &stderr)
			}
			seats, err := state.Seats(context.Background())
			var born []fleetdb.Seat
			for index := range seats {
				if seats[index].SeatID != launcher.SeatID {
					born = append(born, seats[index])
				}
			}
			if err != nil || len(born) != 1 || born[0].SessionID == "" {
				t.Fatalf("birth seats=%+v err=%v", seats, err)
			}
			spaces, err := state.Spaces(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := spaces[born[0].SessionID].Space; got != row.space {
				t.Fatalf("space of born chat %s = %q, want %q (records %+v, stderr %q)",
					born[0].SessionID, got, row.space, spaces, &stderr)
			}
			if row.name == "chosen session id" {
				launches, err := fleetdb.OpenLaunches(context.Background(), values)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = launches.Close() }()
				record, err := launches.LaunchFor(context.Background(), "known-session")
				if err != nil || record.Account != 2 || !record.Cache1H {
					t.Fatalf("new tab lost launch metadata: %+v err=%v", record, err)
				}
			}
		})
	}
}

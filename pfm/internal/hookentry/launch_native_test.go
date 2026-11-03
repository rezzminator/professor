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
	"reflect"
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
	if rc != 1 || !strings.Contains(stderr.String(), "herdr backend needs a caller in a Herdr pane") {
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
		HerdrSocket: launchNativeHerdr(t, filepath.Base(root)).socket,
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
			Places:         func(context.Context) (*place.Fleet, error) { return &place.Fleet{}, nil },
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
			HerdrSocket:    launchNativeHerdr(t, filepath.Base(root)).socket,
			HerdrPane:      "w1:p1",
			HerdrWorkspace: "w1",
			Places:         func(context.Context) (*place.Fleet, error) { return &place.Fleet{}, nil },
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
	for _, inPlace := range []bool{false, true} {
		t.Run(map[bool]string{false: "birth", true: "placement"}[inPlace], func(t *testing.T) {
			root := t.TempDir()
			values := paths.Values{Home: root, StateDB: filepath.Join(root, "pfm.db")}
			var stderr bytes.Buffer
			code := launchNative(
				spawn.BackendChoice{
					Backend:        "herdr",
					InPlace:        inPlace,
					HerdrSocket:    filepath.Join(root, "absent.sock"),
					HerdrPane:      "w1:p1",
					HerdrWorkspace: "w1",
					Places: func(context.Context) (*place.Fleet, error) {
						return nil, errors.New("fixture fleet unreadable")
					},
				},
				claudelaunch.Request{Binary: "/bin/echo", Home: root},
				root, &bytes.Buffer{}, &stderr, config.Runtime{Paths: values},
			)
			want := "pfm internal launch: birth native chat: place Herdr tab: fixture fleet unreadable\n"
			if inPlace {
				want = "pfm internal launch: place native chat: scan the fleet's places: fixture fleet unreadable\n"
			}
			if code != 1 || stderr.String() != want {
				t.Fatalf("refusal expected code=1 stderr=%q; actual code=%d stderr=%q", want, code, &stderr)
			}
		})
	}
}

func TestLaunchNativeNewTabPreservesContinuationsAndLaunchMetadata(t *testing.T) {
	for _, test := range []struct {
		name, callerWorkspace string
		args                  []string
	}{
		{name: "continue", args: []string{"--continue"}},
		{name: "resume", args: []string{"--resume"}},
		{name: "fork", args: []string{"--resume", "parent", "--fork-session"}},
		{name: "launch metadata", args: []string{"--session-id", "known-session"}},
		{name: "same workspace", callerWorkspace: "w1", args: []string{"--session-id", "known-session"}},
		{name: "foreign workspace", callerWorkspace: "w2", args: []string{"--session-id", "known-session"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(values.Home, "atlas")
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
			server := launchNativeHerdr(t, "atlas")
			workspace := test.callerWorkspace
			if workspace == "" {
				workspace = "w1"
			}
			execed := false
			old := LaunchExec
			defer func() { LaunchExec = old }()
			LaunchExec = func(string, []string, []string) error {
				execed = true
				return errors.New("fixture exec stopped")
			}
			cache := true
			var stderr bytes.Buffer
			code := launchNative(
				spawn.BackendChoice{
					Backend:        "herdr",
					HerdrSocket:    server.socket,
					HerdrPane:      workspace + ":p1",
					HerdrWorkspace: workspace,
					InPlace:        test.callerWorkspace != "",
					Places:         func(context.Context) (*place.Fleet, error) { return &place.Fleet{}, nil },
				},
				claudelaunch.Request{
					Binary: "/bin/echo", Home: values.Home, Args: test.args, Account: 2, Cache1H: &cache,
				},
				cwd, io.Discard, &stderr, config.Runtime{Paths: values},
			)
			state := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = state.Close() }()
			seats, err := state.Seats(context.Background())
			if err != nil || len(seats) != 1 || seats[0].SessionID == "" {
				t.Fatalf("birth seats=%+v err=%v", seats, err)
			}
			wantPane, wantCode, wantExec := "w1:p2", 0, false
			wantMutations := []string{"layout.apply:w1"}
			if test.callerWorkspace == "w1" {
				wantPane, wantCode, wantExec = "w1:p1", 1, true
				wantMutations = []string{"tab.rename:w1:t1:atlas"}
			}
			mutations := []string{}
			for len(server.mutations) > 0 {
				mutations = append(mutations, <-server.mutations)
			}
			if seats[0].HerdrWorkspace != "w1" || seats[0].HerdrPane != wantPane ||
				code != wantCode || execed != wantExec || !reflect.DeepEqual(mutations, wantMutations) {
				t.Fatalf(
					"placement expected workspace=w1 pane=%s code=%d exec=%v mutations=%q; actual seat=%+v code=%d exec=%v mutations=%q stderr=%s",
					wantPane,
					wantCode,
					wantExec,
					wantMutations,
					seats[0],
					code,
					execed,
					mutations,
					&stderr,
				)
			}
			if test.args[0] == "--session-id" {
				launches, err := fleetdb.OpenLaunches(context.Background(), values)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = launches.Close() }()
				record, err := launches.LaunchFor(context.Background(), "known-session")
				if err != nil || record.Account != 2 || !record.Cache1H {
					t.Fatalf("birth lost launch metadata: %+v err=%v", record, err)
				}
			}
		})
	}
}

type launchNativeServer struct {
	socket    string
	mutations chan string
}

func launchNativeHerdr(t *testing.T, project string) launchNativeServer {
	t.Helper()
	server := launchNativeServer{
		socket: filepath.Join(testjail.ShortRoot(t), "native.sock"), mutations: make(chan string, 16),
	}
	listener, err := net.Listen("unix", server.socket)
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
					ID, Method string
					Params     map[string]any
				}
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				label, _ := request.Params["label"].(string)
				result, handled := testjail.HerdrWorkspaceReply(request.Method, label, "w1")
				switch request.Method {
				case "ping":
					result, handled = map[string]any{
						"type":     "pong",
						"version":  "fixture",
						"protocol": herdr.SupportedProtocol,
					}, true
				case "workspace.list":
					result = map[string]any{"type": "workspace_list", "workspaces": []any{
						map[string]any{"workspace_id": "w1", "label": project, "pane_count": 1},
						map[string]any{"workspace_id": "w2", "label": "beacon", "pane_count": 1},
					}}
				case "pane.get":
					result, handled = testjail.HerdrPaneInfo(request.Params["pane_id"].(string)), true
				case "tab.get", "tab.rename":
					tab := request.Params["tab_id"].(string)
					if request.Method == "tab.get" {
						label = "caller-label"
					} else {
						server.mutations <- "tab.rename:" + tab + ":" + label
					}
					result, handled = map[string]any{"type": "tab_info", "tab": map[string]any{
						"tab_id": tab, "label": label, "pane_count": 1,
					}}, true
				case "layout.apply":
					server.mutations <- "layout.apply:" + request.Params["workspace_id"].(string)
					result, handled = map[string]any{"layout": map[string]any{
						"workspace_id": "w1", "root": map[string]any{"type": "pane", "pane_id": "w1:p2"},
					}}, true
				}
				if !handled {
					t.Errorf("unexpected Herdr method %s", request.Method)
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": request.ID, "ok": handled, "result": result})
			}()
		}
	}()
	return server
}

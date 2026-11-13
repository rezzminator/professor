//go:build jail

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestChatEndRemovesTheRoleSeatPrompt(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	const launchName = "EndRoleWorker"
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "cc", "--name", launchName,
		"--cwd", filepath.Join(jail.root, "work"), "audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets=%v err=%v", entries, err)
	}
	target, targetCode := headlessTarget(
		context.Background(), entries[0].Name(), &stdout, &stderr, false,
	)
	if targetCode != 0 {
		t.Fatalf("resolve spawned seat code=%d stdout=%q stderr=%q", targetCode, stdout.String(), stderr.String())
	}
	sidDir := filepath.Join(jail.root, "sid")
	if err := agentrole.WriteSeatPrompt(
		sidDir, target.Socket, target.Pane, "<!-- pfm agent-role: reviewer -->\nROLE",
	); err != nil {
		t.Fatal(err)
	}
	if err := agentrole.WriteSeatPrompt(
		sidDir, target.Socket, "", "<!-- pfm agent-role: reviewer -->\nFALLBACK",
	); err != nil {
		t.Fatal(err)
	}
	promptPaths := []string{
		mustReloadSeatPromptPath(t, sidDir, target.Socket, target.Pane),
		mustReloadSeatPromptPath(t, sidDir, target.Socket, ""),
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "end", entries[0].Name()}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat end exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, promptPath := range promptPaths {
		if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
			t.Fatalf("role prompt %s outlived chat end: %v", promptPath, err)
		}
	}
}

func TestChatEndWarnsButSucceedsWhenRolePromptRemovalFails(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	const launchName = "SabotagedRoleWorker"
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "cc", "--name", launchName,
		"--cwd", filepath.Join(jail.root, "work"), "audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets=%v err=%v", entries, err)
	}
	target, targetCode := headlessTarget(
		context.Background(), entries[0].Name(), &stdout, &stderr, false,
	)
	if targetCode != 0 {
		t.Fatalf("resolve spawned seat code=%d stdout=%q stderr=%q", targetCode, stdout.String(), stderr.String())
	}
	promptPath := mustReloadSeatPromptPath(t, filepath.Join(jail.root, "sid"), target.Socket, target.Pane)
	if err := os.MkdirAll(filepath.Join(promptPath, "litter"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptPath, "litter", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "end", entries[0].Name()}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat end exit=%d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "WARNING") || !strings.Contains(stderr.String(), "role prompt") {
		t.Fatalf("chat end stderr=%q, want role-prompt WARNING", stderr.String())
	}
}

func TestReloadTargetWarnsAndPreservesTmuxWhenNativeSeatsUnreadable(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		wantError string
	}{
		{name: "newer-schema", wantError: "newer than binary"},
		{name: "corrupt", wantError: "not a database"},
		{name: "stat-error", wantError: "not a directory"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("HERDR_ENV", "")
			statePath := filepath.Join(t.TempDir(), "state.sqlite")
			switch testCase.name {
			case "newer-schema":
				db, err := sqlitedb.OpenReadWrite(statePath, 5*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				if _, err := db.Exec("PRAGMA user_version=2147483647"); err != nil {
					t.Fatal(err)
				}
			case "corrupt", "stat-error":
				testjail.DatabaseFiles(t, filepath.Dir(statePath))
				if err := os.WriteFile(statePath, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
				if testCase.name == "stat-error" {
					statePath = filepath.Join(statePath, "state.sqlite")
				}
			}
			want := reload.Pane{ID: "%1", PID: 11, CurrentPath: "/worktree"}
			var stderr bytes.Buffer
			socket, pane, state, _, code := reloadTarget(
				context.Background(), "/jail/tmux/cc-test", "%1",
				paths.Values{StateDB: statePath}, commandRuntime{},
				reloadTargetTmux{panes: []reload.Pane{want}}, &stderr, nil,
			)
			if code != 0 || socket != "/jail/tmux/cc-test" || pane != want.ID || state != want {
				t.Fatalf(
					"tmux target=(%q,%q,%+v,%d), want live pane despite unreadable native seats; stderr=%q",
					socket,
					pane,
					state,
					code,
					stderr.String(),
				)
			}
			for _, text := range []string{"WARNING", "read native seats", testCase.wantError} {
				if !strings.Contains(stderr.String(), text) {
					t.Errorf("native-seat warning=%q, want %q", stderr.String(), text)
				}
			}
		})
	}
}

func TestNativeChatReloadSchedulesResolvedPane(t *testing.T) {
	for _, mode := range []string{"ambient", "socket", "identity"} {
		t.Run(mode, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(jail.root, "herdr.sock")
			t.Setenv("TMUX", "")
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			var args []string
			if mode == "socket" {
				args = []string{"--sock", socket, "--pane", "w6:p3"}
			}
			{
				db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
				err := db.PutSeat(
					context.Background(),
					fleetdb.Seat{
						SeatID:         "native",
						SessionID:      "native-session",
						Backend:        "herdr",
						Engine:         "cc",
						State:          "live",
						HerdrSocket:    socket,
						HerdrPane:      "w6:p3",
						HerdrWorkspace: "w6", HerdrTerminalID: "fixture-w6:p3", SeatHostPID: 42, SeatHostStart: 100,
					},
				)
				closeErr := db.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("seed: %v %v", err, closeErr)
				}
			}
			if mode == "ambient" {
				writeScheduleRestartHost(t, runtime.Paths.ProcRoot, "native")
				setCLICallerParent(t, 42)
				(&scheduleRestartHerdr{t: t, proc: runtime.Paths.ProcRoot, pane: "w6:p3", seatID: "native"}).serve(
					socket,
				)
			}
			started := false
			previous := startReloadWorker
			startReloadWorker = func(argv []string, _ deps.StartOptions) (int, error) {
				started = true
				if !strings.Contains(strings.Join(argv, " "), socket) ||
					!strings.Contains(strings.Join(argv, " "), "w6:p3") {
					t.Errorf("worker target %v", argv)
				}
				return 0, nil
			}
			t.Cleanup(func() { startReloadWorker = previous })
			var stdout, stderr bytes.Buffer
			var code int
			if mode == "identity" {
				var seat *fleetdb.Seat
				// No tmux pane is listed: a Herdr identity resolves through its seat alone.
				_, _, _, seat, code = reloadTargetFromIdentity(
					context.Background(),
					resolve.Identity{Backend: "herdr", SocketPath: socket, Pane: "w6:p3"},
					runtime.Paths,
					reloadTargetTmux{},
					&stderr,
					paths.OSEnv{},
				)
				if seat == nil || seat.SeatID != "native" {
					t.Fatalf("identity target seat = %+v, want the native seat decided once", seat)
				}
			} else {
				code = runChatReloadWithRuntime(t.Context(), args, &stdout, &stderr, runtime, paths.OSEnv{})
			}
			logs, logErr := filepath.Glob(filepath.Join(runtime.Paths.SIDDir, "reload-*.log"))
			if code != 0 || (mode != "identity" && (!started || len(logs) != 1)) || logErr != nil {
				t.Fatalf(
					"native %s reload code=%d out=%q err=%q started=%v logs=%v err=%v",
					mode,
					code,
					stdout.String(),
					stderr.String(),
					started,
					logs,
					logErr,
				)
			}
		})
	}
}

func TestNativeReloadWorkerForwardsTypedRequest(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(engine), func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(jail.root, "not-an-engine.sock")
			db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
			if err := db.PutSeat(
				context.Background(),
				fleetdb.Seat{
					SeatID:      "worker",
					SessionID:   "session",
					Engine:      string(engine),
					Backend:     "herdr",
					State:       "live",
					HerdrSocket: socket,
					HerdrPane:   "w6:p3",
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			prior := runHerdrReload
			t.Cleanup(func() { runHerdrReload = prior })
			calls := 0
			runHerdrReload = func(_ context.Context, request reload.Request, options reload.Options, _ io.Writer) (reload.Result, error) {
				calls++
				if request.Engine != engine || request.SocketPath != socket || request.Pane != "w6:p3" ||
					request.Model != "new-model" ||
					request.Effort != "high" ||
					request.Then != "follow up" ||
					request.Cache1H ||
					!request.CacheGiven ||
					!request.AccountGiven ||
					options.SIDDir != runtime.Paths.SIDDir {
					t.Errorf("request=%+v options=%+v", request, options)
				}
				return reload.Result{}, nil
			}
			var out, stderr bytes.Buffer
			code := runChatReloadWorkerWithRuntime(
				[]string{
					"--sock",
					socket,
					"--pane",
					"w6:p3",
					"--model",
					"new-model",
					"--effort",
					"high",
					"--then",
					"follow up",
					"--cache",
					"5m",
					"--account",
					"1",
				},
				&out,
				&stderr,
				runtime,
				paths.OSEnv{},
			)
			if code != 0 || calls != 1 || !strings.Contains(out.String(), "respawned in place") {
				t.Fatalf("code=%d calls=%d out=%q err=%q", code, calls, out.String(), stderr.String())
			}
		})
	}
}

func TestReloadWorkerReportsSuppressedNativeNotification(t *testing.T) {
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "notify.sock")
	serveNativeAdapter(
		t,
		socket,
		map[string]string{"notification.show": `{"type":"notification_show","shown":false,"reason":"disabled"}`},
	)
	db := fleetdb.OpenSharedState(t.Context(), values)
	if err := db.PutSeat(
		t.Context(),
		fleetdb.Seat{
			SeatID:      "notify",
			Backend:     "herdr",
			Engine:      "claude",
			State:       "live",
			HerdrSocket: socket,
			HerdrPane:   "w6:p3",
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	announceReloadWorkerFailure(
		[]string{"--sock", socket, "--pane", "w6:p3"},
		"reload failed",
		false,
		func(context.Context, string, string, string) error { return io.ErrUnexpectedEOF },
		&stderr,
	)
	if got, want := rooted(
		stderr.String(),
		filepath.Dir(socket),
	), "pfm chat reload: native failure notification: native reload failure notification: herdr notification not shown (disabled)\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestChatEndRetryPendingIntentRunsRealDetachedCLIWorker(t *testing.T) {
	jail := newRunJail(t)
	testjail.DatabaseFiles(t, jail.root)
	defer jail.killSockets(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
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
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				reply := map[string]any{"id": req.ID, "result": map[string]any{"type": "pane_list", "panes": []any{}}}
				if req.Method != "pane.list" {
					t.Errorf("unexpected pending reconciliation method %s", req.Method)
					return
				}
				if err := json.NewEncoder(conn).Encode(reply); err != nil {
					t.Error(err)
				}
			}()
		}
	}()

	seat := fleetdb.Seat{
		SeatID:          "pending-cli",
		SessionID:       "57c427ee-9946-472b-b0e1-c038d98e7ae8",
		Name:            "PENDING",
		Backend:         "herdr",
		State:           "live",
		EndPending:      true,
		HerdrSocket:     socket,
		HerdrPane:       "w6:p3",
		HerdrTerminalID: "old-terminal",
		SeatHostPID:     900001,
		SeatHostStart:   100,
	}
	db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() { _ = db.Close() }()
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(testPFMBinary, "chat", "end", seat.SessionID)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || !strings.Contains(stdout.String(), "end scheduled") {
		t.Fatalf("real initiating CLI failed: err=%v out=%s stderr=%s", err, &stdout, &stderr)
	}

	deadline := time.Now().Add(8 * time.Second)
	for {
		_, found, err := db.Seat(context.Background(), seat.SeatID)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			if _, err := os.Stat(
				filepath.Join(filepath.Dir(runtime.Paths.StateDB), "seats", seat.SeatID+".json"),
			); !os.IsNotExist(
				err,
			) {
				t.Fatalf("pending recipe remains: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(filepath.Join(runtime.Paths.SIDDir, "end-"+seat.SeatID+".log"))
			t.Fatalf("real detached CLI worker did not reconcile: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestNativeChatReloadRefusesASecondScheduleForTheSameSeat: a native chat's
// /reload schedules one worker; a second /reload while that worker lives is
// refused by name, and a record whose worker is gone is replaced.
func TestNativeChatReloadRefusesASecondScheduleForTheSameSeat(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	// The worker's liveness is read from the kernel: the stub worker is this
	// test process, which the jail's fake proc tree does not hold.
	t.Setenv(paths.EnvProcRoot, paths.SystemProcRoot())
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(jail.root, "herdr.sock")
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	err = db.PutSeat(context.Background(), fleetdb.Seat{
		SeatID: "native", SessionID: "native-session", Backend: "herdr", Engine: "cc",
		State: "live", HerdrSocket: socket, HerdrPane: "w6:p3",
	})
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatalf("seed: %v %v", err, closeErr)
	}
	started := 0
	previous := startReloadWorker
	// The scheduled worker is this test process: alive for the whole test.
	startReloadWorker = func([]string, deps.StartOptions) (int, error) {
		started++
		return os.Getpid(), nil
	}
	t.Cleanup(func() { startReloadWorker = previous })
	logPath := filepath.Join(runtime.Paths.SIDDir, "reload-herdr-native.log")
	schedule := func() (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := runChatReloadWithRuntime(
			t.Context(), []string{"--sock", socket, "--pane", "w6:p3"}, &stdout, &stderr, runtime, paths.OSEnv{},
		)
		return code, stdout.String(), stderr.String()
	}

	code, stdout, stderr := schedule()
	if code != 0 || started != 1 || !strings.Contains(stdout, "(log "+logPath+")") {
		t.Fatalf("first schedule code=%d started=%d out=%q err=%q, want one worker logging to %s",
			code, started, stdout, stderr, logPath)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("per-seat worker log: %v", err)
	}

	code, stdout, stderr = schedule()
	want := fmt.Sprintf(
		"pfm chat reload: a reload is already scheduled for this chat (worker pid %d, log %s); it runs once the chat is idle\n",
		os.Getpid(),
		logPath,
	)
	if code != 2 || started != 1 || stderr != want || stdout != "" {
		t.Fatalf("second schedule code=%d started=%d out=%q err=%q, want 2 and %q", code, started, stdout, stderr, want)
	}

	// A record whose pid now names another process (its birth differs) is stale.
	record := filepath.Join(runtime.Paths.SIDDir, "reload-herdr-native.pid")
	if err := os.WriteFile(record, []byte(fmt.Sprintf("%d 1\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = schedule()
	if code != 0 || started != 2 {
		t.Fatalf(
			"stale record schedule code=%d started=%d out=%q err=%q, want a fresh worker",
			code,
			started,
			stdout,
			stderr,
		)
	}
}

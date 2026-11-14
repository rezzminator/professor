package restart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// frontRoleEnv makes the re-executed test binary play the front: the pfm
// restart typed in a chat, spawning its worker and following its log. Its
// value is the directory the worker writes its proof of survival into.
const frontRoleEnv = "PFM_RESTART_TEST_FRONT_DIR"

// runFrontRole is the front: it spawns a worker that outlives it, prints the
// worker's pid, then follows the worker's log until it is killed.
func runFrontRole(dir string) int {
	launcher := Launcher{Runner: deps.RealRunner{}, Clock: clock.Real, Exited: childExited}
	script := "sleep 1; echo survived > " + filepath.Join(dir, "marker") + "; echo '" + ExitMarker + "0'"
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, frontRoleEnv+"=") {
			env = append(env, entry)
		}
	}
	pid, logPath, err := launcher.Spawn(context.Background(), []string{"/bin/sh", "-c", script}, env, dir, "worker.log")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(pid)
	return launcher.Follow(context.Background(), pid, logPath, os.Stdout, os.Stderr)
}

func writeLog(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestFollowCopiesTheLogUntilTheExitMarker(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "worker.log")
	writeLog(t, logPath, "")
	pieces := []string{"stop  chats  a  ok", "  ended\nstart herdr ok\n", ExitMarker + "3\nafter the marker\n"}
	launcher := Launcher{Clock: newTickClock(), Exited: func(int) (bool, error) {
		if len(pieces) > 0 {
			writeLog(t, logPath, pieces[0])
			pieces = pieces[1:]
		}
		return false, nil
	}}
	var stdout, stderr bytes.Buffer
	if code := launcher.Follow(context.Background(), 7, logPath, &stdout, &stderr); code != 3 {
		t.Fatalf("Follow = %d, want the worker's own exit code; stderr %s", code, stderr.String())
	}
	want := "stop  chats  a  ok  ended\nstart herdr ok\n" + ExitMarker + "3\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestFollowAWorkerGoneWithoutItsMarkerIsAFailure(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "worker.log")
	writeLog(t, logPath, "stop  chats  a  ok\n")
	for name, exited := range map[string]func(int) (bool, error){
		"exited":      func(int) (bool, error) { return true, nil },
		"wait4 fails": func(int) (bool, error) { return false, errors.New("wait4: EINVAL") },
	} {
		var stdout, stderr bytes.Buffer
		launcher := Launcher{Clock: newTickClock(), Exited: exited}
		if code := launcher.Follow(context.Background(), 7, logPath, &stdout, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "worker pid 7") {
			t.Fatalf("%s: Follow = %d, stderr %q", name, code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	launcher := Launcher{Clock: newTickClock(), Exited: func(int) (bool, error) { return false, nil }}
	if code := launcher.Follow(
		context.Background(),
		7,
		filepath.Join(t.TempDir(), "absent.log"),
		&bytes.Buffer{},
		&stderr,
	); code != 1 {
		t.Fatalf("an unreadable log = %d", code)
	}
}

func TestFollowAnUnreadableExitMarkerIsNamedAndFails(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "worker.log")
	writeLog(t, logPath, "stop  chats  a  ok\n"+ExitMarker+"two\n")
	var stdout, stderr bytes.Buffer
	launcher := Launcher{Clock: newTickClock(), Exited: func(int) (bool, error) { return false, nil }}
	if code := launcher.Follow(context.Background(), 7, logPath, &stdout, &stderr); code != 1 {
		t.Fatalf("Follow = %d, want 1", code)
	}
	want := "pfm restart: worker pid 7: unreadable exit marker \"two\": " +
		"strconv.Atoi: parsing \"two\": invalid syntax; log " + logPath + "\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestSpawnStartsTheWorkerDetachedIntoItsLog(t *testing.T) {
	runner := &hostRunner{}
	launcher := Launcher{Runner: runner}
	pid, logPath, err := launcher.Spawn(
		context.Background(),
		[]string{"pfm", "internal", WorkerCommand, "--front", "7"},
		[]string{"HOME=/jail"},
		t.TempDir(),
		"restart.log",
	)
	if err != nil || pid != 4242 || filepath.Base(logPath) != "restart.log" {
		t.Fatalf("Spawn = %d, %q, %v", pid, logPath, err)
	}
	if len(runner.starts) != 1 || !runner.starts[0].Opts.Detach || runner.starts[0].Opts.Stdout == nil {
		t.Fatalf("starts = %+v, want one detached start writing to the log", runner.starts)
	}
	runner.startErr = errors.New("fork: resource unavailable")
	if _, _, err := launcher.Spawn(context.Background(), []string{"pfm"}, nil, t.TempDir(), "x.log"); err == nil {
		t.Fatal("a failed start reported a worker")
	}
}

func TestRestartWorkerEnvDropsThePaneAndKeepsTheHerdrBinary(t *testing.T) {
	got := restartWorkerEnv([]string{
		"HOME=/jail", "TMUX=/tmp/tmux-0/pfm-cc-a,1,0", "TMUX_PANE=%3",
		herdr.EnvPane + "=p1", herdr.EnvBin + "=/opt/bin/herdr",
	})
	if !slices.Equal(got, []string{"HOME=/jail", herdr.EnvBin + "=/opt/bin/herdr"}) {
		t.Fatalf("worker env = %v", got)
	}
}

func TestLaunchingChatIsTheTmuxSocketOrTheHerdrPane(t *testing.T) {
	original := restartIdentify
	t.Cleanup(func() { restartIdentify = original })
	for _, tc := range []struct {
		identity resolve.Identity
		err      error
		want     chatAddress
	}{
		{identity: resolve.Identity{Backend: "tmux", SocketPath: "/tmp/tmux-0/pfm-cc-a"}, want: chatAddress{Socket: "pfm-cc-a"}},
		{identity: resolve.Identity{Backend: "herdr", SocketPath: "/run/herdr.sock", Pane: "w1:p2"}, want: chatAddress{Socket: "/run/herdr.sock", Pane: "w1:p2"}},
		{err: resolve.ErrNotInChat},
	} {
		restartIdentify = func(context.Context) (resolve.Identity, error) { return tc.identity, tc.err }
		if got, err := launchingChat(); err != nil || got != tc.want {
			t.Fatalf("launcher=%+v, want %+v", got, tc.want)
		}
	}
}

func TestChatAddressHoldsOnlyItsOwnChat(t *testing.T) {
	tmuxChat := headless.Chat{Socket: "pfm-cc-a", Pane: "%0"}
	herdrChat := headless.Chat{Backend: spawn.BackendHerdr, Socket: "/run/herdr.sock", Pane: "w1:p2"}
	for _, test := range []struct {
		address chatAddress
		chat    headless.Chat
		want    bool
	}{
		{chatAddress{Socket: "pfm-cc-a"}, tmuxChat, true},
		{chatAddress{Socket: "pfm-cc-b"}, tmuxChat, false},
		{chatAddress{}, headless.Chat{}, false},
		{chatAddress{Socket: "/run/herdr.sock", Pane: "w1:p2"}, herdrChat, true},
		{chatAddress{Socket: "/run/herdr.sock", Pane: "w1:p3"}, herdrChat, false},
		{chatAddress{Socket: "/run/herdr.sock"}, herdrChat, false},
	} {
		if got := test.address.holds(test.chat); got != test.want {
			t.Fatalf("%+v holds %+v = %v, want %v", test.address, test.chat, got, test.want)
		}
	}
}

func TestThePublicVerbHasNoWorkerFlags(t *testing.T) {
	for _, row := range []struct {
		args []string
		want string
	}{{[]string{"--worker"}, "flag provided but not defined: -worker\n{usage}\n"}, {[]string{"--front", "5", "--dry-run"}, "flag provided but not defined: -front\n{usage}\n"}, {[]string{"--launched-from", "x"}, "flag provided but not defined: -launched-from\n{usage}\n"}, {[]string{"now"}, "{usage}\n"}} {
		var stdout, stderr bytes.Buffer
		code := Main(row.args, &stdout, &stderr, config.Runtime{})
		if got := strings.Replace(stderr.String(), usage, "{usage}", 1); code != 2 || got != row.want {
			t.Errorf("Main(%v) stderr = %q, want %q (exit %d)", row.args, got, row.want, code)
		}
	}
}

// TestWorkerRunsOnlyAsTheDetachedWorker: typed by hand the worker refuses —
// without --front, or outside a session of its own — and runs nothing.
func TestWorkerRunsOnlyAsTheDetachedWorker(t *testing.T) {
	original := sessionLeader
	t.Cleanup(func() { sessionLeader = original })
	sessionLeader = func() bool { return false } // a real worker run here would restart the fence
	for _, args := range [][]string{{}, {"--front", "0"}, {"--front", "5", "--launched-pane", "w1:p2"}, {"--front", "5", "x"}} {
		var stdout, stderr bytes.Buffer
		if code := Worker(args, &stdout, &stderr, config.Runtime{}); code != 2 ||
			stderr.String() != "usage: pfm internal restart-run --front PID [--launched-from SOCKET [--launched-pane PANE]]\n" {
			t.Fatalf("Worker(%v) = %d, stderr %q", args, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := Worker([]string{"--front", "5"}, &stdout, &stderr, config.Runtime{}); code != 2 ||
		stderr.String() != "pfm internal restart-run: runs only as the detached worker pfm restart starts in its own session; type pfm restart\n" || stdout.Len() != 0 {
		t.Fatalf("a foreground worker = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestSessionLeaderReadsThisProcess(t *testing.T) {
	sid, err := unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionLeader(); got != (sid == os.Getpid()) {
		t.Fatalf("sessionLeader = %v, sid %d pid %d", got, sid, os.Getpid())
	}
}

func TestLiveDepsReadsTheDaemonFromConfig(t *testing.T) {
	runtime := config.Runtime{Paths: paths.Values{StateDB: "/jail/state/fleet.db", Home: "/jail"}}
	plain := LiveDeps(runtime, &bytes.Buffer{}, []int{9}, chatAddress{Socket: "pfm-cc-a"})
	if plain.DaemonProbe != nil || plain.LaunchedFrom.Socket != "pfm-cc-a" ||
		!slices.Equal(plain.Spare, []int{9}) {
		t.Fatalf("plain deps = %+v", plain)
	}
	if plain.LogDir != "/jail/.local/state/pfm/log/restart" {
		t.Fatalf("log dir = %q, want the jailed home's log root", plain.LogDir)
	}
	runtime.Config.MCPServers = map[string]config.MCPServer{"chat": {Enabled: true}}
	configured := LiveDeps(runtime, &bytes.Buffer{}, nil, chatAddress{})
	if configured.DaemonProbe == nil {
		t.Fatalf("configured deps: daemon probe set %v", configured.DaemonProbe != nil)
	}
}

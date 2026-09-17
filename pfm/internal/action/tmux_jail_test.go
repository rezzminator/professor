package action

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type actionTmuxJail struct {
	root    string
	tmuxDir string
	sidDir  string
	home    string
	sockets []string
}

func newActionTmuxJail(t *testing.T) *actionTmuxJail {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "cca")
	if err != nil {
		t.Fatal(err)
	}
	jail := &actionTmuxJail{
		root:    root,
		tmuxDir: filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
		sidDir:  filepath.Join(root, "sid"),
		home:    filepath.Join(root, "home"),
	}
	for _, directory := range []string{
		jail.tmuxDir,
		jail.sidDir,
		jail.home,
		filepath.Join(root, "claude"),
		filepath.Join(root, "codex"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", jail.home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", jail.root)
	t.Setenv("PFM_HOME", jail.home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SID_DIR", jail.sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	// A chat server loads the user's ~/.tmux.conf in real life; a fixture must
	// not, or the machine it runs on steers the test.
	t.Setenv("PFM_TMUX_CONF", "/dev/null")
	t.Cleanup(func() {
		for _, socket := range jail.sockets {
			_ = jail.command("-L", socket, "kill-server").Run()
		}
		if err := os.RemoveAll(jail.root); err != nil {
			t.Errorf("remove action tmux jail: %v", err)
		}
	})
	return jail
}

func (jail *actionTmuxJail) command(arguments ...string) *exec.Cmd {
	command := exec.Command("tmux", arguments...)
	command.Env = append(
		os.Environ(),
		"HOME="+jail.home,
		"TMUX=",
		"TMUX_TMPDIR="+jail.root,
	)
	return command
}

func (jail *actionTmuxJail) start(
	t *testing.T,
	socket, session, command string,
) string {
	t.Helper()
	process := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		command,
	)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("start %s: %v: %s", socket, err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	return jail.paneID(t, socket)
}

func (jail *actionTmuxJail) paneID(t *testing.T, socket string) string {
	t.Helper()
	output, err := jail.command(
		"-L",
		socket,
		"list-panes",
		"-F",
		"#{pane_id}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func TestJailedSoloCompetingAttemptsAndSelfSwitch(t *testing.T) {
	jail := newActionTmuxJail(t)
	id := "66666666-6666-4666-8666-666666666666"
	keepSocket := "cc-600-1-1"
	duplicateSocket := "cc-700-1-1"
	sharedSocket := "cc-800-1-1"
	jail.start(t, keepSocket, "keep", "sleep 120")
	jail.start(t, duplicateSocket, "duplicate", "sleep 120")
	targetPane := jail.start(t, sharedSocket, "shared", "sleep 120")
	if output, err := jail.command(
		"-L",
		sharedSocket,
		"split-window",
		"-d",
		"sleep 120",
	).CombinedOutput(); err != nil {
		t.Fatalf("split shared server: %v: %s", err, output)
	}
	for name := range map[string]struct{}{
		keepSocket:                      {},
		duplicateSocket:                 {},
		sharedSocket + "." + targetPane: {},
	} {
		writeActionFile(
			t,
			filepath.Join(jail.sidDir, name),
			"/tx/"+id+".jsonl",
			0o600,
		)
	}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux: TmuxExecutor{
			Binary:  "tmux",
			TmuxDir: jail.tmuxDir,
		},
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	runErrors := make([]error, 2)
	for index := range runErrors {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			runErrors[index] = executor.Solo(
				context.Background(),
				id,
				keepSocket,
				false,
			)
		}()
	}
	wait.Wait()
	for index, runErr := range runErrors {
		if runErr != nil {
			t.Fatalf("solo attempt %d: %v", index, runErr)
		}
	}
	commandTmux := TmuxExecutor{Binary: "tmux", TmuxDir: jail.tmuxDir}
	if !commandTmux.SocketAlive(context.Background(), keepSocket) {
		t.Fatal("solo killed keep socket")
	}
	if commandTmux.SocketAlive(context.Background(), duplicateSocket) {
		t.Fatal("solo left duplicate standalone server alive")
	}
	sharedPanes, err := commandTmux.ListPanes(context.Background(), sharedSocket)
	if err != nil || len(sharedPanes) != 1 {
		t.Fatalf("shared panes=%#v err=%v, want one sibling", sharedPanes, err)
	}

	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(jail.root, "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	// A COPY, not a symlink. tmux reports pane_current_command from the
	// process's comm, and macOS sets that from the real executable behind a
	// symlink — so a link named "claude" pointing at sleep runs a process named
	// "sleep", and the engine window becomes unrecognizable. Linux happens to
	// report the link name, which is why this passed there.
	fakeEngine(t, sleepPath, claudePath)
	if output, err := jail.command(
		"-L",
		keepSocket,
		"new-window",
		"-d",
		"-n",
		"engine",
		claudePath+" 120",
	).CombinedOutput(); err != nil {
		t.Fatalf("add Claude window: %v: %s", err, output)
	}
	waitForPaneCommand(t, jail, keepSocket, ":1", "claude")
	if output, err := jail.command(
		"-L",
		keepSocket,
		"select-window",
		"-t",
		":0",
	).CombinedOutput(); err != nil {
		t.Fatalf("select shell window: %v: %s", err, output)
	}
	if !executor.SelfSwitch(
		context.Background(),
		filepath.Join(jail.tmuxDir, keepSocket)+",1,0",
		keepSocket,
	) {
		t.Fatal("SelfSwitch() did not recognize own socket")
	}
	output, err := jail.command(
		"-L",
		keepSocket,
		"display-message",
		"-p",
		"#{window_index}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != "1" {
		t.Fatalf("selfswitch active window=%q, want engine window 1", output)
	}
}

func waitForPaneCommand(t *testing.T, jail *actionTmuxJail, socket, target, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		output, err := jail.command(
			"-L", socket,
			"list-panes",
			"-t", target,
			"-F", "#{pane_current_command}\t#{pane_dead}\t#{pane_dead_status}",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("read pane command for %s: %v: %s", target, err, output)
		}
		last = strings.TrimSuffix(string(output), "\n")
		fields := strings.Split(last, "\t")
		if len(fields) != 3 {
			t.Fatalf("pane command row=%q, want command/dead/status", last)
		}
		if fields[0] == want {
			return
		}
		if fields[1] == "1" {
			t.Fatalf("pane %s died before reporting %q: command=%q status=%q", target, want, fields[0], fields[2])
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane %s never reported current command %q within 5s; last=%q", target, want, last)
}

// fakeEngine installs a runnable executable at target that behaves like the
// source binary but carries its own name, so tmux reports that name as the
// pane's current command on every platform.
func fakeEngine(t *testing.T, source, target string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	// Copying a system binary on arm64 macOS invalidates its signature and the
	// kernel refuses to exec it. An ad-hoc signature makes the copy runnable
	// again; without this the window dies at birth and the failure reads as a
	// selfswitch bug.
	if output, err := exec.Command("codesign", "--force", "--sign", "-", target).CombinedOutput(); err != nil {
		t.Skipf("cannot ad-hoc sign the fake engine: %v: %s", err, output)
	}
}

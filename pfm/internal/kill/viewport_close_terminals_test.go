package kill

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This file pins CloseTerminals — the second viewport shape (viewport.go) —
// plus the two real command seams it reads the world through.

type fakeClientLister struct {
	pids []int
	err  error
}

func (lister fakeClientLister) ClientPIDs(context.Context, string) ([]int, error) {
	return lister.pids, lister.err
}

type fakeProcessInfoTable struct {
	infoByPID map[int]ProcessInfo
	errByPID  map[int]error
}

func (table fakeProcessInfoTable) Info(_ context.Context, pid int) (ProcessInfo, error) {
	if err, bad := table.errByPID[pid]; bad {
		return ProcessInfo{}, err
	}
	info, found := table.infoByPID[pid]
	if !found {
		return ProcessInfo{}, fmt.Errorf("no such process %d", pid)
	}
	return info, nil
}

type fakeCloseSignaller struct {
	errByPID map[int]error
	signaled []int
}

func (signaller *fakeCloseSignaller) Signal(pid int, _ syscall.Signal) error {
	signaller.signaled = append(signaller.signaled, pid)
	if err, bad := signaller.errByPID[pid]; bad {
		return err
	}
	return nil
}

func TestCloseTerminals(t *testing.T) {
	for _, test := range []struct {
		name         string
		clients      []int
		infoByPID    map[int]ProcessInfo
		errByPID     map[int]error
		signalErrors map[int]error
		self         int
		wantClosed   []int
		wantSignaled []int
		wantSkipped  []string
	}{
		{
			// The chat's own pid lives at PID 100; killing it directly would
			// reach the pane the chat is running in, not the terminal tab
			// around it. Only the PARENT (200, the shell) may be signalled.
			name:    "signals the client's parent, never the client itself",
			clients: []int{100},
			infoByPID: map[int]ProcessInfo{
				100: {PID: 100, PPID: 200, Comm: "tmux"},
				200: {PID: 200, PPID: 1, Comm: "zsh"},
			},
			wantClosed:   []int{200},
			wantSignaled: []int{200},
		},
		{
			name:    "skips a parent at pid 1 or below — no terminal shell to close",
			clients: []int{101},
			infoByPID: map[int]ProcessInfo{
				101: {PID: 101, PPID: 1, Comm: "tmux"},
				1:   {PID: 1, PPID: 0, Comm: "launchd"},
			},
			wantSkipped: []string{
				"client 101: parent is pid 1 — no terminal shell to close",
			},
		},
		{
			name:    "skips a parent that is the closer's own pid",
			clients: []int{102},
			infoByPID: map[int]ProcessInfo{
				102: {PID: 102, PPID: 300, Comm: "tmux"},
				300: {PID: 300, PPID: 1, Comm: "zsh"},
			},
			self: 300,
			wantSkipped: []string{
				"client 102: parent 300 is this hook itself",
			},
		},
		{
			name:    "skips a parent that is not a terminal shell",
			clients: []int{103},
			infoByPID: map[int]ProcessInfo{
				103: {PID: 103, PPID: 400, Comm: "tmux"},
				400: {PID: 400, PPID: 1, Comm: "Code Helper"},
			},
			wantSkipped: []string{
				`client 103: parent 400 is "Code Helper", not a terminal shell`,
			},
		},
		{
			// Two vsct panes attached from the same VS Code terminal tab share
			// one parent shell; that shell must be hung up exactly once.
			name:    "de-duplicates two clients sharing one parent shell",
			clients: []int{104, 105},
			infoByPID: map[int]ProcessInfo{
				104: {PID: 104, PPID: 500, Comm: "tmux"},
				105: {PID: 105, PPID: 500, Comm: "tmux"},
				500: {PID: 500, PPID: 1, Comm: "bash"},
			},
			wantClosed:   []int{500},
			wantSignaled: []int{500},
		},
		{
			name:    "a client the process table cannot read is named, not silent",
			clients: []int{106},
			errByPID: map[int]error{
				106: fmt.Errorf("no such process"),
			},
			wantSkipped: []string{"client 106: no such process"},
		},
		{
			name:    "a parent the process table cannot read is named, not silent",
			clients: []int{107},
			infoByPID: map[int]ProcessInfo{
				107: {PID: 107, PPID: 600, Comm: "tmux"},
			},
			errByPID: map[int]error{
				600: fmt.Errorf("gone"),
			},
			wantSkipped: []string{"client 107 parent 600: gone"},
		},
		{
			name:    "a signal error is named, not silent, and never counted as closed",
			clients: []int{108},
			infoByPID: map[int]ProcessInfo{
				108: {PID: 108, PPID: 700, Comm: "zsh"},
				700: {PID: 700, PPID: 1, Comm: "zsh"},
			},
			signalErrors: map[int]error{700: fmt.Errorf("operation not permitted")},
			wantSignaled: []int{700},
			wantSkipped:  []string{"client 108: hang up shell 700: operation not permitted"},
		},
		{
			name:    "no attached client skips with a named reason",
			clients: nil,
			wantSkipped: []string{
				"no client attached — chat was not being watched",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			signaller := &fakeCloseSignaller{errByPID: test.signalErrors}
			closed, skipped, err := CloseTerminals(context.Background(), "socket", ViewportDeps{
				Tmux:      fakeClientLister{pids: test.clients},
				Processes: fakeProcessInfoTable{infoByPID: test.infoByPID, errByPID: test.errByPID},
				Signals:   signaller,
				Self:      test.self,
			})
			if err != nil {
				t.Fatalf("CloseTerminals error = %v", err)
			}
			if !reflect.DeepEqual(closed, test.wantClosed) {
				t.Fatalf("closed = %v, want %v", closed, test.wantClosed)
			}
			if !reflect.DeepEqual(signaller.signaled, test.wantSignaled) {
				t.Fatalf("signaled = %v, want %v", signaller.signaled, test.wantSignaled)
			}
			if !reflect.DeepEqual(skipped, test.wantSkipped) {
				t.Fatalf("skipped = %#v, want %#v", skipped, test.wantSkipped)
			}
		})
	}
}

func TestCloseTerminalsRequiresAllThreeSeams(t *testing.T) {
	full := ViewportDeps{
		Tmux:      fakeClientLister{},
		Processes: fakeProcessInfoTable{},
		Signals:   &fakeCloseSignaller{},
	}
	for _, test := range []struct {
		name string
		deps ViewportDeps
	}{
		{name: "nil tmux", deps: ViewportDeps{Processes: full.Processes, Signals: full.Signals}},
		{name: "nil process table", deps: ViewportDeps{Tmux: full.Tmux, Signals: full.Signals}},
		{name: "nil signaller", deps: ViewportDeps{Tmux: full.Tmux, Processes: full.Processes}},
	} {
		t.Run(test.name, func(t *testing.T) {
			closed, skipped, err := CloseTerminals(context.Background(), "socket", test.deps)
			if err == nil {
				t.Fatal("CloseTerminals with a missing seam returned no error")
			}
			if closed != nil || skipped != nil {
				t.Fatalf("closed=%v skipped=%v, want both nil alongside the error", closed, skipped)
			}
		})
	}
}

// TestCommandProcessTableInfoReadsRealProcess pins the real `ps` seam
// against the test binary's own pid — no fixture can fake a process table
// entry the OS itself did not create.
func TestCommandProcessTableInfoReadsRealProcess(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps binary is not installed")
	}
	self := os.Getpid()
	info, err := (CommandProcessTable{}).Info(context.Background(), self)
	if err != nil {
		t.Fatalf("Info(%d) error = %v", self, err)
	}
	if info.PID != self {
		t.Fatalf("info.PID = %d, want %d", info.PID, self)
	}
	if info.PPID != os.Getppid() {
		t.Fatalf("info.PPID = %d, want the test binary's real ppid %d", info.PPID, os.Getppid())
	}
	if strings.TrimSpace(info.Comm) == "" {
		t.Fatal("info.Comm is empty for a live process")
	}
}

// TestCommandTmuxClientPIDsRealServer pins the real `tmux list-clients`
// parse: no client attached reports an empty slice with no error, and a real
// attached client reports its own live pid.
func TestCommandTmuxClientPIDsRealServer(t *testing.T) {
	jail := newKillTmuxJail(t)
	ctx := context.Background()
	socket := "cc-viewport-clients"
	if output, err := jail.command(
		"-f", "/dev/null", "-L", socket, "new-session", "-d", "-s", "s1", "sleep 30",
	).CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	socketPath := jail.tmuxDir + "/" + socket

	pids, err := (CommandTmux{}).ClientPIDs(ctx, socketPath)
	if err != nil {
		t.Fatalf("ClientPIDs before attach: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("ClientPIDs before attach = %v, want empty", pids)
	}

	// A control-mode client (`tmux -C attach-session`) counts as an
	// attached client without needing a real pty. Its stdin is a pipe this
	// test holds open for the process's whole life — closing the write end
	// is what lets it see EOF and exit during cleanup.
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	attach := jail.command("-L", socket, "-C", "attach-session", "-t", "s1")
	attach.Stdin = stdinRead
	if err := attach.Start(); err != nil {
		t.Fatalf("start control client: %v", err)
	}
	_ = stdinRead.Close()
	t.Cleanup(func() {
		_ = stdinWrite.Close()
		_ = attach.Process.Kill()
		_, _ = attach.Process.Wait()
	})

	var pidsAfter []int
	for attempt := 0; attempt < 50; attempt++ {
		pidsAfter, err = (CommandTmux{}).ClientPIDs(ctx, socketPath)
		if err != nil {
			t.Fatalf("ClientPIDs after attach: %v", err)
		}
		if len(pidsAfter) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pidsAfter) != 1 {
		t.Fatalf("ClientPIDs after attach = %v, want exactly one client pid", pidsAfter)
	}
	if err := exec.Command("kill", "-0", strconv.Itoa(pidsAfter[0])).Run(); err != nil {
		t.Fatalf("reported client pid %d is not a live process: %v", pidsAfter[0], err)
	}
}

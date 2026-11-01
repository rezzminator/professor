package kill

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gather/gathertest"
)

// This file pins CloseTerminals — the second viewport shape (viewport.go) —
// plus the real tmux seam it reads its clients through.

const viewportUID = 501

type fakeClientLister struct {
	pids []int
	err  error
}

func (lister fakeClientLister) ClientPIDs(context.Context, string) ([]int, error) {
	return lister.pids, lister.err
}

// rereadTable runs onReread when a pid's Stat is read a second time: the
// moment between the closer identifying a client's parent and re-reading the
// client.
type rereadTable struct {
	*gathertest.Table
	reads    map[int]int
	onReread func(table *gathertest.Table, pid int)
}

func (table rereadTable) Stat(pid int) (gather.ProcStat, error) {
	table.reads[pid]++
	if table.reads[pid] == 2 && table.onReread != nil {
		table.onReread(table.Table, pid)
	}
	return table.Table.Stat(pid)
}

func tmuxClient(parent int) *gathertest.Process {
	return &gathertest.Process{
		Start: 1000, UID: viewportUID, ParentPID: parent, Argv: []string{"tmux", "attach"}, Exe: "/usr/bin/tmux",
	}
}

func shellProcess(exe string, argv ...string) *gathertest.Process {
	return &gathertest.Process{Start: 2000, UID: viewportUID, ParentPID: 1, Argv: argv, Exe: exe}
}

func TestCloseTerminals(t *testing.T) {
	for _, test := range []struct {
		name        string
		clients     []int
		processes   map[int]*gathertest.Process
		self        int
		onReread    func(table *gathertest.Table, pid int)
		onOpen      func(table *gathertest.Table, pid int)
		signalErr   error
		wantClosed  []int
		wantSent    []gathertest.Sent
		wantSkipped []string
	}{
		{
			// The chat's own pid lives at PID 100; killing it directly would
			// reach the pane the chat is running in, not the terminal tab
			// around it. Only the PARENT (200, the shell) may be signalled.
			name:    "signals the client's parent, never the client itself",
			clients: []int{100},
			processes: map[int]*gathertest.Process{
				100: tmuxClient(200),
				200: shellProcess("/bin/zsh", "-zsh"),
			},
			wantClosed: []int{200},
			wantSent:   []gathertest.Sent{{PID: 200, Sig: syscall.SIGHUP}},
		},
		{
			name:    "skips a parent at pid 1 or below — no terminal shell to close",
			clients: []int{101},
			processes: map[int]*gathertest.Process{
				101: tmuxClient(1),
				1:   shellProcess("/sbin/launchd", "/sbin/launchd"),
			},
			wantSkipped: []string{"client 101: parent is pid 1 — no terminal shell to close"},
		},
		{
			name:    "skips a parent that is the closer's own pid",
			clients: []int{102},
			processes: map[int]*gathertest.Process{
				102: tmuxClient(300),
				300: shellProcess("/bin/zsh", "zsh"),
			},
			self:        300,
			wantSkipped: []string{"client 102: parent 300 is this hook itself"},
		},
		{
			name:    "skips a parent that is not a terminal shell",
			clients: []int{103},
			processes: map[int]*gathertest.Process{
				103: tmuxClient(400),
				400: shellProcess("/opt/Code/Code Helper", "Code Helper"),
			},
			wantSkipped: []string{`client 103: parent 400 is "Code Helper", not a terminal shell`},
		},
		{
			// A shell is told by the executable the kernel runs, never by an
			// argv[0] the process can rename.
			name:    "skips a parent that only names itself a shell",
			clients: []int{103},
			processes: map[int]*gathertest.Process{
				103: tmuxClient(400),
				400: shellProcess("/usr/bin/python3", "zsh"),
			},
			wantSkipped: []string{`client 103: parent 400 is "python3", not a terminal shell`},
		},
		{
			name:    "skips a parent whose executable cannot be read",
			clients: []int{103},
			processes: map[int]*gathertest.Process{
				103: tmuxClient(400),
				400: shellProcess("", "zsh"),
			},
			wantSkipped: []string{
				"client 103: parent 400's executable is unreadable, so it is not known to be a terminal shell",
			},
		},
		{
			// Two vsct panes attached from the same VS Code terminal tab share
			// one parent shell; that shell must be hung up exactly once.
			name:    "de-duplicates two clients sharing one parent shell",
			clients: []int{104, 105},
			processes: map[int]*gathertest.Process{
				104: tmuxClient(500),
				105: tmuxClient(500),
				500: shellProcess("/usr/bin/bash", "bash"),
			},
			wantClosed: []int{500},
			wantSent:   []gathertest.Sent{{PID: 500, Sig: syscall.SIGHUP}},
		},
		{
			name:        "a client the process table cannot read is named, not silent",
			clients:     []int{106},
			wantSkipped: []string{"client 106: file does not exist"},
		},
		{
			name:      "a parent the process table cannot read is named, not silent",
			clients:   []int{107},
			processes: map[int]*gathertest.Process{107: tmuxClient(600)},
			wantSkipped: []string{
				"client 107 parent 600: identify process 600: owner: file does not exist (file does not exist)",
			},
		},
		{
			// The parent died and its pid went to another shell between the
			// client's two reads: the client, reparented, no longer names it.
			name:    "a parent replaced while it was read is left alone",
			clients: []int{110},
			processes: map[int]*gathertest.Process{
				110: tmuxClient(900),
				900: shellProcess("/bin/zsh", "zsh"),
			},
			onReread: func(table *gathertest.Table, pid int) {
				if pid == 110 {
					table.Processes[110].ParentPID = 1
				}
			},
			wantSkipped: []string{"client 110: left parent 900 while it was read"},
		},
		{
			name:    "a shell whose pid went to a new process before the hang-up is never signalled",
			clients: []int{100},
			processes: map[int]*gathertest.Process{
				100: tmuxClient(200),
				200: shellProcess("/bin/zsh", "-zsh"),
			},
			onOpen: func(table *gathertest.Table, pid int) {
				table.Processes[pid].Start = 2999
			},
			wantSkipped: []string{"client 100: shell 200 ended before the hang-up"},
		},
		{
			name:    "a shell that became another program before the hang-up is never signalled",
			clients: []int{100},
			processes: map[int]*gathertest.Process{
				100: tmuxClient(200),
				200: shellProcess("/bin/zsh", "-zsh"),
			},
			onOpen: func(table *gathertest.Table, pid int) {
				table.Processes[pid].Argv = []string{"vim", "notes.txt"}
				table.Processes[pid].Exe = "/usr/bin/vim"
			},
			wantSkipped: []string{"client 100: shell 200 changed before the hang-up; left alone"},
		},
		{
			name:    "another user's shell is never signalled",
			clients: []int{100},
			processes: map[int]*gathertest.Process{
				100: tmuxClient(200),
				200: {Start: 2000, UID: 0, ParentPID: 1, Argv: []string{"-zsh"}, Exe: "/bin/zsh"},
			},
			wantSkipped: []string{"client 100: hang up shell 200: pid 200: signal refused: runs as uid 0, not 501"},
		},
		{
			name:    "a signal error is named, not silent, and never counted as closed",
			clients: []int{108},
			processes: map[int]*gathertest.Process{
				108: tmuxClient(700),
				700: shellProcess("/bin/zsh", "zsh"),
			},
			signalErr:   syscall.EPERM,
			wantSkipped: []string{"client 108: hang up shell 700: send hangup to pid 700: operation not permitted"},
		},
		{
			name:        "no attached client skips with a named reason",
			clients:     nil,
			wantSkipped: []string{"no client attached — chat was not being watched"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := &gathertest.Table{Processes: test.processes}
			if table.Processes == nil {
				table.Processes = map[int]*gathertest.Process{}
			}
			kernel := &gathertest.Kernel{
				OnOpen: func(pid int) error {
					if test.onOpen != nil {
						test.onOpen(table, pid)
					}
					return nil
				},
				OnSignal: func(int, syscall.Signal) error { return test.signalErr },
			}
			self := test.self
			if self == 0 {
				self = 99999
			}
			procs := rereadTable{Table: table, reads: map[int]int{}, onReread: test.onReread}
			closed, skipped, err := CloseTerminals(context.Background(), "socket", ViewportDeps{
				Tmux: fakeClientLister{pids: test.clients},
				Door: gathertest.Door(procs, kernel, viewportUID, self),
			})
			if err != nil {
				t.Fatalf("CloseTerminals error = %v", err)
			}
			if !reflect.DeepEqual(closed, test.wantClosed) {
				t.Fatalf("closed = %v, want %v", closed, test.wantClosed)
			}
			if !reflect.DeepEqual(kernel.Sent, test.wantSent) {
				t.Fatalf("signals sent = %v, want %v", kernel.Sent, test.wantSent)
			}
			if !reflect.DeepEqual(skipped, test.wantSkipped) {
				t.Fatalf("skipped = %#v, want %#v", skipped, test.wantSkipped)
			}
		})
	}
}

func TestCloseTerminalsRequiresAllThreeSeams(t *testing.T) {
	full := ViewportDeps{
		Tmux: fakeClientLister{},
		Door: gathertest.Door(&gathertest.Table{}, &gathertest.Kernel{}, viewportUID, 99999),
	}
	noProcs, noKernel := full.Door, full.Door
	noProcs.Procs, noKernel.Kernel = nil, nil
	for _, test := range []struct {
		name string
		deps ViewportDeps
	}{
		{name: "nil tmux", deps: ViewportDeps{Door: full.Door}},
		{name: "nil process table", deps: ViewportDeps{Tmux: full.Tmux, Door: noProcs}},
		{name: "nil signal kernel", deps: ViewportDeps{Tmux: full.Tmux, Door: noKernel}},
	} {
		t.Run(test.name, func(t *testing.T) {
			closed, skipped, err := CloseTerminals(context.Background(), "socket", test.deps)
			const want = "viewport closer needs tmux, a process table, and a signal kernel"
			if err == nil || err.Error() != want {
				t.Fatalf("CloseTerminals with a missing seam error = %v, want %q", err, want)
			}
			if closed != nil || skipped != nil {
				t.Fatalf("closed=%v skipped=%v, want both nil alongside the error", closed, skipped)
			}
		})
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

	pids, err := (TmuxKiller{}).ClientPIDs(ctx, socketPath)
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
		pidsAfter, err = (TmuxKiller{}).ClientPIDs(ctx, socketPath)
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

package hostcheck

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gather/gathertest"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeTmuxServer is one server the fake tmux answers for: empty unless it
// holds panes, from the paneFromCall-th read on when paneFromCall is set.
type fakeTmuxServer struct {
	pid          int
	panes        int
	paneFromCall int
	ignoreTERM   bool
	ignoreKILL   bool
	dead         bool
	calls        int
	failure      error
	// pidReads counts ServerPID answers; from the second on, the server
	// answers secondPID when that is set.
	pidReads  int
	secondPID int
}

type fakeTmux struct {
	servers map[string]*fakeTmuxServer
}

func (fake *fakeTmux) ListPanes(_ context.Context, socket string) ([]gather.ProbePane, error) {
	server := fake.servers[socket]
	if server == nil || server.dead {
		return nil, fmt.Errorf("%w: %s", gather.ErrServerGone, socket)
	}
	server.calls++
	if server.failure != nil {
		return nil, server.failure
	}
	if server.panes > 0 || (server.paneFromCall > 0 && server.calls >= server.paneFromCall) {
		return []gather.ProbePane{{Socket: socket, PaneID: "%1"}}, nil
	}
	return nil, fmt.Errorf("%w: %s", gather.ErrServerEmpty, socket)
}

func (fake *fakeTmux) ServerPID(_ context.Context, socket string) (int, error) {
	server := fake.servers[socket]
	if server == nil || server.dead {
		return 0, fmt.Errorf("%w: %s", gather.ErrServerGone, socket)
	}
	server.pidReads++
	if server.pidReads > 1 && server.secondPID != 0 {
		return server.secondPID, nil
	}
	return server.pid, nil
}

// reapUID is the uid the fake reaper's door acts as.
const reapUID = 1000

// reapTable is the process table behind the fake servers: each server's pid
// runs tmux with the command line pfm starts a chat server with, naming its
// socket under dir by -S.
func reapTable(dir string, fake *fakeTmux) *gathertest.Table {
	table := &gathertest.Table{Processes: map[int]*gathertest.Process{}}
	for socket, server := range fake.servers {
		table.Processes[server.pid] = &gathertest.Process{
			Start: uint64(5000 + server.pid),
			UID:   reapUID,
			Argv:  []string{"/usr/bin/tmux", "-S", filepath.Join(dir, socket), "new-session", "-d", "-s", "chat"},
			Exe:   "/usr/bin/tmux",
		}
	}
	return table
}

// reapKernel delivers signals to the fake servers: a server that honours a
// signal exits, its pid a zombie and its socket gone.
func reapKernel(fake *fakeTmux, table *gathertest.Table) *gathertest.Kernel {
	return &gathertest.Kernel{OnSignal: func(pid int, sig syscall.Signal) error {
		for _, server := range fake.servers {
			if server.pid != pid {
				continue
			}
			if (sig == syscall.SIGTERM && !server.ignoreTERM) || (sig == syscall.SIGKILL && !server.ignoreKILL) {
				server.dead = true
				table.Processes[pid].Zombie = true
			}
		}
		return nil
	}}
}

// tmuxServersFixture makes a real (closed) unix socket file per name under a
// short directory, since the probe lists only socket special files.
func tmuxServersFixture(t *testing.T, sockets ...string) string {
	t.Helper()
	dir := testjail.ShortRoot(t)
	for _, socket := range sockets {
		path := filepath.Join(dir, socket)
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatalf("create socket %q: %v", path, err)
		}
		listener.SetUnlinkOnClose(false)
		if err := listener.Close(); err != nil {
			t.Fatalf("close socket %q: %v", path, err)
		}
	}
	return dir
}

// fleetFixture: one abandoned empty pfm server, one pfm server with a live
// pane, and one empty server on a socket pfm does not own.
func fleetFixture(t *testing.T) (string, *fakeTmux) {
	dir := tmuxServersFixture(t, "cc-1-2-3", "cx-4-5-6", "work")
	return dir, &fakeTmux{servers: map[string]*fakeTmuxServer{
		"cc-1-2-3": {pid: 101},
		"cx-4-5-6": {pid: 102, panes: 1},
		"work":     {pid: 103},
	}}
}

func newFakeReaper(fake *fakeTmux, table *gathertest.Table, kernel *gathertest.Kernel) TmuxReaper {
	return TmuxReaper{
		Client: fake,
		Door:   gathertest.Door(table, kernel, reapUID, os.Getpid()),
		Wait:   5 * time.Second,
		Poll:   100 * time.Millisecond,
	}
}

func TestTmuxServersRowsAnAbandonedEmptyServerWithTheReapFix(t *testing.T) {
	dir, fake := fleetFixture(t)
	rows := detect(t, checkTmuxServers, Env{TmuxDir: dir, Tmux: fake})
	assertRows(t, rows, Row{
		Warn,
		checkTmuxServers,
		filepath.Join(dir, "cc-1-2-3"),
		"abandoned empty tmux server: it runs with no session and no pane yet holds a pfm chat socket, " +
			"so it counts as an open chat",
		"pfm chat ls --reap (SIGTERM, then SIGKILL after 5s; a server with a live pane is never touched)",
	})
}

// A failed probe is a row naming the failure, never an empty "nothing found".
func TestTmuxServersProbeFailureReadsAsFailure(t *testing.T) {
	dir := tmuxServersFixture(t, "cc-1-2-3")
	fake := &fakeTmux{servers: map[string]*fakeTmuxServer{
		"cc-1-2-3": {pid: 101, failure: fmt.Errorf("list-panes: exit status 1: permission denied")},
	}}
	rows := detect(t, checkTmuxServers, Env{TmuxDir: dir, Tmux: fake})
	if len(rows) != 1 || rows[0].Path != dir ||
		!strings.HasPrefix(rows[0].Problem, "tmux probe failed: cc-1-2-3: could not read this tmux server: ") ||
		!strings.Contains(rows[0].Problem, "permission denied") {
		t.Fatalf("rows = %+v, want one failure row naming cc-1-2-3", rows)
	}

	fake.servers["cc-1-2-3"].failure = &exec.Error{Name: "tmux", Err: exec.ErrNotFound}
	rows = detect(t, checkTmuxServers, Env{TmuxDir: dir, Tmux: fake})
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Problem, "not checked: probing the pfm tmux servers failed: ") {
		t.Fatalf("rows = %+v, want one not-checked row when tmux cannot run", rows)
	}
}

// The reap signals only the server it identified: the pid the server names,
// running tmux that names this socket, as the same generation, owner and
// command line at every signal. Changes land as the door opens the pid,
// between the identity read and the signal.
func TestReapSignalsOnlyTheIdentifiedServer(t *testing.T) {
	term := gathertest.Sent{PID: 101, Sig: syscall.SIGTERM}
	kill := gathertest.Sent{PID: 101, Sig: syscall.SIGKILL}
	tests := []struct {
		name   string
		server func(server *fakeTmuxServer, process *gathertest.Process, dir string)
		onOpen func(process *gathertest.Process, opens int)
		sent   []gathertest.Sent
		failed int
		stdout string
	}{
		{
			name:   "a server that honours SIGTERM ends by it",
			sent:   []gathertest.Sent{term},
			stdout: "cc-1-2-3 (pid 101): ended by SIGTERM\n",
		},
		{
			name: "a server started by -L label is the same server",
			server: func(_ *fakeTmuxServer, process *gathertest.Process, _ string) {
				process.Argv = []string{"tmux", "-L", "cc-1-2-3", "new-session", "-d"}
			},
			sent:   []gathertest.Sent{term},
			stdout: "cc-1-2-3 (pid 101): ended by SIGTERM\n",
		},
		{
			name:   "a server ignoring SIGTERM gets SIGKILL after the bounded wait",
			server: func(server *fakeTmuxServer, _ *gathertest.Process, _ string) { server.ignoreTERM = true },
			sent:   []gathertest.Sent{term, kill},
			stdout: "cc-1-2-3 (pid 101): ignored SIGTERM for 5s, ended by SIGKILL\n",
		},
		{
			name: "a server surviving SIGKILL is reported running",
			server: func(server *fakeTmuxServer, _ *gathertest.Process, _ string) {
				server.ignoreTERM, server.ignoreKILL = true, true
			},
			sent:   []gathertest.Sent{term, kill},
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): still running 5s after SIGKILL\n",
		},
		{
			name:   "a pid reused before SIGTERM is never signalled",
			onOpen: func(process *gathertest.Process, _ int) { process.Start = 9999 },
			stdout: "cc-1-2-3: already gone\n",
		},
		{
			name:   "a pid now another user's is never signalled",
			onOpen: func(process *gathertest.Process, _ int) { process.UID = 0 },
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): left alone, the process planned as " +
				"[\"/usr/bin/tmux\" \"-S\" \"{dir}/cc-1-2-3\" \"new-session\" \"-d\" \"-s\" \"chat\"] " +
				"changed before SIGTERM\n",
		},
		{
			name:   "a pid running another command line is never signalled",
			onOpen: func(process *gathertest.Process, _ int) { process.Argv = []string{"/usr/bin/vim"} },
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): left alone, the process planned as " +
				"[\"/usr/bin/tmux\" \"-S\" \"{dir}/cc-1-2-3\" \"new-session\" \"-d\" \"-s\" \"chat\"] " +
				"changed before SIGTERM\n",
		},
		{
			name:   "SIGKILL is withheld from a process that changed after SIGTERM",
			server: func(server *fakeTmuxServer, _ *gathertest.Process, _ string) { server.ignoreTERM = true },
			onOpen: func(process *gathertest.Process, opens int) {
				if opens == 2 {
					process.Argv = []string{"/usr/bin/vim"}
				}
			},
			sent:   []gathertest.Sent{term},
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): not ended: pid 101 changed identity after SIGTERM; SIGKILL withheld\n",
		},
		{
			name: "a pid whose command line names another socket is never signalled",
			server: func(_ *fakeTmuxServer, process *gathertest.Process, dir string) {
				process.Argv = []string{"/usr/bin/tmux", "-S", filepath.Join(dir, "cc-9-9-9"), "new-session"}
			},
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): left alone, its command line " +
				"[\"/usr/bin/tmux\" \"-S\" \"{dir}/cc-9-9-9\" \"new-session\"] is not tmux naming {dir}/cc-1-2-3\n",
		},
		{
			name: "a pid that is not tmux is never signalled",
			server: func(_ *fakeTmuxServer, process *gathertest.Process, _ string) {
				process.Argv = []string{"/usr/bin/vim", "-L", "cc-1-2-3"}
			},
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): left alone, its command line " +
				"[\"/usr/bin/vim\" \"-L\" \"cc-1-2-3\"] is not tmux naming {dir}/cc-1-2-3\n",
		},
		{
			name:   "a server owned by another user is refused",
			server: func(_ *fakeTmuxServer, process *gathertest.Process, _ string) { process.UID = 0 },
			failed: 1,
			stdout: "cc-1-2-3 (pid 101): not ended: pid 101: signal refused: runs as uid 0, not 1000\n",
		},
		{
			name:   "a server naming another pid on the second read is left alone",
			server: func(server *fakeTmuxServer, _ *gathertest.Process, _ string) { server.secondPID = 202 },
			failed: 1,
			stdout: "cc-1-2-3: left alone, it answered pid 101 then 202\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, fake := fleetFixture(t)
			table := reapTable(dir, fake)
			if test.server != nil {
				test.server(fake.servers["cc-1-2-3"], table.Processes[101], dir)
			}
			kernel := reapKernel(fake, table)
			kernel.OnOpen = func(pid int) error {
				if test.onOpen != nil && pid == 101 {
					test.onOpen(table.Processes[101], kernel.Opens)
				}
				return nil
			}
			reaper := newFakeReaper(fake, table, kernel)
			start := reaper.Door.Clock.Now()
			var stdout bytes.Buffer
			failed, err := ReapTmuxServers(context.Background(), dir, reaper, &stdout)
			if err != nil {
				t.Fatalf("reap: %v", err)
			}
			want := strings.ReplaceAll(test.stdout, "{dir}", dir)
			if failed != test.failed || stdout.String() != want {
				t.Fatalf("failed=%d stdout=%q, want failed=%d stdout=%q", failed, stdout.String(), test.failed, want)
			}
			if !reflect.DeepEqual(kernel.Sent, test.sent) {
				t.Fatalf("signals = %v, want %v (and nothing to the live or foreign server)", kernel.Sent, test.sent)
			}
			if slices.Contains(test.sent, kill) {
				if waited := reaper.Door.Clock.Now().Sub(start); waited < 5*time.Second {
					t.Fatalf("SIGKILL came after %s, before the 5s bounded wait", waited)
				}
			}
		})
	}
}

// A server that gained a pane between the probe and the reap is live again.
func TestReapLeavesAServerThatGainedAPane(t *testing.T) {
	dir := tmuxServersFixture(t, "cc-1-2-3")
	// The probe reads it twice (one retry on any failure); the reap's re-read
	// is the third call.
	fake := &fakeTmux{servers: map[string]*fakeTmuxServer{"cc-1-2-3": {pid: 101, paneFromCall: 3}}}
	table := reapTable(dir, fake)
	kernel := reapKernel(fake, table)
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, table, kernel), &stdout)
	if err != nil || failed != 0 || len(kernel.Sent) != 0 ||
		stdout.String() != "cc-1-2-3: left alone, it now holds 1 pane(s)\n" {
		t.Fatalf("failed=%d err=%v signals=%v stdout=%q", failed, err, kernel.Sent, stdout.String())
	}
}

func TestReapWithNothingAbandonedSaysSo(t *testing.T) {
	dir := tmuxServersFixture(t, "cx-4-5-6", "work")
	fake := &fakeTmux{servers: map[string]*fakeTmuxServer{"cx-4-5-6": {pid: 102, panes: 1}, "work": {pid: 103}}}
	table := reapTable(dir, fake)
	kernel := reapKernel(fake, table)
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, table, kernel), &stdout)
	if err != nil || failed != 0 || len(kernel.Sent) != 0 ||
		stdout.String() != "no abandoned empty pfm tmux server under "+dir+"\n" {
		t.Fatalf("failed=%d err=%v signals=%v stdout=%q", failed, err, kernel.Sent, stdout.String())
	}
}

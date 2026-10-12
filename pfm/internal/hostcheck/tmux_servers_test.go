package hostcheck

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
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
}

type fakeTmux struct {
	servers map[string]*fakeTmuxServer
	signals []string
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
	return server.pid, nil
}

func (fake *fakeTmux) signal(pid int, signal syscall.Signal) error {
	for _, server := range fake.servers {
		if server.pid != pid {
			continue
		}
		if server.dead {
			return syscall.ESRCH
		}
		if signal == 0 {
			return nil
		}
		fake.signals = append(fake.signals, fmt.Sprintf("%d %v", pid, signal))
		if (signal == syscall.SIGTERM && !server.ignoreTERM) || (signal == syscall.SIGKILL && !server.ignoreKILL) {
			server.dead = true
		}
		return nil
	}
	return syscall.ESRCH
}

type fakeReapClock struct{ now time.Time }

func (clock *fakeReapClock) sleep(_ context.Context, duration time.Duration) error {
	clock.now = clock.now.Add(duration)
	return nil
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

func newFakeReaper(fake *fakeTmux, clock *fakeReapClock) TmuxReaper {
	return TmuxReaper{
		Client: fake,
		Signal: fake.signal,
		Sleep:  clock.sleep,
		Now:    func() time.Time { return clock.now },
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
	if len(fake.signals) != 0 {
		t.Fatalf("the detector signalled: %v", fake.signals)
	}
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

func TestReapSendsKillAfterTheBoundedWaitWhenTermIsIgnored(t *testing.T) {
	dir, fake := fleetFixture(t)
	fake.servers["cc-1-2-3"].ignoreTERM = true
	clock := &fakeReapClock{now: time.Unix(1_000_000, 0)}
	start := clock.now
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, clock), &stdout)
	if err != nil || failed != 0 {
		t.Fatalf("reap failed=%d err=%v stdout=%q", failed, err, stdout.String())
	}
	if want := []string{"101 terminated", "101 killed"}; !reflect.DeepEqual(fake.signals, want) {
		t.Fatalf("signals = %v, want %v (and nothing to the live or foreign server)", fake.signals, want)
	}
	if waited := clock.now.Sub(start); waited < 5*time.Second {
		t.Fatalf("SIGKILL came after %s, before the 5s bounded wait", waited)
	}
	if want := "cc-1-2-3 (pid 101): ignored SIGTERM for 5s, ended by SIGKILL\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestReapEndsAServerThatHonoursTerm(t *testing.T) {
	dir, fake := fleetFixture(t)
	clock := &fakeReapClock{now: time.Unix(1_000_000, 0)}
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, clock), &stdout)
	if err != nil || failed != 0 || !reflect.DeepEqual(fake.signals, []string{"101 terminated"}) ||
		stdout.String() != "cc-1-2-3 (pid 101): ended by SIGTERM\n" {
		t.Fatalf("failed=%d err=%v signals=%v stdout=%q", failed, err, fake.signals, stdout.String())
	}
}

// A server that gained a pane between the probe and the reap is live again.
func TestReapLeavesAServerThatGainedAPane(t *testing.T) {
	dir := tmuxServersFixture(t, "cc-1-2-3")
	// The probe reads it twice (one retry on any failure); the reap's re-read
	// is the third call.
	fake := &fakeTmux{servers: map[string]*fakeTmuxServer{"cc-1-2-3": {pid: 101, paneFromCall: 3}}}
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, &fakeReapClock{}), &stdout)
	if err != nil || failed != 0 || len(fake.signals) != 0 ||
		stdout.String() != "cc-1-2-3: left alone, it now holds 1 pane(s)\n" {
		t.Fatalf("failed=%d err=%v signals=%v stdout=%q", failed, err, fake.signals, stdout.String())
	}
}

func TestReapReportsAServerThatSurvivesKill(t *testing.T) {
	dir := tmuxServersFixture(t, "cc-1-2-3")
	fake := &fakeTmux{servers: map[string]*fakeTmuxServer{
		"cc-1-2-3": {pid: 101, ignoreTERM: true, ignoreKILL: true},
	}}
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, &fakeReapClock{}), &stdout)
	if err != nil || failed != 1 || stdout.String() != "cc-1-2-3 (pid 101): still running 5s after SIGKILL\n" {
		t.Fatalf("failed=%d err=%v stdout=%q", failed, err, stdout.String())
	}
}

func TestReapWithNothingAbandonedSaysSo(t *testing.T) {
	dir := tmuxServersFixture(t, "cx-4-5-6", "work")
	fake := &fakeTmux{servers: map[string]*fakeTmuxServer{"cx-4-5-6": {pid: 102, panes: 1}, "work": {pid: 103}}}
	var stdout bytes.Buffer
	failed, err := ReapTmuxServers(context.Background(), dir, newFakeReaper(fake, &fakeReapClock{}), &stdout)
	if err != nil || failed != 0 || len(fake.signals) != 0 ||
		stdout.String() != "no abandoned empty pfm tmux server under "+dir+"\n" {
		t.Fatalf("failed=%d err=%v signals=%v stdout=%q", failed, err, fake.signals, stdout.String())
	}
}

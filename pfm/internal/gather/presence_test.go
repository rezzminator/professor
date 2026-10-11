package gather

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// listenChatSocket binds a real unix socket named like a chat's tmux socket.
func listenChatSocket(t *testing.T, directory, name string) net.Listener {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("listen on chat socket %q: %v", name, err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Logf("close chat socket %q: %v", name, err)
		}
	})
	return listener
}

func TestPresenceFilesSeesSocketsAndCrumbsByNameAndContentNotMtime(t *testing.T) {
	tmuxDir, sidDir := t.TempDir(), t.TempDir()
	probe := PresenceProbe{TmuxDir: tmuxDir, SIDDir: sidDir}
	crumb := filepath.Join(sidDir, "cc-1800000000-1-1")
	if err := os.WriteFile(crumb, []byte("/t/one.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Not a crumb and not a chat socket: neither may move the fingerprint.
	if err := os.WriteFile(filepath.Join(sidDir, "scratch.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := probe.Files()
	if err != nil {
		t.Fatal(err)
	}

	// The statusline rewrites a crumb every tick: same content, newer mtime.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(crumb, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidDir, "scratch.tmp"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := probe.Files(); err != nil || got != first {
		t.Fatalf("a rewritten crumb or a foreign file moved the fingerprint: %q != %q (err %v)", got, first, err)
	}

	// A split socket: both panes' statuslines rewrite the one bare socket crumb
	// with their own transcript every tick. That flip is no chat born or gone.
	if err := os.WriteFile(crumb, []byte("/t/other-pane.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := probe.Files(); err != nil || got != first {
		t.Fatalf("two panes rewriting their shared socket crumb moved the fingerprint: %q != %q (err %v)",
			got, first, err)
	}

	paneCrumb := filepath.Join(sidDir, "cc-1800000000-1-1.%3")
	if err := os.WriteFile(paneCrumb, []byte("/t/one.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withPane, err := probe.Files()
	if err != nil || withPane == first {
		t.Fatalf("a new pane crumb must move the fingerprint: %q (err %v)", withPane, err)
	}
	if err := os.WriteFile(paneCrumb, []byte("/t/two.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := probe.Files(); got == withPane {
		t.Fatal("a pane crumb that now names another transcript left the fingerprint unchanged")
	}

	before, _ := probe.Files()
	listenChatSocket(t, tmuxDir, "cc-1800000000-2-2")
	after, err := probe.Files()
	if err != nil || after == before || !strings.Contains(after, "socket:cc-1800000000-2-2:") {
		t.Fatalf("a new chat socket must move the fingerprint: %q vs %q (err %v)", after, before, err)
	}
}

func TestPresenceFilesRefusesAnUnreadableDirectoryInsteadOfReportingNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := (PresenceProbe{TmuxDir: file, SIDDir: t.TempDir()}).Files(); err == nil {
		t.Fatalf("an unreadable socket directory returned %q, want an error", got)
	}
	absent := filepath.Join(t.TempDir(), "missing")
	if got, err := (PresenceProbe{TmuxDir: absent, SIDDir: absent}).Files(); err != nil || got == "" {
		t.Fatalf("an absent directory is a legitimate empty fleet, got %q err %v", got, err)
	}
}

func TestPresenceProcsSeesDeathReuseAndRefusedSockets(t *testing.T) {
	tmuxDir := t.TempDir()
	listener := listenChatSocket(t, tmuxDir, "cc-1800000000-1-1")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{StartTime: 7}},
		200: {stat: ProcStat{StartTime: 9}},
	}}
	probe := PresenceProbe{TmuxDir: tmuxDir, Proc: proc}
	live := Snapshot{
		Panes:           []ProbePane{{Socket: "cc-1800000000-1-1", PID: 100}},
		ClaudeProcesses: []ClaudeProcess{{PID: 200, PanePID: 100}},
	}
	base, settled, err := probe.Procs(live)
	if err != nil || !settled {
		t.Fatalf("baseline = %q settled=%v err=%v, want a settled fingerprint", base, settled, err)
	}
	if again, _, _ := probe.Procs(live); again != base {
		t.Fatalf("nothing moved but the fingerprint did: %q != %q", again, base)
	}

	proc.processes[200] = fakeProcess{stat: ProcStat{StartTime: 10}}
	if reused, _, _ := probe.Procs(live); reused == base {
		t.Fatal("a reused pid (new start time) must read as a different process")
	}
	proc.processes[200] = fakeProcess{stat: ProcStat{StartTime: 9}}

	delete(proc.processes, 200)
	gone, settled, err := probe.Procs(live)
	if err != nil || settled || gone == base {
		t.Fatalf("a pid already gone = %q settled=%v err=%v, want a changed, unsettled fingerprint", gone, settled, err)
	}
	proc.processes[200] = fakeProcess{stat: ProcStat{StartTime: 9}}

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	down, _, _ := probe.Procs(live)
	if down == base || !strings.Contains(down, "dial:cc-1800000000-1-1:down") {
		t.Fatalf("a socket that refuses connections must read as down: %q", down)
	}
}

// TestReadPaneCrumbRefusesACrumbItCannotTrust pins the parked poll's crumb read
// against what a shared crumb directory can hold after it was listed: a FIFO
// (an open that blocks would hang the refresh loop with the picker showing its
// last rows as current), a symlink, or a file far larger than a transcript
// path. Each is an error, never a hang and never a silent skip.
func TestReadPaneCrumbRefusesACrumbItCannotTrust(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"fifo": func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(target, []byte("/t/one.jsonl\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"oversized": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(strings.Repeat("x", 64*1024)), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			sidDir := t.TempDir()
			plant(t, filepath.Join(sidDir, "cc-1800000000-1-1.%3"))
			done := make(chan error, 1)
			go func() {
				_, err := readPaneCrumb(filepath.Join(sidDir, "cc-1800000000-1-1.%3"))
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("a %s crumb was read as a transcript path", name)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("reading a %s crumb blocked", name)
			}
		})
	}
}

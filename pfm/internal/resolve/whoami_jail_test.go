//go:build jail

package resolve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestJailedWhoamiEnvironmentAndAncestryPaths walks every rung of chat.sh's
// self_tmux/tmux_from_ancestry (chat.sh:78-96): $TMUX first, then the process
// chain's environments, then the tmux servers' own pane-pid tables, and a
// stated failure when no ancestor carries tmux at all.
func TestJailedWhoamiEnvironmentAndAncestryPaths(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newResolveJail(t)
	socket := "cc-1700000500-1-1"
	session := "whoami-session"
	row := jail.startPlain(t, socket, session)
	fields := strings.Split(row, "\t")
	if len(fields) != 2 {
		t.Fatalf("pane row = %q", row)
	}
	pane := fields[0]
	panePID, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(jail.tmuxDir, socket)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1) $TMUX plus the exported Claude session id: the ordinary Claude chat.
	identifier, err := NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{
			TMUX:            fmt.Sprintf("%s,%d,0", socketPath, panePID),
			TMUXPane:        pane,
			ClaudeSessionID: "11111111-2222-4333-8444-555555555555",
		},
		TmuxDir:   jail.tmuxDir,
		Processes: fakeProcTree{parents: map[int]int{9999: panePID, panePID: 1}}, SelfPID: 9999,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identifier.Identify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Session != session ||
		identity.SocketPath != socketPath ||
		identity.SocketName != socket ||
		identity.Pane != pane ||
		identity.Engine != string(pfmengine.Claude) ||
		identity.ID != "11111111-2222-4333-8444-555555555555" ||
		identity.Source != "env-claude" ||
		identity.Recovered {
		t.Fatalf("env identity = %+v", identity)
	}

	// 2) No $TMUX, but an ancestor's environment carries it — the codex path
	// that left every codex-origin message unsigned.
	codexSocket := "cx-1700000501-1-2"
	codexSession := "codex-session"
	codexRow := jail.startPlain(t, codexSocket, codexSession)
	codexFields := strings.Split(codexRow, "\t")
	codexPane := codexFields[0]
	codexPanePID, err := strconv.Atoi(codexFields[1])
	if err != nil {
		t.Fatal(err)
	}
	codexSocketPath := filepath.Join(jail.tmuxDir, codexSocket)
	tree := fakeProcTree{
		parents: map[int]int{4242: 4241, 4241: 4240, 4240: codexPanePID, codexPanePID: 1},
		environments: map[int]map[string]string{
			4242: {"PATH": "/usr/bin"},
			4241: {"PATH": "/usr/bin"},
			4240: {
				"TMUX":      codexSocketPath + ",99,0",
				"TMUX_PANE": codexPane,
			},
		},
	}
	identifier, err = NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{CodexThreadID: "thread-abc"},
		Processes:   tree,
		SelfPID:     4242,
		TmuxDir:     jail.tmuxDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err = identifier.Identify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Session != codexSession ||
		identity.SocketPath != codexSocketPath ||
		identity.Pane != codexPane ||
		identity.Engine != string(pfmengine.Codex) ||
		identity.ID != "thread-abc" ||
		identity.Source != "env-codex" ||
		!identity.Recovered {
		t.Fatalf("ancestry-environ identity = %+v", identity)
	}

	// 3) No $TMUX and no ancestor environment either: the tmux servers'
	// pane-pid tables still know which pane owns the chain.
	blind := fakeProcTree{
		parents:      map[int]int{7777: panePID, panePID: 1},
		environments: map[int]map[string]string{},
	}
	identifier, err = NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{},
		Processes:   blind,
		SelfPID:     7777,
		TmuxDir:     jail.tmuxDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err = identifier.Identify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Session != session ||
		identity.Pane != pane ||
		identity.Source != "ancestry" ||
		!identity.Recovered {
		t.Fatalf("ancestry-owner identity = %+v", identity)
	}

	// 4) Nothing carries tmux: the failure is STATED, in chat.sh's words.
	orphan := fakeProcTree{
		parents:      map[int]int{9001: 1},
		environments: map[int]map[string]string{9001: {"PATH": "/usr/bin"}},
	}
	identifier, err = NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{},
		Processes:   orphan,
		SelfPID:     9001,
		TmuxDir:     filepath.Join(jail.root, "empty-tmux-dir"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identifier.Identify(ctx); !errors.Is(err, ErrNotInChat) {
		t.Fatalf("orphan identity error = %v, want ErrNotInChat", err)
	}
}

func (jail *resolveJail) startPlain(
	t *testing.T,
	socket, session string,
) string {
	t.Helper()
	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"-n",
		"whoami-window",
		"sleep",
		"120",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start %s: %v: %s", socket, err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	output, err := jail.command(
		"-L",
		socket,
		"list-panes",
		"-F",
		"#{pane_id}\t#{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// kernelTableChildEnv marks the child TestWhoamiProcTreeReadsTheKernelTable
// starts from its own test binary: the variable its environment must show.
const kernelTableChildEnv = "PFM_TEST_KERNEL_TABLE_CHILD"

// The kernel's own table answers ancestry recovery's two facts for a live
// process on either OS: its environment and its parent. On macOS this read
// once returned "not implemented", failing every caller lookup there.
func TestWhoamiProcTreeReadsTheKernelTable(t *testing.T) {
	if os.Getenv(kernelTableChildEnv) != "" {
		// The kernel fills a new image's environment only after the exec that
		// wakes Start's caller, so the child says when it runs Go code.
		ready := os.NewFile(3, "ready")
		_, _ = ready.Write([]byte{1})
		_ = ready.Close()
		_, _ = io.Copy(io.Discard, os.Stdin) // held until the parent has read it
		return
	}
	marker := "held-by-" + strconv.Itoa(os.Getpid())
	child := exec.Command(os.Args[0], "-test.run=^TestWhoamiProcTreeReadsTheKernelTable$")
	child.Env = append(os.Environ(), kernelTableChildEnv+"="+marker)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readyRead.Close() }()
	child.ExtraFiles = []*os.File{readyWrite}
	startErr := child.Start()
	_ = readyWrite.Close()
	if startErr != nil {
		t.Fatalf("start the held child: %v", startErr)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	if _, err := readyRead.Read(make([]byte, 1)); err != nil {
		t.Fatalf("wait for the held child %d to run: %v", child.Process.Pid, err)
	}
	tree := ProcTree{Root: paths.SystemProcRoot()}
	environment, err := tree.Environ(child.Process.Pid)
	if err != nil || environment[kernelTableChildEnv] != marker {
		t.Fatalf("Environ(child %d) %s=%q, %v; want %q",
			child.Process.Pid, kernelTableChildEnv, environment[kernelTableChildEnv], err, marker)
	}
	if parent, err := tree.Parent(child.Process.Pid); err != nil || parent != os.Getpid() {
		t.Fatalf("Parent(child %d) = %d, %v; want this test %d", child.Process.Pid, parent, err, os.Getpid())
	}
}

package resolve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

// fakeProcTree is a jailed process chain: no test ever reads live /proc.
type fakeProcTree struct {
	parents      map[int]int
	environments map[int]map[string]string
}

// erroringNamer always fails the tmux exec — a socket that answered wrong,
// not one that never existed.
type erroringNamer struct{ err error }

func (namer erroringNamer) SessionName(context.Context, string, string) (string, error) {
	return "", namer.err
}

// L1-F5: a tmux exec failure on an already-resolved socket must surface as
// its own error, never as ErrNoTmux — the caller IS inside tmux, the exec
// just could not run.
func TestIdentifySurfacesATmuxExecFailureOnAResolvedSocket(t *testing.T) {
	sentinel := errors.New("tmux exec boom")
	identifier, err := NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{TMUX: "/tmp/tmux-1000/cc-1-2-3,42,0"},
		Namer:       erroringNamer{err: sentinel},
		TmuxDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = identifier.Identify(context.Background())
	if err == nil {
		t.Fatal("Identify() with a failing tmux exec returned no error")
	}
	if errors.Is(err, ErrNoTmux) {
		t.Fatalf("Identify() error = %v, want the exec failure, not ErrNoTmux", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Identify() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestEngineForUnknownSocketIsExplicit(t *testing.T) {
	if got := engineForSocket("unmanaged"); got != "unknown" {
		t.Fatalf("engineForSocket(unmanaged)=%q, want explicit unknown", got)
	}
}

type fakeParentProcFS struct {
	parent int
}

func (proc fakeParentProcFS) Environ(int) (map[string]string, error) {
	return map[string]string{}, nil
}

func (proc fakeParentProcFS) Stat(int) (ProcStat, error) {
	return ProcStat{ParentPID: proc.parent}, nil
}

func TestProcTreeParentUsesInjectedProcFS(t *testing.T) {
	tree := ProcTree{Root: filepath.Join(t.TempDir(), "does-not-exist"), ProcFS: fakeParentProcFS{parent: 41}}
	if got, err := tree.Parent(99); err != nil || got != 41 {
		t.Fatalf("Parent() = %d, %v; want 41, nil", got, err)
	}
}

func (tree fakeProcTree) Environ(pid int) (map[string]string, error) {
	environment, found := tree.environments[pid]
	if !found {
		return nil, errors.New("no environ")
	}
	return environment, nil
}

func (tree fakeProcTree) Parent(pid int) (int, error) {
	parent, found := tree.parents[pid]
	if !found {
		return 0, errors.New("no stat")
	}
	return parent, nil
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

// TestJailedWhoamiEnvironmentAndAncestryPaths walks every rung of chat.sh's
// self_tmux/tmux_from_ancestry (chat.sh:78-96): $TMUX first, then the process
// chain's environments, then the tmux servers' own pane-pid tables, and a
// stated failure when no ancestor carries tmux at all.
func TestJailedWhoamiEnvironmentAndAncestryPaths(t *testing.T) {
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
		TmuxDir: jail.tmuxDir,
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
	codexPane := strings.Split(codexRow, "\t")[0]
	codexSocketPath := filepath.Join(jail.tmuxDir, codexSocket)
	tree := fakeProcTree{
		parents: map[int]int{4242: 4241, 4241: 4240, 4240: 1},
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
	if _, err := identifier.Identify(ctx); !errors.Is(err, ErrNoTmux) {
		t.Fatalf("orphan identity error = %v, want ErrNoTmux", err)
	}
}

// TestWhoamiProcTreeReadsAJailedProcRoot proves the real /proc reader is
// jailable, so ancestry recovery is testable without touching live processes.
func TestWhoamiProcTreeReadsAJailedProcRoot(t *testing.T) {
	root := t.TempDir()
	pid := 4242
	directory := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	environ := strings.Join([]string{
		"PATH=/usr/bin",
		"TMUX=/tmp/tmux-1000/cc-1-2-3,42,0",
		"TMUX_PANE=%7",
	}, "\x00") + "\x00"
	if err := os.WriteFile(
		filepath.Join(directory, "environ"),
		[]byte(environ),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "stat"),
		[]byte("4242 (bash) S 4200 4242 4242 0 -1 4194304 100 0 0 0 1 2 3 4 20 0 1 0 999\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	tree := ProcTree{Root: root}
	environment, err := tree.Environ(pid)
	if err != nil {
		t.Fatal(err)
	}
	if environment["TMUX"] != "/tmp/tmux-1000/cc-1-2-3,42,0" ||
		environment["TMUX_PANE"] != "%7" {
		t.Fatalf("environ = %+v", environment)
	}
	parent, err := tree.Parent(pid)
	if err != nil || parent != 4200 {
		t.Fatalf("Parent() = %d, %v, want 4200", parent, err)
	}
	if socketPath := socketFromTMUX(environment["TMUX"]); socketPath !=
		"/tmp/tmux-1000/cc-1-2-3" {
		t.Fatalf("socketFromTMUX() = %q", socketPath)
	}
}

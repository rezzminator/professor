package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// fakeProcTree is a jailed process chain: no test ever reads live /proc.
type fakeProcTree struct {
	parents      map[int]int
	environments map[int]map[string]string
}

// erroringNamer always fails the tmux exec — a socket that answered wrong,
// not one that never existed.
type erroringNamer struct{ err error }

func (namer erroringNamer) SessionPane(context.Context, string, string) (string, int, error) {
	return "", 0, namer.err
}

// L1-F5: a tmux exec failure on an already-resolved socket must surface as
// its own error, never as ErrNotInChat — the caller IS inside tmux, the exec
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
	if errors.Is(err, ErrNotInChat) {
		t.Fatalf("Identify() error = %v, want the exec failure, not ErrNotInChat", err)
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
		return nil, os.ErrNotExist
	}
	return environment, nil
}

func (tree fakeProcTree) Parent(pid int) (int, error) {
	parent, found := tree.parents[pid]
	if !found {
		return 0, os.ErrNotExist
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
	if socketPath := pfmtmux.SocketPath(environment["TMUX"]); socketPath !=
		"/tmp/tmux-1000/cc-1-2-3" {
		t.Fatalf("pfmtmux.SocketPath() = %q", socketPath)
	}
}

type fixedPaneNamer struct{ pid int }

func (namer fixedPaneNamer) SessionPane(context.Context, string, string) (string, int, error) {
	return "cc-1", namer.pid, nil
}

func TestIdentifySelectsNearestProvenContainer(t *testing.T) {
	for _, tc := range []struct {
		name              string
		tmuxPID, herdrPID int
		backend           string
		ambiguous         bool
	}{
		{"tmux-inside-herdr", 4200, 5000, "tmux", false},
		{"herdr-inside-tmux", 4000, 4200, "herdr", false},
		{"equal-claims", 4200, 4200, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
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
						if json.NewDecoder(conn).Decode(&req) != nil {
							return
						}
						var body map[string]any
						if req.Method == "pane.get" {
							body = map[string]any{
								"type": "pane_info",
								"pane": map[string]any{"pane_id": "wC:p24", "workspace_id": "wC", "terminal_id": "t9"},
							}
						} else {
							body = map[string]any{
								"type": "pane_process_info",
								"process_info": map[string]any{
									"pane_id":              "wC:p24",
									"foreground_processes": []any{map[string]any{"pid": tc.herdrPID}},
								},
							}
						}
						_ = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "ok": true, "result": body})
					}()
				}
			}()
			identifier, err := NewWhoami(
				WhoamiDependencies{
					Environment: &WhoamiEnvironment{
						TMUX:        "/tmp/tmux-1000/cc-1,1,0",
						TMUXPane:    "%3",
						HerdrSocket: socket,
						HerdrPane:   "wC:p24",
					},
					Namer:     fixedPaneNamer{pid: tc.tmuxPID},
					SelfPID:   4300,
					Processes: fakeProcTree{parents: map[int]int{4300: 4200, 4200: 4100, 4100: 4000, 4000: 1}},
					TmuxDir:   t.TempDir(),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			got, err := identifier.Identify(context.Background())
			if tc.ambiguous {
				if !errors.Is(err, ErrAmbiguous) {
					t.Fatalf("identity=%+v err=%v", got, err)
				}
				return
			}
			if got.Backend != tc.backend || tc.backend == "tmux" && err != nil ||
				tc.backend == "herdr" && !errors.Is(err, ErrNotRegistered) {
				t.Fatalf("identity=%+v err=%v", got, err)
			}
		})
	}
}

type unreadableAncestry struct{}

func (unreadableAncestry) Parent(int) (int, error)                { return 0, os.ErrPermission }
func (unreadableAncestry) Environ(int) (map[string]string, error) { return nil, os.ErrPermission }

func TestIdentifyDoesNotHideUnreadableAncestry(t *testing.T) {
	identifier, err := NewWhoami(
		WhoamiDependencies{
			Environment: &WhoamiEnvironment{},
			Processes:   unreadableAncestry{},
			SelfPID:     4300,
			TmuxDir:     t.TempDir(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = identifier.Identify(context.Background())
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrNotInChat) {
		t.Fatalf("unreadable ancestry became absence: %v", err)
	}
}

func TestIdentifyMissingTmuxIsFailedRead(t *testing.T) {
	identifier, err := NewWhoami(
		WhoamiDependencies{
			Environment: &WhoamiEnvironment{TMUX: "/tmp/missing,1,0", TMUXPane: "%3"},
			Namer:       CommandTmuxNamer{Binary: filepath.Join(t.TempDir(), "missing-tmux")},
			Processes:   fakeProcTree{parents: map[int]int{4300: 1}},
			SelfPID:     4300,
			TmuxDir:     t.TempDir(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = identifier.Identify(context.Background())
	if !pfmtmux.CouldNotRun(err) || errors.Is(err, ErrNotInChat) || errors.Is(err, ErrNotRegistered) ||
		errors.Is(err, ErrAmbiguous) {
		t.Fatalf("missing binary became absence: %v", err)
	}
}

func TestIdentifyDistinguishesMissingTmuxServerFromReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name, stderr string
		gone         bool
	}{
		{"permission denied", "error connecting to /tmp/cc-probe (Permission denied)", false},
		{"unknown connection failure", "error connecting to /tmp/cc-probe (Resource temporarily unavailable)", false},
		{"missing socket", "error connecting to /tmp/cc-probe (No such file or directory)", true},
		{"refused connection", "error connecting to /tmp/cc-probe (Connection refused)", true},
		{"no server", "no server running on /tmp/cc-probe", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := &exec.ExitError{Stderr: []byte(tc.stderr)}
			identifier, err := NewWhoami(WhoamiDependencies{
				Environment: &WhoamiEnvironment{TMUX: "/tmp/cc-probe,1,0", TMUXPane: "%3"},
				Namer:       erroringNamer{err: failure},
				Processes:   fakeProcTree{parents: map[int]int{4300: 1}},
				SelfPID:     4300, TmuxDir: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = identifier.Identify(context.Background())
			if errors.Is(err, ErrNotInChat) != tc.gone || !tc.gone && !errors.Is(err, failure) {
				t.Fatalf(
					"Identify with %q returned %v; want gone=%t and read failures preserved",
					tc.stderr,
					err,
					tc.gone,
				)
			}
		})
	}
}

type environmentDeniedTree struct{ fakeProcTree }

func (environmentDeniedTree) Environ(int) (map[string]string, error) {
	return nil, os.ErrPermission
}

func TestIdentifyPreservesAncestryEnvironmentReadFailure(t *testing.T) {
	identifier, err := NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{},
		Processes:   environmentDeniedTree{fakeProcTree{parents: map[int]int{4300: 1}}},
		SelfPID:     4300, TmuxDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = identifier.Identify(context.Background())
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrNotInChat) {
		t.Fatalf("unreadable environment became absence: %v", err)
	}
}

type environmentForeignTree struct{ fakeProcTree }

func (environmentForeignTree) Environ(int) (map[string]string, error) {
	return nil, fmt.Errorf("%w: %w", ErrForeignProcess, os.ErrPermission)
}

// An ssh login's ancestry crosses sshd's non-dumpable session; that ancestor is no pane of the caller's.
func TestIdentifyPassesOverForeignAncestor(t *testing.T) {
	identifier, err := NewWhoami(WhoamiDependencies{
		Environment: &WhoamiEnvironment{},
		Processes:   environmentForeignTree{fakeProcTree{parents: map[int]int{4300: 1}}},
		SelfPID:     4300, TmuxDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = identifier.Identify(context.Background())
	if !errors.Is(err, ErrNotInChat) {
		t.Fatalf("a foreign ancestor blocked identification: %v", err)
	}
}

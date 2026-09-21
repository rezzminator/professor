package reap

import (
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// fakeProc is a process table with parents, argv and resident memory — the
// three facts a sweep reads. It implements gather.ProcMemory too, so the RSS
// path is exercised without a /proc.
type fakeProc struct {
	parents map[int]int
	cmdline map[int][]string
	rss     map[int]int64
}

func (proc fakeProc) PIDs() ([]int, error) {
	pids := make([]int, 0, len(proc.parents))
	for pid := range proc.parents {
		pids = append(pids, pid)
	}
	return pids, nil
}

func (proc fakeProc) Cmdline(pid int) ([]string, error) {
	cmdline, found := proc.cmdline[pid]
	if !found {
		return nil, errors.New("no such process")
	}
	return cmdline, nil
}

func (proc fakeProc) Environ(int) (map[string]string, error) { return nil, nil }
func (proc fakeProc) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }

func (proc fakeProc) Stat(pid int) (gather.ProcStat, error) {
	parent, found := proc.parents[pid]
	if !found {
		return gather.ProcStat{}, errors.New("no such process")
	}
	return gather.ProcStat{ParentPID: parent}, nil
}

func (proc fakeProc) RSSKB(pid int) (int64, error) {
	return proc.rss[pid], nil
}

// The fixture is the shape this machine actually runs: a chat pane whose
// leader is claude and whose children include an MCP server, and a shell pane
// carrying a project's dev servers.
func newFleetProc() fakeProc {
	return fakeProc{
		parents: map[int]int{
			// chat pane: claude leads, an MCP server and a statusline shell
			// hang off it.
			100: 1, 101: 100, 102: 100,
			// dev pane: a shell leads, pnpm and its node child run under it.
			200: 1, 201: 200, 202: 201,
			// bare shell pane: nothing running in it at all.
			300: 1,
			// dev-server pane: the LEADER itself is the server.
			400: 1, 401: 400,
		},
		cmdline: map[int][]string{
			100: {"claude", "--resume", "abc"},
			101: {"uv", "--directory", "/home/x/harvester", "run", "harvester"},
			102: {"/bin/sh", "-c", "statusline"},
			200: {"zsh"},
			201: {"pnpm", "dev"},
			202: {"node", "vite"},
			300: {"bash"},
			400: {"node", "server.js"},
			401: {"esbuild"},
		},
		rss: map[int]int64{
			100: 500_000, 101: 40_000, 102: 1_000,
			200: 5_000, 201: 30_000, 202: 120_000,
			300: 5_000,
			400: 90_000, 401: 10_000,
		},
	}
}

func TestForeignProcessesExemptsAChatsOwnSubtree(t *testing.T) {
	tree, err := NewProcessTree(newFleetProc())
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	if foreign := tree.ForeignProcesses([]int{100}); len(foreign) != 0 {
		t.Fatalf(
			"a chat's own MCP server and statusline read as foreign: %v",
			foreign,
		)
	}
}

func TestForeignProcessesExemptsOpenCodeOwnSubtree(t *testing.T) {
	tree, err := NewProcessTree(fakeProc{
		parents: map[int]int{500: 1, 501: 500},
		cmdline: map[int][]string{
			500: {"opencode", "--session", "fixture"},
			501: {"node", "mcp-server"},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	if foreign := tree.ForeignProcesses([]int{500}); len(foreign) != 0 {
		t.Fatalf("an OpenCode chat's own child processes read as foreign: %v", foreign)
	}
}

func TestForeignProcessesUsesConfiguredOpenCodeBinary(t *testing.T) {
	tree, err := NewProcessTree(fakeProc{
		parents: map[int]int{600: 1, 601: 600},
		cmdline: map[int][]string{
			600: {"ocx", "--session", "fixture"},
			601: {"node", "mcp-server"},
		},
	}, "claude", "codex", "/custom/ocx")
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	if foreign := tree.ForeignProcesses([]int{600}); len(foreign) != 0 {
		t.Fatalf("configured OpenCode binary was treated as foreign: %v", foreign)
	}
}

func TestForeignProcessesFindsDevServersUnderAShell(t *testing.T) {
	tree, err := NewProcessTree(newFleetProc())
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	foreign := tree.ForeignProcesses([]int{200})
	if len(foreign) != 1 || foreign[0] != "pnpm" {
		t.Fatalf("ForeignProcesses() = %v, want [pnpm]", foreign)
	}
}

// The pane LEADER counts too. One socket on this machine runs a project's
// backend, cortex and frontend as the pane commands themselves; judging only
// descendants would have declared it reapable.
func TestForeignProcessesJudgesThePaneLeader(t *testing.T) {
	tree, err := NewProcessTree(newFleetProc())
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	foreign := tree.ForeignProcesses([]int{400})
	if len(foreign) != 1 || foreign[0] != "node" {
		t.Fatalf("ForeignProcesses() = %v, want [node]", foreign)
	}
}

func TestForeignProcessesLeavesAnEmptyShellReapable(t *testing.T) {
	tree, err := NewProcessTree(newFleetProc())
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	if foreign := tree.ForeignProcesses([]int{300}); len(foreign) != 0 {
		t.Fatalf("an idle shell pane reads as hosting %v", foreign)
	}
}

func TestSubtreeRSSSumsTheWholeTree(t *testing.T) {
	tree, err := NewProcessTree(newFleetProc())
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	if got, want := tree.SubtreeRSSKB(100), int64(541_000); got != want {
		t.Fatalf("SubtreeRSSKB(chat) = %d, want %d", got, want)
	}
	if got, want := tree.SubtreeRSSKB(200), int64(155_000); got != want {
		t.Fatalf("SubtreeRSSKB(dev shell) = %d, want %d", got, want)
	}
}

// A ProcFS with no memory extension leaves the RAM column at zero rather than
// failing the sweep: the classification never depends on it.
func TestSubtreeRSSWithoutAMemorySourceReportsZero(t *testing.T) {
	tree, err := NewProcessTree(memorylessProc{inner: newFleetProc()})
	if err != nil {
		t.Fatalf("NewProcessTree() error = %v", err)
	}
	if got := tree.SubtreeRSSKB(100); got != 0 {
		t.Fatalf("SubtreeRSSKB() = %d, want 0", got)
	}
	if foreign := tree.ForeignProcesses([]int{200}); len(foreign) != 1 {
		t.Fatalf("classification degraded with no memory source: %v", foreign)
	}
}

// memorylessProc exposes only gather.ProcFS — no RSSKB — so the sweep runs
// against a process source that cannot report memory at all.
type memorylessProc struct{ inner fakeProc }

func (proc memorylessProc) PIDs() ([]int, error) { return proc.inner.PIDs() }

func (proc memorylessProc) Cmdline(pid int) ([]string, error) {
	return proc.inner.Cmdline(pid)
}

func (proc memorylessProc) Environ(pid int) (map[string]string, error) {
	return proc.inner.Environ(pid)
}

func (proc memorylessProc) FDLinks(pid int) ([]gather.FDLink, error) {
	return proc.inner.FDLinks(pid)
}

func (proc memorylessProc) Stat(pid int) (gather.ProcStat, error) {
	return proc.inner.Stat(pid)
}

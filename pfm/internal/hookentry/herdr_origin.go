package hookentry

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// tmuxOrigin settles the tmux server and pane this run reports through. A
// tmux-fired verb names its socket. An engine-fired event reads $TMUX
// ("socket,server pid,session") and $TMUX_PANE, which Codex's shell snapshot
// can carry from a server long gone, so they are believed only while the
// server pid $TMUX names is a tmux ancestor of the hook. A tmux process met first
// on the walk up makes them stale: the pane process below it carries the true
// pair in its own environment, set by tmux when it started the pane. A walk
// that ends with no tmux above the hook runs outside tmux, and the process
// environment's Herdr pane is the target. A process table that cannot be read
// keeps $TMUX, and the ownership walk (paneEngineFired) names the table.
func (reporter *herdrReporter) tmuxOrigin() error {
	reporter.tmuxPane = reporter.deps.getenv("TMUX_PANE")
	if reporter.options.socket != "" {
		return nil
	}
	socket, rest, _ := strings.Cut(reporter.deps.getenv("TMUX"), ",")
	reporter.options.socket = socket
	if socket == "" || !reporter.firedByEngine() || reporter.deps.procInfo == nil || reporter.deps.getpid == nil {
		return nil
	}
	serverText, _, _ := strings.Cut(rest, ",")
	// A $TMUX with no usable server pid matches no ancestor, pid 0 included.
	server, err := strconv.Atoi(serverText)
	serverKnown := err == nil && server > 0
	child, pid := 0, reporter.deps.getpid()
	for depth := 0; depth < herdrMaxProcessDepth; depth++ {
		parent, command, err := reporter.deps.procInfo(pid)
		if err != nil {
			return nil
		}
		if child != 0 && isTmuxCommand(command) {
			return reporter.paneOrigin(child)
		}
		if serverKnown && parent == server && reporter.isTmuxServer(server) {
			return nil
		}
		if parent <= 1 || parent == pid {
			reporter.options.socket, reporter.tmuxPane = "", ""
			return nil
		}
		child, pid = pid, parent
	}
	return nil
}

// isTmuxServer reports whether the ancestor at the server pid $TMUX names is
// a tmux: a dead server's pid can be reused by a process that is not one. A
// pid the table cannot read keeps $TMUX, as an unreadable walk does.
func (reporter *herdrReporter) isTmuxServer(server int) bool {
	_, command, err := reporter.deps.procInfo(server)
	return err != nil || isTmuxCommand(command)
}

// paneOrigin reads the socket and pane of the tmux pane process pane from its
// own environment: $TMUX names a server the hook does not run under.
func (reporter *herdrReporter) paneOrigin(pane int) error {
	stale := reporter.options.socket
	if reporter.deps.environ == nil {
		return fmt.Errorf("$TMUX names %s, a server the hook does not run under, and no process table reads "+
			"the environment of tmux pane process %d (skipped)", stale, pane)
	}
	environment, err := reporter.deps.environ(pane)
	if err != nil {
		return fmt.Errorf("$TMUX names %s, a server the hook does not run under; read the environment of tmux "+
			"pane process %d (skipped): %w", stale, pane, err)
	}
	socket, _, _ := strings.Cut(environment["TMUX"], ",")
	if socket == "" || environment["TMUX_PANE"] == "" {
		return fmt.Errorf("$TMUX names %s, a server the hook does not run under, and tmux pane process %d "+
			"carries no TMUX and TMUX_PANE (skipped)", stale, pane)
	}
	reporter.options.socket, reporter.tmuxPane = socket, environment["TMUX_PANE"]
	return nil
}

// isTmuxCommand reports whether a process command is a tmux server: its
// binary name, or the "tmux: server" title tmux gives itself on Linux.
func isTmuxCommand(command string) bool {
	return filepath.Base(command) == "tmux" || strings.HasPrefix(command, "tmux:")
}

// threadWithoutRollout reports whether a Codex payload comes from a thread
// that keeps no rollout file, its "transcript_path" JSON null: the memory
// consolidation thread Codex runs inside the chat's own process. The chat's
// own thread names its rollout in every event; an absent key is not null.
func threadWithoutRollout(engine pfmengine.ID, raw []byte) bool {
	if engine != pfmengine.Codex {
		return false
	}
	var probe struct {
		TranscriptPath json.RawMessage `json:"transcript_path"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return string(probe.TranscriptPath) == "null"
}

// realProcInfo reads a process's parent and command name through the ProcFS
// (the kernel's table on Linux, sysctl on macOS, a fixture root in a jail).
func realProcInfo(procfs gather.ProcFS) func(int) (int, string, error) {
	return func(pid int) (int, string, error) {
		stat, err := procfs.Stat(pid)
		if err != nil {
			return 0, "", fmt.Errorf("read parent of pid %d: %w", pid, err)
		}
		if identity, ok := procfs.(gather.ProcIdentity); ok {
			process, err := identity.ProcessIdentity(pid)
			if err != nil {
				return 0, "", fmt.Errorf("read command of pid %d: %w", pid, err)
			}
			return stat.ParentPID, process.Command, nil
		}
		argv, err := procfs.Cmdline(pid)
		if err != nil || len(argv) == 0 {
			return 0, "", fmt.Errorf("read command of pid %d: %w", pid, errors.Join(err, errors.New("no argv")))
		}
		return stat.ParentPID, argv[0], nil
	}
}

package hookentry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// tmuxOrigin settles the tmux server and pane this run reports through. A
// tmux-fired verb names its socket; an engine-fired event reads $TMUX
// ("socket,server pid,session") and $TMUX_PANE. Codex never comes here: its
// hooks can run in the shared daemon's environment (runCodex).
func (reporter *herdrReporter) tmuxOrigin() {
	reporter.tmuxPane = reporter.deps.getenv("TMUX_PANE")
	if reporter.options.socket == "" {
		reporter.options.socket, _, _ = strings.Cut(reporter.deps.getenv("TMUX"), ",")
	}
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

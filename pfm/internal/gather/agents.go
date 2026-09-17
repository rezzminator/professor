package gather

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
)

// DetectAgents returns strict session identities for Claude processes using a
// non-primary config directory.
func DetectAgents(proc ProcFS, home string, panes []Pane, binaries ...string) ([]Agent, error) {
	cmdlines, err := processCmdlines(proc)
	if err != nil {
		return nil, fmt.Errorf("list processes for agent scan: %w", err)
	}
	return detectAgentsFrom(cmdlines, proc, home, panes, binaries...)
}

// detectAgentsFrom is DetectAgents over an already-fetched pid->cmdline
// snapshot — see processCmdlines.
func detectAgentsFrom(
	cmdlines map[int][]string,
	proc ProcFS,
	home string,
	panes []Pane,
	binaries ...string,
) ([]Agent, error) {
	pids := sortedPIDs(cmdlines)
	paneByPID := panesByPID(panes)
	primaryRoot := filepath.Clean(filepath.Join(home, ".claude"))
	seenSessions := make(map[string]struct{})
	agents := make([]Agent, 0)

	for _, pid := range pids {
		cmdline := cmdlines[pid]
		if !isClaudeCommand(cmdline, binaries...) {
			continue
		}
		sessionIDs := claudeSessionIDs(cmdline)
		if len(sessionIDs) == 0 {
			continue
		}
		environment, err := proc.Environ(pid)
		if err != nil {
			continue
		}
		configDir := environment["CLAUDE_CONFIG_DIR"]
		if configDir == "" {
			configDir = primaryRoot
		}
		if filepath.Clean(configDir) == primaryRoot {
			continue
		}

		stat, _ := proc.Stat(pid)
		pane, paneFound := paneForProcess(proc, pid, paneByPID)
		for _, sessionID := range sessionIDs {
			if _, duplicate := seenSessions[sessionID]; duplicate {
				continue
			}
			seenSessions[sessionID] = struct{}{}
			agent := Agent{
				PID:       pid,
				SessionID: sessionID,
				ConfigDir: configDir,
				StartTime: stat.StartTime,
			}
			if paneFound {
				agent.PanePID = pane.PID
				agent.Socket = pane.Socket
				agent.PaneID = pane.PaneID
			}
			agents = append(agents, agent)
		}
	}
	sort.Slice(agents, func(left, right int) bool {
		return agents[left].SessionID < agents[right].SessionID
	})
	return agents, nil
}

// IsClaudeCommand reports whether an argv belongs to a Claude Code process.
// The reaper asks the same question the agent scan does — a process tree
// hosting a chat is not a process tree hosting somebody's dev server — so
// there is one spelling of it (K3).
func IsClaudeCommand(cmdline []string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.Claude, cmdline, true, binaries...)
}

// IsCodexCommand reports whether an argv belongs to a Codex process.
func IsCodexCommand(cmdline []string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.Codex, cmdline, false, binaries...)
}

func isClaudeCommand(cmdline []string, binaries ...string) bool {
	return IsClaudeCommand(cmdline, binaries...)
}

func claudeSessionIDs(cmdline []string) []string {
	seen := make(map[string]struct{})
	sessionIDs := make([]string, 0, 2)
	for index := 1; index < len(cmdline); index++ {
		argument := cmdline[index]
		var value string
		switch {
		case argument == "--session-id" || argument == "--resume":
			if index+1 >= len(cmdline) {
				continue
			}
			index++
			value = cmdline[index]
		case strings.HasPrefix(argument, "--session-id="):
			value = strings.TrimPrefix(argument, "--session-id=")
		case strings.HasPrefix(argument, "--resume="):
			value = strings.TrimPrefix(argument, "--resume=")
		default:
			continue
		}
		value = strings.TrimSuffix(filepath.Base(value), filepath.Ext(value))
		if !isUUID(value) {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		sessionIDs = append(sessionIDs, value)
	}
	return sessionIDs
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		default:
			if (character < '0' || character > '9') &&
				(character < 'a' || character > 'f') &&
				(character < 'A' || character > 'F') {
				return false
			}
		}
	}
	return true
}

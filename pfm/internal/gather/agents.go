package gather

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// DetectAgents returns strict session identities for Claude processes using a
// non-primary config directory, plus every Claude daemon job. The warnings
// name each daemon-job record that exists but could not be read — a job the
// scan failed to see, never one it proved absent.
func DetectAgents(proc ProcFS, home string, panes []ProbePane, binaries ...string) ([]Agent, []string, error) {
	cmdlines, err := processCmdlines(proc)
	if err != nil {
		return nil, nil, fmt.Errorf("list processes for agent scan: %w", err)
	}
	return detectAgentsFrom(cmdlines, proc, home, panes, binaries...)
}

// detectAgentsFrom is DetectAgents over an already-fetched pid->cmdline
// snapshot — see processCmdlines.
func detectAgentsFrom(
	cmdlines map[int][]string,
	proc ProcFS,
	home string,
	panes []ProbePane, binaries ...string,
) ([]Agent, []string, error) {
	pids := sortedPIDs(cmdlines)
	paneByPID := panesByPID(panes)
	primaryRoot := filepath.Clean(filepath.Join(home, ".claude"))
	seenSessions := make(map[string]struct{})
	agents := make([]Agent, 0)
	var warnings []string

	for _, pid := range pids {
		cmdline := cmdlines[pid]
		if !IsClaudeCommand(cmdline, binaries...) {
			continue
		}
		sessionIDs := claudeSessionIDs(cmdline)
		environment, err := proc.Environ(pid)
		if err != nil {
			continue
		}
		configDir := environment["CLAUDE_CONFIG_DIR"]
		if configDir == "" {
			configDir = primaryRoot
		}
		stat, _ := proc.Stat(pid)
		if len(sessionIDs) == 0 {
			// A Claude daemon job runs in a pre-started spare whose argv names
			// no session; the session it claimed lives only in Claude's own
			// per-process record. It is an agent on every config dir, the
			// primary one included, because no pane crumb ever names it.
			sessionID, found, warning := claudeBackgroundJobSession(configDir, pid, stat.StartTime)
			if warning != "" {
				warnings = append(warnings, warning)
			}
			if !found {
				continue
			}
			sessionIDs = []string{sessionID}
		} else if filepath.Clean(configDir) == primaryRoot {
			continue
		}

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
	return agents, warnings, nil
}

// claudeProcessRecord is the part of Claude Code's per-process record,
// <config>/sessions/<pid>.json, that names a daemon job's session.
type claudeProcessRecord struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	ProcStart string `json:"procStart"`
}

// claudeBackgroundJobSession reads the session a Claude daemon job process
// is running. The record must be this very process — its pid, and its start
// time when both sides know one, so a record left behind by an earlier
// process under a recycled pid is never believed — and must say "bg": an
// interactive record's session is already carried by its pane's crumb.
// A missing record is no job — most Claude processes are not daemon jobs —
// but a record that exists and cannot be read is a failure to look, and
// comes back as a warning naming it.
func claudeBackgroundJobSession(configDir string, pid int, startTime uint64) (string, bool, string) {
	path := filepath.Join(configDir, "sessions", strconv.Itoa(pid)+".json")
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, ""
	}
	if err != nil {
		return "", false, fmt.Sprintf(
			"claude process record %s unreadable; a daemon job there may be missing: %v", path, err,
		)
	}
	var record claudeProcessRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return "", false, fmt.Sprintf(
			"claude process record %s malformed; a daemon job there may be missing: %v", path, err,
		)
	}
	if record.PID != pid || record.Kind != "bg" || !pfmengine.IsUUID(record.SessionID) {
		return "", false, ""
	}
	if record.ProcStart != "" && startTime != 0 &&
		record.ProcStart != strconv.FormatUint(startTime, 10) {
		return "", false, ""
	}
	return record.SessionID, true, ""
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
		if !pfmengine.IsUUID(value) {
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

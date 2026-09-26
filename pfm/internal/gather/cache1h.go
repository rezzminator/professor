package gather

import (
	"fmt"
	"sort"
)

// DetectCache1H returns the sockets whose Claude chat runs the ⚡1h cache.
//
// The rule is an opt-OUT, not an opt-in:
// since Claude Code 2.1.215 the 1h window is the harness DEFAULT, so a chat
// reads as 5m only when it was explicitly born with FORCE_PROMPT_CACHING_5M=1.
// Env binds at birth, so the live process environment is the only truth — an
// ENABLE_PROMPT_CACHING_1H=1 birth and a flagless elder both run 1h.
//
// An unreadable environment also reads as 1h: macOS lets no process read
// another's environment, and "unreadable" and "flagless" mean the same thing
// under a 1h default. The badge is then wrong only for a chat deliberately born
// 5m, and it errs by NOT promising a cheaper window than the chat actually has.
func DetectCache1H(proc ProcFS, panes []ProbePane, binaries ...string) ([]string, error) {
	cmdlines, err := processCmdlines(proc)
	if err != nil {
		return nil, fmt.Errorf("list processes for cache scan: %w", err)
	}
	return detectCache1HFrom(cmdlines, proc, panes, binaries...)
}

// detectCache1HFrom is DetectCache1H over an already-fetched pid->cmdline
// snapshot — see processCmdlines.
func detectCache1HFrom(
	cmdlines map[int][]string,
	proc ProcFS,
	panes []ProbePane,
	binaries ...string,
) ([]string, error) {
	pids := sortedPIDs(cmdlines)
	paneByPID := panesByPID(panes)
	sockets := make(map[string]bool)

	for _, pid := range pids {
		if !IsClaudeCommand(cmdlines[pid], binaries...) {
			continue
		}
		pane, found := paneForProcess(proc, pid, paneByPID)
		if !found {
			continue
		}
		environment, err := proc.Environ(pid)
		forced5M := err == nil &&
			environment["FORCE_PROMPT_CACHING_5M"] == "1"
		// One forced-5m process settles the socket: a shared socket is 5m as
		// soon as any Claude on it was born that way, and a later 1h sibling
		// must not talk the badge back on.
		if forced5M {
			sockets[pane.Socket] = false
		} else if _, seen := sockets[pane.Socket]; !seen {
			sockets[pane.Socket] = true
		}
	}

	result := make([]string, 0, len(sockets))
	for socket, cache1H := range sockets {
		if cache1H {
			result = append(result, socket)
		}
	}
	sort.Strings(result)
	return result, nil
}

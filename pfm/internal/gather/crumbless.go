package gather

import (
	"sort"

	pfmengine "hostops/pfm/internal/engine"
)

// DetectCrumblessLive finds every live Claude pane on a valid cc-* socket
// (cc-<epoch>-<pid>-<rand> or cc-new-*, validCrumbSocket's own grammar) whose
// socket carries no crumb: a chat still sitting at a startup prompt (folder
// trust, MCP approval) has a live process — DetectClaudeProcesses already
// walks the pane's process tree to find it — but the statusline writes the
// SID crumb only once boot finishes, so the crumb-driven picker is blind to
// it until then.
//
// Dedup is by SOCKET, never by pane: a crumb resolving ANYWHERE on the socket
// means the ordinary live row already covers it, so no pane there gets a
// crumbless entry even when some OTHER pane on the same socket has no crumb
// of its own. This mirrors compose's own socket-level trust in
// liveClaudeRows — the two must agree on when a socket is "covered" or a
// booting row and a live row would double up.
func DetectCrumblessLive(
	proc ProcFS,
	claudeProcesses []ClaudeProcess,
	crumbs []Crumb,
	panes []ProbePane,
) []CrumblessLive {
	crumbedSockets := make(map[string]struct{}, len(crumbs))
	for _, crumb := range crumbs {
		crumbedSockets[crumb.Socket] = struct{}{}
	}
	paneByTarget := make(map[string]ProbePane, len(panes))
	for index := range panes {
		pane := panes[index]
		paneByTarget[pane.Socket+"\x00"+pane.PaneID] = pane
	}

	seenSockets := make(map[string]struct{})
	entries := make([]CrumblessLive, 0)
	for _, process := range claudeProcesses {
		// validCrumbSocket alone also accepts a 4-part cx-<epoch>-<pid>-<rand>
		// shape (it is the crumb-filename grammar, shared by both engines); the
		// prefix test narrows this to the Claude sockets the bug is about.
		id, known := pfmengine.FromSocket(process.Socket)
		if !known || id != pfmengine.Claude ||
			!validCrumbSocket(process.Socket) {
			continue
		}
		if _, crumbed := crumbedSockets[process.Socket]; crumbed {
			continue
		}
		if _, already := seenSockets[process.Socket]; already {
			continue
		}
		pane, found := paneByTarget[process.Socket+"\x00"+process.PaneID]
		if !found {
			continue
		}
		seenSockets[process.Socket] = struct{}{}
		entries = append(entries, CrumblessLive{
			Socket:        process.Socket,
			SessionName:   pane.SessionName,
			WindowID:      pane.WindowID,
			WindowName:    pane.WindowName,
			PaneID:        pane.PaneID,
			PID:           process.PID,
			CWD:           pane.CurrentPath,
			PaneStartUnix: processBirth(proc, pane.PID),
		})
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Socket < entries[right].Socket
	})
	return entries
}

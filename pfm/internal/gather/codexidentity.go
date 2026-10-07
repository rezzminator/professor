package gather

import (
	"context"
	"strings"
	"sync"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// CodexIdentity is one live codex pane's own observed thread identity, read
// off the TUI's bottom status line — or the record that its capture could
// not be read. Failed is not "no identity": a capture that failed says
// nothing about the pane, and a kill decision must never read it as "this
// pane runs nothing".
type CodexIdentity struct {
	Socket   string
	PaneID   string
	Name     string
	ThreadID string
	Failed   bool
}

// CaptureCodexIdentity reads every live codex pane's own status line — the
// same bounded fan-out CaptureClaudeLabels runs for the claude 🔖 label, over
// a different parse. Codex's TUI renders the thread's NAME, or the bare
// thread id when the thread is unnamed, as the first `·`-separated field of
// its last status line. Named /clear and auto-titled successors may expose
// only a name; the caller must resolve it rather than assume a bare-id frame.
func CaptureCodexIdentity(
	ctx context.Context,
	capturer PaneCapturer,
	panes []ProbePane,
) []CodexIdentity {
	if capturer == nil {
		return nil
	}
	candidates := make([]ProbePane, 0, len(panes))
	for index := range panes {
		pane := panes[index]
		if id, ok := pfmengine.FromSocket(pane.Socket); !ok || id != pfmengine.Codex {
			continue
		}
		if pane.SessionName != pane.Socket || pane.WindowID == "" {
			continue
		}
		if pane.CurrentCommand == "tmux" {
			continue
		}
		candidates = append(candidates, pane)
	}
	if len(candidates) == 0 {
		return nil
	}

	identities := make([]CodexIdentity, len(candidates))
	var waitGroup sync.WaitGroup
	slots := make(chan struct{}, labelCaptureLimit)
	for index := range candidates {
		pane := candidates[index]
		index := index
		waitGroup.Add(1)
		slots <- struct{}{}
		go func() {
			defer waitGroup.Done()
			defer func() { <-slots }()
			result := CodexIdentity{Socket: pane.Socket, PaneID: pane.PaneID}
			capture, err := capturer.CapturePane(ctx, pane.Socket, pane.PaneID)
			if err != nil {
				result.Failed = true
			} else {
				result.Name, result.ThreadID = parseCodexIdentity(capture)
			}
			identities[index] = result
		}()
	}
	waitGroup.Wait()
	return identities
}

// parseCodexIdentity reads the bottommost status row, whose first two fields
// are the thread identity and directory. Shortcut and agent footers may follow
// it. Stop at any other nonstatus row: modals can hide today's status while
// an older status-shaped transcript remains above them. A status needs a
// metadata field beyond identity and directory, not a bare transcript pair.
func parseCodexIdentity(capture string) (name, threadID string) {
	lines := strings.Split(capture, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "? for shortcuts") ||
			strings.HasPrefix(line, "← for agents") || strings.HasPrefix(line, "→ for agents") {
			continue
		}
		fields := strings.Split(line, "·")
		if len(fields) < 3 || strings.TrimSpace(fields[2]) == "" {
			return "", ""
		}
		directory := strings.TrimSpace(fields[1])
		windowsAbsolute := len(directory) > 2 && directory[1] == ':' && (directory[2] == '\\' || directory[2] == '/')
		if directory != "~" && !strings.HasPrefix(directory, "~/") && !strings.HasPrefix(directory, "/") &&
			!windowsAbsolute {
			return "", ""
		}
		field := strings.TrimSpace(fields[0])
		if field == "" {
			return "", ""
		}
		if pfmengine.IsUUID(field) {
			return "", field
		}
		return field, ""
	}
	return "", ""
}

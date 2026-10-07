package gather

import (
	"context"
	"regexp"
	"slices"
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

var codexContextField = regexp.MustCompile(`^Context ?\d+% (left|used)$`)

// codexFooters are the native toolbar rows Codex draws below its status row.
var codexFooters = []string{"? for shortcuts", "← for agents", "→ for agents"}

// parseCodexIdentity reads only the bottommost status row: blank rows and the
// native shortcut and agent footers below it are skipped, and any other row
// ends the search, since modals can hide today's status while an older
// status-shaped transcript remains above them. A configurable status row's
// exact UUID field wins over model/project labels; a legacy row (identity,
// directory, metadata) names its thread by its first field; prose elsewhere is
// never searched.
func parseCodexIdentity(capture string) (name, threadID string) {
	last, toolbar := "", false
	lines := strings.Split(capture, "\n")
	for index := len(lines) - 1; index >= 0 && last == ""; index-- {
		line := strings.TrimSpace(lines[index])
		switch {
		case line == "":
		case slices.ContainsFunc(codexFooters, func(footer string) bool { return strings.HasPrefix(line, footer) }):
			toolbar = true
		default:
			last = line
		}
	}
	if last == "" {
		return "", ""
	}
	fields := strings.Split(last, "·")
	hasContext := false
	for _, field := range fields {
		hasContext = hasContext || codexContextField.MatchString(strings.TrimSpace(field))
	}
	legacy := len(fields) > 2 && strings.TrimSpace(fields[2]) != "" && codexDirectory(strings.TrimSpace(fields[1]))
	bareSession := toolbar && len(fields) == 1 && pfmengine.IsUUID(strings.TrimSpace(fields[0]))
	if !hasContext && !legacy && !bareSession {
		return "", ""
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if pfmengine.IsUUID(field) {
			if threadID != "" {
				return "", ""
			}
			threadID = field
		}
	}
	if threadID != "" {
		return "", threadID
	}
	field := strings.TrimSpace(fields[0])
	// Configurable model-first layouts do not provide a name without an
	// explicit name field. Guessing the model as a title breaks /clear binding.
	legacyTitle := legacy && strings.TrimSpace(fields[2]) == "Full Access"
	if field == "" || toolbar && !legacyTitle || strings.Contains(last, "Main [") {
		return "", ""
	}
	return field, ""
}

// codexDirectory reports whether a status field is the directory Codex prints:
// home, a home-relative path, or an absolute Unix or Windows path.
func codexDirectory(field string) bool {
	windowsAbsolute := len(field) > 2 && field[1] == ':' && (field[2] == '\\' || field[2] == '/')
	return field == "~" || strings.HasPrefix(field, "~/") || strings.HasPrefix(field, "/") || windowsAbsolute
}

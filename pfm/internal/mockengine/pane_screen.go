package mockengine

import (
	"fmt"
	"strings"
)

// pane is the screen: the last lines of conversation, an optional busy
// footer or open menu, the background-agent rows, the composer and the
// statusline. Every change repaints the whole thing, so a capture sees only
// what is true now.
type pane struct {
	history     []string
	footer      string
	menu        *Step
	selected    int
	agents      agentPanel
	draft       composer
	queued      []string
	status      string
	glyph       string
	placeholder string
	rule        string
	renameDraft *composer
	renameError string
	// left is set once the engine exits by /exit: like Claude Code and Codex
	// unmounting their input box, the last frame draws no composer, so a
	// reader of the pane never takes the ended engine's box for the next one's.
	left bool
}

const historyKeep = 12

// claudeComposerRule is a horizontal rule of Claude Code's input box, drawn
// above and below its composer row; the kill-exit finisher reads the box down
// to the closing one (pfmcall.ComposerBoxEmpty). Codex draws no box.
const claudeComposerRule = "────────────────────────────────────────\r\n"

// These dialog strings are read by internal/spawn/spawn.go's Codex birth.
const (
	codexRenameCommand = "/rename"
	codexRenameOffer   = "rename the current thread"
	codexRenameTitle   = "Rename thread"
	codexRenameHint    = "Type a name and press Enter"
	codexRenameEmpty   = "Thread name cannot be empty."
	codexRenameFooter  = "Press enter to confirm or esc to go back"
)

func (screen *pane) say(line string) {
	screen.history = append(screen.history, line)
	if len(screen.history) > historyKeep {
		screen.history = screen.history[len(screen.history)-historyKeep:]
	}
}

func (screen *pane) render() string {
	var out strings.Builder
	out.WriteString("\x1b[2J\x1b[H")
	for _, line := range screen.history {
		out.WriteString(line)
		out.WriteString("\r\n")
	}
	if screen.footer != "" {
		out.WriteString("\r\n" + screen.footer + "\r\n")
	}
	if screen.left {
		return out.String()
	}
	if screen.renameDraft != nil {
		out.WriteString("\r\n" + codexRenameTitle + "\r\n" + codexRenameHint + "\r\n")
		out.WriteString(screen.glyph + " " + screen.renameDraft.text() + "\r\n")
		if screen.renameError != "" {
			out.WriteString(screen.renameError + "\r\n")
		}
		out.WriteString(codexRenameFooter + "\r\n")
		return out.String()
	}
	if screen.menu != nil {
		out.WriteString("\r\n")
		for index, option := range screen.menu.Options {
			marker := "  "
			if index+1 == screen.selected {
				marker = screen.glyph + " "
			}
			fmt.Fprintf(&out, "%s%d. %s\r\n", marker, index+1, option)
		}
		out.WriteString("Enter to confirm · Esc to cancel\r\n")
	} else {
		draft := screen.draft.text()
		text := draft
		if text == "" {
			if screen.placeholder != "" {
				text = "\x1b[2m" + screen.placeholder + "\x1b[0m"
			}
		}
		out.WriteString(screen.agentWaitLine())
		out.WriteString("\r\n" + screen.rule + screen.glyph + " " + text + "\r\n" + screen.rule)
		if screen.placeholder != "" && draft != "" && strings.HasPrefix(codexRenameCommand, draft) {
			out.WriteString(codexRenameOffer + "\r\n")
		}
		if len(screen.queued) > 0 {
			out.WriteString("  Press up to edit queued messages\r\n")
		}
	}
	out.WriteString(screen.agents.render())
	if screen.status != "" {
		out.WriteString(screen.status + "\r\n")
	}
	return out.String()
}
